package website

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func rewriteTokens(rule string) []string {
	tokens := configToken.FindAllString(rule, -1)
	out := tokens[:0]
	for _, token := range tokens {
		if !strings.HasPrefix(token, "#") {
			out = append(out, token)
		}
	}
	return out
}
func hasRootLocation(rule string) bool {
	tokens := rewriteTokens(rule)
	depth := 0
	for i := 0; i < len(tokens); i++ {
		if depth == 0 && tokens[i] == "location" {
			if i+2 < len(tokens) && tokens[i+1] == "/" && tokens[i+2] == "{" {
				return true
			}
			if i+3 < len(tokens) && tokens[i+1] == "^~" && tokens[i+2] == "/" && tokens[i+3] == "{" {
				return true
			}
		}
		switch tokens[i] {
		case "{":
			depth++
		case "}":
			if depth > 0 {
				depth--
			}
		}
	}
	return false
}

func hasAnyLocation(rule string) bool {
	tokens := rewriteTokens(rule)
	depth := 0
	for i := 0; i < len(tokens); i++ {
		if depth == 0 && tokens[i] == "location" {
			return true
		}
		switch tokens[i] {
		case "{":
			depth++
		case "}":
			if depth > 0 {
				depth--
			}
		}
	}
	return false
}

// 兼容宝塔风格伪静态：允许多个 location 块及各类 server/location 级指令，
// 仅禁止闭合外层 server 块、声明顶层块或 include 外部文件，最终由 openresty -t 校验语法。
func validateRewrite(rule string) error {
	if len(rule) > 64<<10 || strings.ContainsRune(rule, 0) {
		return errors.New("伪静态规则不能超过64KiB或包含空字符")
	}
	tokens := rewriteTokens(rule)
	depth := 0
	directive := true
	for _, token := range tokens {
		if directive && token != "}" {
			switch strings.ToLower(token) {
			case "server", "http", "events", "stream", "upstream", "mail":
				return errors.New("伪静态中不能包含外层配置块：" + token)
			case "include", "load_module":
				return errors.New("伪静态中不允许使用指令：" + token)
			}
			directive = false
		}
		switch token {
		case "{":
			depth++
			directive = true
		case "}":
			depth--
			if depth < 0 {
				return errors.New("伪静态大括号不匹配")
			}
			directive = true
		case ";":
			directive = true
		}
	}
	if depth != 0 || (!directive && len(tokens) > 0) {
		return errors.New("伪静态规则缺少分号或大括号不匹配")
	}
	return nil
}
func (m *Module) saveRewrite(c *gin.Context) {
	var in struct {
		Rule string `json:"rule"`
	}
	if !response.Bind(c, &in) {
		return
	}
	in.Rule = strings.TrimSpace(in.Rule)
	if err := validateRewrite(in.Rule); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	release, err := m.tasks.Acquire("website:" + c.Param("id"))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.Type != "static" && ((row.ProjectType != "go" && row.ProjectType != "nodejs") || row.RuntimeEnvironment == "") {
		response.Fail(c, response.Invalid, "反向代理站点不使用本地伪静态")
		return
	}
	if row.Type != "static" && hasAnyLocation(in.Rule) {
		response.Fail(c, response.Invalid, "代理站点伪静态请直接填写 location / 内的重写规则")
		return
	}
	if in.Rule == "" {
		auditSite(c, "清空伪静态")
	} else {
		auditSite(c, "", fmt.Sprintf("保存伪静态规则，共 %d 行", strings.Count(in.Rule, "\n")+1))
	}
	row.RewriteRule = in.Rule
	if err := m.change(c.Request.Context(), row, false, nil, nil, func() error { return m.db.Save(&row).Error }); err != nil {
		failure(c, err)
		return
	}
	response.OK(c, m.view(row, m.running(c.Request.Context())))
}
func validateRedirect(row store.Website) error {
	if !row.RedirectEnabled {
		return nil
	}
	if row.RedirectCode != 301 && row.RedirectCode != 302 {
		return errors.New("重定向类型仅支持301或302")
	}
	if strings.IndexFunc(row.RedirectTarget, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`"'\{};$`, r)
	}) >= 0 {
		return errors.New("重定向地址含不支持的字符，请使用URL编码")
	}
	target, err := url.Parse(row.RedirectTarget)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return errors.New("重定向目标需要完整HTTP或HTTPS地址")
	}
	if row.RedirectKeepURI && target.RawQuery != "" {
		return errors.New("保留请求路径时，目标地址不能自带查询参数")
	}
	port := 80
	if target.Scheme == "https" {
		port = 443
	}
	if target.Port() != "" {
		port, err = strconv.Atoi(target.Port())
		if err != nil || port < 1 || port > 65535 {
			return errors.New("目标端口需要1至65535")
		}
	}
	host := strings.ToLower(target.Hostname())
	for _, server := range siteServers(row) {
		if server.Port != port || server.SSL != (target.Scheme == "https") {
			continue
		}
		for _, name := range server.Hosts {
			name = strings.Trim(name, "[]")
			if name == host || (strings.HasPrefix(name, "*.") && strings.HasSuffix(host, name[1:])) {
				return errors.New("重定向目标仍由本站处理，会形成循环，请使用其他域名或端口")
			}
		}
	}
	return nil
}
func (m *Module) saveRedirect(c *gin.Context) {
	var in struct {
		Enabled bool   `json:"enabled"`
		Target  string `json:"target"`
		Code    int    `json:"code"`
		KeepURI bool   `json:"keepUri"`
	}
	if !response.Bind(c, &in) {
		return
	}
	release, err := m.tasks.Acquire("website:" + c.Param("id"))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.row(c)
	if !ok {
		return
	}
	old := row
	row.RedirectEnabled, row.RedirectTarget, row.RedirectCode, row.RedirectKeepURI = in.Enabled, strings.TrimSpace(in.Target), in.Code, in.KeepURI
	if err := validateRedirect(row); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	auditSite(c, "", "重定向 "+auditSwitch(old.RedirectEnabled)+" → "+auditSwitch(row.RedirectEnabled),
		fmt.Sprintf("目标 %s → %s", auditURL(old.RedirectTarget), auditURL(row.RedirectTarget)),
		fmt.Sprintf("状态码 %d → %d", old.RedirectCode, row.RedirectCode),
		"保留原 URI "+auditSwitch(old.RedirectKeepURI)+" → "+auditSwitch(row.RedirectKeepURI))
	if err := m.change(c.Request.Context(), row, false, nil, nil, func() error { return m.db.Save(&row).Error }); err != nil {
		failure(c, err)
		return
	}
	response.OK(c, m.view(row, m.running(c.Request.Context())))
}
func (m *Module) sslCertificate(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	certificate, privateKey := "", ""
	if row.SSLEnabled {
		certPath, keyPath := m.certPaths(row.ID)
		data, err := os.ReadFile(certPath)
		if err != nil {
			failure(c, err)
			return
		}
		certificate = string(data)
		if keyData, err := os.ReadFile(keyPath); err == nil {
			privateKey = string(keyData)
		}
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"certificate": certificate, "privateKey": privateKey})
}
