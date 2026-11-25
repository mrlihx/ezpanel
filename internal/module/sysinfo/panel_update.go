package sysinfo

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/version"
	"github.com/gin-gonic/gin"
)

const (
	panelRepoURL     = "https://github.com/mrlihx/ezpanel"
	panelReleaseAPI  = "https://api.github.com/repos/mrlihx/ezpanel/releases"
	panelReleaseDown = "https://github.com/mrlihx/ezpanel/releases/download"
)

// 与 scripts/install.sh 中保持一致的官方发布验签公钥。
const releasePublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBojANBgkqhkiG9w0BAQEFAAOCAY8AMIIBigKCAYEAt+eYEoelJoOvpWvav2ov
A8CIp5+MafjGNox/ceb1O82lHUD5lqXZqO7cVlTICEwKPrL3vtzhWaJ3NeUTznM3
t/6vtGPHvMlhvSp6DS32Cyewl9k5AXuZb3czJY+JFmjy7wt6ByaOr3724rUI0HaZ
Ayy/RHIWn/z0CzbXYVdeKcQQO7/SAJ6SWezE+FRGcEVpArF2gcvLwQ/NAY6iIN8Z
5S5cavUd0DqqZqm9twa0Utjxe7eT/1htHP9u+V3mIoBkmoX3xL+e3ySoWjzeX6Yt
27c62Rcq8aMsazN87qj1N3H7ZaX3CCbS9okHptE36kcdOgsdTpByYPP5TgmKbMf+
rLBW9t2WGVs5bBqxHD4wwryLVZKdqyz+eQHfhXykeBZ2+pqlTmmx1ZfdxIFZFzMo
DAx5p2vnjS0ByDkfQ0X/RFl4RFBtkT3raZiIg5aXrVXd65JOKKpbsqKFYg17qfkM
Ylbws6P+LqytD+sDRE/MA3sSdRQYE2Yq0e7gjQAc9WWBAgMBAAE=
-----END PUBLIC KEY-----`

var releaseTagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-r[1-9][0-9]*)?$`)

type PanelStatus struct {
	PanelVersion    string `json:"panelVersion"`
	PublicIP        string `json:"publicIp"`
	RepoURL         string `json:"repoUrl"`
	LatestVersion   string `json:"latestVersion"`
	LatestTag       string `json:"latestTag"`
	HasUpdate       bool   `json:"hasUpdate"`
	ReleaseURL      string `json:"releaseUrl"`
	ReleaseNotes    string `json:"releaseNotes,omitempty"`
	PublishedAt     string `json:"publishedAt,omitempty"`
	CheckedAt       string `json:"checkedAt,omitempty"`
	LastUpdateError string `json:"lastUpdateError,omitempty"`
}

type panelMetaCache struct {
	mu            sync.RWMutex
	publicIP      string
	publicIPAt    time.Time
	latestVersion string
	latestTag     string
	hasUpdate     bool
	releaseURL    string
	releaseNotes  string
	publishedAt   string
	checkedAt     time.Time
	updating      bool
}

func (m *Module) startPanelMetaLoop(ctx context.Context) {
	go func() {
		// 启动后稍等 3 秒异步探测外网 IP 与最新版本，不阻塞启动。
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		m.refreshPublicIP(ctx)
		_, _ = m.checkPanelUpdate(ctx)

		daily := time.NewTicker(24 * time.Hour)
		defer daily.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-daily.C:
				m.refreshPublicIP(ctx)
				_, _ = m.checkPanelUpdate(ctx)
			}
		}
	}()
}

func (m *Module) panelUpdateErrorFile() string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		wd = "/opt/ezpanel"
	}
	return filepath.Join(wd, "data", "panel-update-error.txt")
}

func (m *Module) readLastUpdateError() string {
	raw, err := os.ReadFile(m.panelUpdateErrorFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (m *Module) refreshPublicIP(ctx context.Context) string {
	m.panelMeta.mu.RLock()
	if m.panelMeta.publicIP != "" && time.Since(m.panelMeta.publicIPAt) < 30*time.Minute {
		ip := m.panelMeta.publicIP
		m.panelMeta.mu.RUnlock()
		return ip
	}
	m.panelMeta.mu.RUnlock()

	endpoints := []string{
		"https://4.ipw.cn",
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, ep := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "ezpanel/"+version.Version)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			continue
		}
		candidate := strings.TrimSpace(string(body))
		if ip := net.ParseIP(candidate); ip != nil {
			m.panelMeta.mu.Lock()
			m.panelMeta.publicIP = ip.String()
			m.panelMeta.publicIPAt = time.Now()
			m.panelMeta.mu.Unlock()
			return ip.String()
		}
	}
	m.panelMeta.mu.RLock()
	ip := m.panelMeta.publicIP
	m.panelMeta.mu.RUnlock()
	return ip
}

func compareSemver(a, b string) int {
	pa := parseSemverParts(a)
	pb := parseSemverParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] > pb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parseSemverParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		out[i], _ = strconv.Atoi(parts[i])
	}
	return out
}

func stripReleaseVersion(tag string) string {
	v := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if idx := strings.Index(v, "-r"); idx >= 0 {
		v = v[:idx]
	}
	return v
}

func isNewerPanelRelease(current, latestVersion string) bool {
	if latestVersion == "" {
		return false
	}
	cur := strings.TrimSpace(current)
	if cur == "" || strings.Contains(cur, "dev") {
		return false
	}
	return compareSemver(latestVersion, cur) > 0
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
}

func fetchLatestGitHubRelease(ctx context.Context) (githubRelease, error) {
	client := &http.Client{Timeout: 12 * time.Second}
	fetch := func(url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "ezpanel/"+version.Version)
		return client.Do(req)
	}
	resp, err := fetch(panelReleaseAPI + "/latest")
	if err != nil {
		return githubRelease{}, errors.New("无法连接 GitHub 获取最新版本")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var rel githubRelease
		if err := json.NewDecoder(io.LimitReader(resp.Body, 512<<10)).Decode(&rel); err != nil {
			return githubRelease{}, errors.New("解析 GitHub 发布信息失败")
		}
		if !releaseTagPattern.MatchString(rel.TagName) {
			return githubRelease{}, errors.New("官方发布标签格式无效")
		}
		return rel, nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return githubRelease{}, fmt.Errorf("GitHub API 返回异常状态 (%d)", resp.StatusCode)
	}
	// 无正式版时回退读取最新发布（含预览版）
	resp2, err := fetch(panelReleaseAPI + "?per_page=1")
	if err != nil {
		return githubRelease{}, errors.New("无法连接 GitHub 获取预览版列表")
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("GitHub API 返回异常状态 (%d)", resp2.StatusCode)
	}
	var list []githubRelease
	if err := json.NewDecoder(io.LimitReader(resp2.Body, 512<<10)).Decode(&list); err != nil || len(list) == 0 {
		return githubRelease{}, errors.New("尚未找到可用的官方发布版本")
	}
	if !releaseTagPattern.MatchString(list[0].TagName) {
		return githubRelease{}, errors.New("官方发布标签格式无效")
	}
	return list[0], nil
}

func (m *Module) checkPanelUpdate(ctx context.Context) (PanelStatus, error) {
	rel, err := fetchLatestGitHubRelease(ctx)
	if err != nil {
		return m.panelStatusSnapshot(), err
	}
	latestVer := stripReleaseVersion(rel.TagName)
	hasUpdate := isNewerPanelRelease(version.Version, latestVer)
	notes := strings.TrimSpace(rel.Body)
	if len(notes) > 2000 {
		notes = notes[:2000] + "\n..."
	}
	m.panelMeta.mu.Lock()
	m.panelMeta.latestTag = rel.TagName
	m.panelMeta.latestVersion = latestVer
	m.panelMeta.hasUpdate = hasUpdate
	m.panelMeta.releaseURL = rel.HTMLURL
	m.panelMeta.releaseNotes = notes
	m.panelMeta.publishedAt = rel.PublishedAt
	m.panelMeta.checkedAt = time.Now().UTC()
	m.panelMeta.mu.Unlock()
	return m.panelStatusSnapshot(), nil
}

func (m *Module) panelStatusSnapshot() PanelStatus {
	m.panelMeta.mu.RLock()
	defer m.panelMeta.mu.RUnlock()
	checkedAt := ""
	if !m.panelMeta.checkedAt.IsZero() {
		checkedAt = m.panelMeta.checkedAt.Format(time.RFC3339)
	}
	return PanelStatus{
		PanelVersion:    version.Version,
		PublicIP:        m.panelMeta.publicIP,
		RepoURL:         panelRepoURL,
		LatestVersion:   m.panelMeta.latestVersion,
		LatestTag:       m.panelMeta.latestTag,
		HasUpdate:       m.panelMeta.hasUpdate,
		ReleaseURL:      m.panelMeta.releaseURL,
		ReleaseNotes:    m.panelMeta.releaseNotes,
		PublishedAt:     m.panelMeta.publishedAt,
		CheckedAt:       checkedAt,
		LastUpdateError: m.readLastUpdateError(),
	}
}

func (m *Module) panelStatus(c *gin.Context) {
	if m.panelMeta.publicIP == "" {
		go m.refreshPublicIP(context.Background())
	}
	response.OK(c, m.panelStatusSnapshot())
}

func (m *Module) panelUpdateCheck(c *gin.Context) {
	status, err := m.checkPanelUpdate(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	response.OK(c, status)
}

func (m *Module) dismissPanelUpdateError(c *gin.Context) {
	_ = os.Remove(m.panelUpdateErrorFile())
	response.OK(c, nil)
}

func verifyReleaseSignature(sumsData, sigData []byte) error {
	block, _ := pem.Decode([]byte(releasePublicKeyPEM))
	if block == nil {
		return errors.New("内置发布公钥格式无效")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return errors.New("内置发布公钥解析失败")
	}
	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		return errors.New("内置发布公钥类型无效")
	}
	digest := sha256.Sum256(sumsData)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sigData); err != nil {
		return errors.New("官方发布签名验证失败，已中止更新")
	}
	return nil
}

func extractExpectedSHA256(sumsData []byte, filename string) (string, error) {
	found := ""
	for line := range strings.SplitSeq(string(sumsData), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == filename {
			if found != "" {
				return "", errors.New("SHA256SUMS 中存在重复条目")
			}
			h := strings.ToLower(fields[0])
			if len(h) != 64 {
				return "", errors.New("SHA256SUMS 校验和格式无效")
			}
			found = h
		}
	}
	if found == "" {
		return "", errors.New("SHA256SUMS 中未找到当前架构的发布包")
	}
	return found, nil
}

func downloadToFile(ctx context.Context, url, dst string, maxBytes int64) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ezpanel/"+version.Version)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败，远端返回状态码 %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return "", fmt.Errorf("写入下载文件失败：%w", copyErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > maxBytes {
		return "", errors.New("下载文件超出大小限制")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (m *Module) panelUpdateApply(c *gin.Context) {
	var in struct {
		Tag string `json:"tag"`
	}
	_ = c.ShouldBindJSON(&in)

	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		response.Fail(c, response.Invalid, "在线更新仅支持 Linux amd64/arm64 环境")
		return
	}
	ctx := c.Request.Context()
	var running int64
	if m.db == nil || m.db.WithContext(ctx).Model(&store.Task{}).Where("status = ?", "running").Count(&running).Error != nil {
		response.Fail(c, response.System, "无法读取面板任务状态")
		return
	}
	if running > 0 {
		response.Fail(c, response.Conflict, "当前有正在执行的后台任务，请等待任务完成后再更新面板")
		return
	}
	systemctl, err := executor.LookPath("systemctl")
	if err != nil {
		response.Fail(c, response.Invalid, "此环境不支持 systemd，无法执行在线更新")
		return
	}
	systemdRun, err := executor.LookPath("systemd-run")
	if err != nil {
		response.Fail(c, response.Invalid, "此环境缺少 systemd-run，无法执行安全在线更新")
		return
	}
	const unit = "ezpanel.service"
	pidRes, err := executor.Run(ctx, 5*time.Second, systemctl, "show", "--property=MainPID", "--value", unit)
	if err != nil || strings.TrimSpace(pidRes.Stdout) != strconv.Itoa(os.Getpid()) {
		response.Fail(c, response.Invalid, "当前面板未由 ezpanel.service 托管，请使用命令行脚本更新")
		return
	}
	exePath, err := os.Executable()
	if err != nil {
		response.Fail(c, response.System, "无法确定当前面板程序路径")
		return
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	installDir, err := os.Getwd()
	if err != nil || installDir == "" {
		installDir = "/opt/ezpanel"
	}

	m.panelMeta.mu.Lock()
	if m.panelMeta.updating {
		m.panelMeta.mu.Unlock()
		response.Fail(c, response.Conflict, "面板更新已在执行中，请勿重复操作")
		return
	}
	m.panelMeta.updating = true
	m.panelMeta.mu.Unlock()
	released := false
	release := func() {
		if !released {
			released = true
			m.panelMeta.mu.Lock()
			m.panelMeta.updating = false
			m.panelMeta.mu.Unlock()
		}
	}
	defer release()

	tag := strings.TrimSpace(in.Tag)
	if tag == "" {
		rel, err := fetchLatestGitHubRelease(ctx)
		if err != nil {
			response.Fail(c, response.System, response.Summary(err.Error()))
			return
		}
		tag = rel.TagName
	}
	if !releaseTagPattern.MatchString(tag) {
		response.Fail(c, response.Invalid, "发布标签格式无效")
		return
	}
	targetVer := stripReleaseVersion(tag)
	artifact := "ezpanel-linux-" + runtime.GOARCH
	baseURL := panelReleaseDown + "/" + tag

	workDir, err := os.MkdirTemp("", "ezpanel-self-update-*")
	if err != nil {
		response.Fail(c, response.System, "创建临时更新目录失败")
		return
	}
	keepWorkDir := false
	defer func() {
		if !keepWorkDir {
			_ = os.RemoveAll(workDir)
		}
	}()

	sumsPath := filepath.Join(workDir, "SHA256SUMS")
	sigPath := filepath.Join(workDir, "SHA256SUMS.sig")
	newBinPath := filepath.Join(workDir, "ezpanel.new")

	if _, err := downloadToFile(ctx, baseURL+"/SHA256SUMS", sumsPath, 1<<20); err != nil {
		response.Fail(c, response.System, "下载校验清单失败："+err.Error())
		return
	}
	if _, err := downloadToFile(ctx, baseURL+"/SHA256SUMS.sig", sigPath, 1<<20); err != nil {
		response.Fail(c, response.System, "下载官方签名失败："+err.Error())
		return
	}
	sumsData, err := os.ReadFile(sumsPath)
	if err != nil {
		response.Fail(c, response.System, "读取校验清单失败")
		return
	}
	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		response.Fail(c, response.System, "读取官方签名失败")
		return
	}
	if err := verifyReleaseSignature(sumsData, sigData); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	expectedHash, err := extractExpectedSHA256(sumsData, artifact)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	actualHash, err := downloadToFile(ctx, baseURL+"/"+artifact, newBinPath, 256<<20)
	if err != nil {
		response.Fail(c, response.System, "下载新版本程序失败："+err.Error())
		return
	}
	if actualHash != expectedHash {
		response.Fail(c, response.Invalid, "下载文件 SHA-256 校验不匹配，已中止更新")
		return
	}
	if err := os.Chmod(newBinPath, 0755); err != nil {
		response.Fail(c, response.System, "设置新版本执行权限失败")
		return
	}
	verRes, err := executor.Run(ctx, 10*time.Second, newBinPath, "version")
	if err != nil || strings.TrimSpace(verRes.Stdout) != targetVer {
		response.Fail(c, response.Invalid, "新版本程序自检失败（版本号不匹配或无法执行），已中止更新")
		return
	}

	// 备份当前二进制，通过独立 systemd-run 单元执行原子替换 + 健康检查 + 失败自动回退。
	bakPath := exePath + ".bak"
	origData, err := os.ReadFile(exePath)
	if err != nil {
		response.Fail(c, response.System, "读取当前面板程序备份失败")
		return
	}
	if err := os.WriteFile(bakPath, origData, 0755); err != nil {
		response.Fail(c, response.System, "写入旧版本备份失败，已中止更新")
		return
	}

	errFile := m.panelUpdateErrorFile()
	_ = os.Remove(errFile)
	scriptPath := filepath.Join(workDir, "apply.sh")
	script := fmt.Sprintf(`#!/bin/bash
set -u
BIN=%q
BAK=%q
NEW=%q
ERRFILE=%q
INSTALL_DIR=%q
WORK=%q
rm -f "$ERRFILE"
if ! mv -f "$NEW" "$BIN"; then
  echo "替换面板程序文件失败，已保持原版本运行。" > "$ERRFILE"
  rm -rf "$WORK"
  exit 1
fi
chmod 755 "$BIN"
systemctl restart ezpanel.service
ok=false
for i in $(seq 1 25); do
  if systemctl is-active --quiet ezpanel.service && "$BIN" ready --install-dir "$INSTALL_DIR" >/dev/null 2>&1; then
    ok=true
    break
  fi
  sleep 1
done
if [ "$ok" = true ]; then
  rm -f "$BAK" "$ERRFILE"
else
  reason="新版本启动健康检查未通过，已自动回退到原版本。"
  last_err=$(journalctl -u ezpanel.service -n 8 --no-pager 2>/dev/null | tail -n 3 | tr '\n' ' ')
  if [ -n "$last_err" ]; then
    reason="$reason 最近日志：$last_err"
  fi
  printf '%%s\n' "$reason" > "$ERRFILE"
  if [ -f "$BAK" ]; then
    mv -f "$BAK" "$BIN"
    chmod 755 "$BIN"
    systemctl restart ezpanel.service
  fi
fi
rm -rf "$WORK"
`, exePath, bakPath, newBinPath, errFile, installDir, workDir)

	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		_ = os.Remove(bakPath)
		response.Fail(c, response.System, "写入更新执行脚本失败")
		return
	}
	job := []string{"--quiet", "--collect", "--unit=ezpanel-self-update", "--on-active=2s", "--timer-property=AccuracySec=1s", "/bin/bash", scriptPath}
	if _, err := executor.Run(ctx, 10*time.Second, systemdRun, job...); err != nil {
		_ = os.Remove(bakPath)
		response.Fail(c, response.System, "调度更新任务失败，请检查 systemd 状态")
		return
	}
	keepWorkDir = true
	response.OK(c, gin.H{"updating": true, "targetVersion": targetVer, "targetTag": tag})
}
