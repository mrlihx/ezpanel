package docker

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

const (
	composeTemplateKey    = "docker.compose-template."
	composeEnvNamePattern = `[\p{L}\p{N}_.\[\]-]+`
)

var (
	composeProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	composeEnvName     = regexp.MustCompile(`^` + composeEnvNamePattern + `$`)
	composeTemplateRef = regexp.MustCompile(`\$\$|\$\{?([A-Za-z_][A-Za-z0-9_]*)`)
	// 带引号的多行值必须整体匹配，避免把值中的文本误当成变量名。
	composeEnvDeclaration = regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?(` + composeEnvNamePattern + `)[ \t]*` +
		`(?:[=:][ \t]*('(?:[^'\\]|\\[\s\S])*'|"(?:[^"\\]|\\[\s\S])*"|[^\r\n]*))?[ \t]*(?:#[^\r\n]*)?\r?$`)
)

type composeTemplateInput struct {
	Name     string   `json:"name" yaml:"name"`
	Remark   string   `json:"remark" yaml:"remark,omitempty"`
	Category string   `json:"category" yaml:"category"`
	YAML     string   `json:"yaml,omitempty" yaml:"yaml"` // 模板正文加密存储。
	EnvKeys  []string `json:"envKeys,omitempty" yaml:"envKeys,omitempty"`
}

type composeTemplate struct {
	ID string `json:"id"`
	composeTemplateInput
}

func (m *Module) composeTemplates(c *gin.Context) {
	var rows []store.Setting
	if err := m.db.Where("key LIKE ?", composeTemplateKey+"%").Find(&rows).Error; err != nil {
		fail(c, err)
		return
	}
	templates := make([]composeTemplate, 0, len(rows))
	for _, row := range rows {
		var item composeTemplate
		if err := json.Unmarshal([]byte(row.Value), &item); err != nil {
			fail(c, errors.New("无法读取 Compose 模板"))
			return
		}
		if item.Category == "" {
			item.Category = "custom"
		}
		item.YAML = ""
		templates = append(templates, item)
	}
	response.OK(c, templates)
}

func (m *Module) loadComposeTemplate(id string) (composeTemplate, error) {
	var item composeTemplate
	var row store.Setting
	if err := m.db.First(&row, "key = ?", composeTemplateKey+id).Error; err != nil {
		return item, err
	}
	if err := json.Unmarshal([]byte(row.Value), &item); err != nil {
		return item, errors.New("无法读取 Compose 模板")
	}
	if item.Category == "" {
		item.Category = "custom"
	}
	plain, err := store.Decrypt(m.cfg.AESKey, item.YAML)
	item.YAML = plain
	return item, err
}

func (m *Module) readComposeTemplate(c *gin.Context) (composeTemplate, bool) {
	id, ok := resource(c)
	if !ok {
		return composeTemplate{}, false
	}
	item, err := m.loadComposeTemplate(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.Fail(c, response.NotFound, "Compose 模板不存在")
	} else if err != nil {
		fail(c, err)
	}
	return item, err == nil
}

func (m *Module) composeTemplate(c *gin.Context) {
	if item, ok := m.readComposeTemplate(c); ok {
		response.OK(c, struct {
			composeTemplate
			Variables []string `json:"variables"`
		}{item, composeTemplateVariables(item.YAML, item.EnvKeys)})
	}
}

func composeTemplateVariables(content string, envKeys []string) []string {
	keys := make(map[string]struct{}, len(envKeys))
	for _, key := range envKeys {
		keys[key] = struct{}{}
	}
	var root yaml.Node
	if yaml.Unmarshal([]byte(content), &root) == nil {
		var visit func(*yaml.Node)
		visit = func(node *yaml.Node) {
			if node.Kind == yaml.ScalarNode {
				for _, match := range composeTemplateRef.FindAllStringSubmatch(node.Value, -1) {
					if match[1] != "" {
						keys[match[1]] = struct{}{}
					}
				}
			}
			// Compose 只插值 YAML 值。别名的目标已在锚点处遍历。
			if node.Kind == yaml.MappingNode {
				for i := 1; i < len(node.Content); i += 2 {
					visit(node.Content[i])
				}
				return
			}
			for _, child := range node.Content {
				visit(child)
			}
		}
		visit(&root)
	}
	variables := make([]string, 0, len(keys))
	for key := range keys {
		variables = append(variables, key)
	}
	sort.Strings(variables)
	return variables
}

func (m *Module) saveComposeTemplate(c *gin.Context) {
	var in composeTemplateInput
	if !response.Bind(c, &in) {
		return
	}
	id := c.Param("id")
	if id != "" {
		var ok bool
		if id, ok = resource(c); !ok {
			return
		}
	}
	m.persistComposeTemplate(c, id, in)
}

func (m *Module) persistComposeTemplate(c *gin.Context, id string, in composeTemplateInput) {
	in.Name = strings.TrimSpace(in.Name)
	in.Remark = strings.TrimSpace(in.Remark)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 || utf8.RuneCountInString(in.Remark) > 512 {
		response.Fail(c, response.Invalid, "模板名称须为 1–64 个字符，备注最多 512 个字符")
		return
	}
	switch in.Category {
	case "":
		in.Category = "custom"
	case "database", "website", "ai", "tool", "custom":
	default:
		response.Fail(c, response.Invalid, "模板分类无效")
		return
	}
	if err := validateComposeText(&in.YAML, nil); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	for _, key := range in.EnvKeys {
		if !composeEnvName.MatchString(key) {
			response.Fail(c, response.Invalid, "模板环境变量只能包含有效的变量名")
			return
		}
	}
	update := id != ""
	if !update {
		id = store.ID()
	}
	ciphertext, err := store.Encrypt(m.cfg.AESKey, in.YAML)
	if err != nil {
		fail(c, err)
		return
	}
	in.YAML = ciphertext
	item := composeTemplate{ID: id, composeTemplateInput: in}
	data, err := json.Marshal(item)
	if err != nil {
		fail(c, err)
		return
	}
	row := store.Setting{Key: composeTemplateKey + id, Value: string(data)}
	if update {
		result := m.db.Model(&row).Update("value", row.Value)
		if result.Error != nil {
			fail(c, result.Error)
			return
		}
		if result.RowsAffected == 0 {
			response.Fail(c, response.NotFound, "Compose 模板不存在")
			return
		}
	} else if err := m.db.Create(&row).Error; err != nil {
		fail(c, err)
		return
	}
	item.YAML = ""
	response.OK(c, item)
}

func (m *Module) deleteComposeTemplate(c *gin.Context) {
	if id, ok := resource(c); ok {
		res := m.db.Delete(&store.Setting{}, "key = ?", composeTemplateKey+id)
		if res.Error != nil {
			fail(c, res.Error)
			return
		}
		if res.RowsAffected == 0 {
			response.Fail(c, response.NotFound, "编排模板不存在")
			return
		}
		response.OK(c, nil)
	}
}

func (m *Module) importComposeTemplate(c *gin.Context) {
	var request struct {
		Format  string `json:"format"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if !response.Bind(c, &request) {
		return
	}
	if request.Format != "json" && request.Format != "yaml" {
		response.Fail(c, response.Invalid, "请选择 JSON 或 YAML 模板文件")
		return
	}
	content := []byte(strings.TrimPrefix(request.Content, "\ufeff"))
	if request.Format == "json" && !json.Valid(content) {
		response.Fail(c, response.Invalid, "模板 JSON 格式无效")
		return
	}
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		response.Fail(c, response.Invalid, "模板文件须包含 Compose 配置")
		return
	}
	var in composeTemplateInput
	config := root.Content[0]
	for i := 0; i < len(config.Content); i += 2 {
		if config.Content[i].Value == "yaml" {
			if err := config.Decode(&in); err != nil {
				response.Fail(c, response.Invalid, "模板内容格式无效")
				return
			}
			m.persistComposeTemplate(c, "", in)
			return
		}
	}
	for i := 0; i < len(config.Content); i += 2 {
		if config.Content[i].Value == "services" || config.Content[i].Value == "include" {
			m.persistComposeTemplate(c, "", composeTemplateInput{Name: request.Name, YAML: string(content)})
			return
		}
	}
	response.Fail(c, response.Invalid, "模板文件须包含 Compose 配置")
}

func (m *Module) exportComposeTemplate(c *gin.Context) {
	item, ok := m.readComposeTemplate(c)
	if !ok {
		return
	}
	format := c.DefaultQuery("format", "yaml")
	var content []byte
	var err error
	switch format {
	case "json":
		content, err = json.MarshalIndent(item.composeTemplateInput, "", "  ")
	case "yaml":
		content, err = yaml.Marshal(item.composeTemplateInput)
	default:
		response.Fail(c, response.Invalid, "导出格式须为 JSON 或 YAML")
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	filename := strings.NewReplacer("/", "_", `\`, "_").Replace(item.Name)
	response.OK(c, gin.H{"filename": filename + "." + format, "content": string(content)})
}

func (m *Module) saveComposeProjectTemplate(c *gin.Context) {
	var in struct {
		ProjectName string `json:"projectName"`
		Name        string `json:"name"`
		Remark      string `json:"remark"`
		Category    string `json:"category"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !validExistingComposeName(in.ProjectName) {
		response.Fail(c, response.Invalid, "Compose 项目名称无效")
		return
	}
	project, err := m.getComposeProject(c.Request.Context(), in.ProjectName)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.Fail(c, response.NotFound, "Compose 项目不存在")
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	if len(project.ComposeFiles) > 1 {
		response.Fail(c, response.Invalid, "模板支持单个 Compose 文件，含多个配置文件的项目暂不能另存为模板")
		return
	}
	content, env, err := m.readComposeProjectFiles(project)
	if err != nil {
		fail(c, err)
		return
	}
	keys := []string{}
	for _, match := range composeEnvDeclaration.FindAllStringSubmatch(strings.TrimPrefix(env, "\ufeff"), -1) {
		keys = append(keys, match[1])
	}
	sort.Strings(keys)
	m.persistComposeTemplate(c, "", composeTemplateInput{
		Name: in.Name, Remark: in.Remark, Category: in.Category, YAML: content, EnvKeys: slices.Compact(keys),
	})
}

func (m *Module) deployComposeTemplate(c *gin.Context) {
	var in struct {
		Project string  `json:"project"`
		Env     *string `json:"env"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !composeProjectName.MatchString(in.Project) {
		response.Fail(c, response.Invalid, "项目名须以小写字母或数字开头，最多 63 个字符，可包含 - 和 _")
		return
	}
	item, ok := m.readComposeTemplate(c)
	if !ok {
		return
	}
	m.submitComposeCreate(c, composeCreateInput{
		Name: "ezpanel-" + in.Project, TemplateID: item.ID, YAML: &item.YAML, Env: in.Env,
		Deploy: true, legacyTemplate: true,
	})
}
