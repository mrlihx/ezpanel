package software

import (
	"encoding/json"

	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

func (m *Module) saveDockerRegistrySettings(c *gin.Context) {
	var in struct {
		Mirrors            []string `json:"mirrors"`
		InsecureRegistries []string `json:"insecureRegistries"`
		Revision           string   `json:"revision"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Mirrors == nil || in.InsecureRegistries == nil || len(in.Revision) != 64 {
		response.Fail(c, response.Invalid, "仓库配置或修订号无效")
		return
	}
	ctx := c.Request.Context()
	current, err := m.packages.Settings(ctx, "docker")
	if err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	if current.Revision != in.Revision {
		response.Fail(c, response.Conflict, "配置已被修改，请重新加载后保存")
		return
	}
	// 保留其他原生配置的 JSON 值，包括大整数和嵌套对象。
	var document map[string]json.RawMessage
	if err = json.Unmarshal([]byte(current.Content), &document); err != nil || document == nil {
		response.Fail(c, response.Invalid, "请先在全部配置中修正 daemon.json")
		return
	}
	for key, addresses := range map[string][]string{
		"registry-mirrors":    in.Mirrors,
		"insecure-registries": in.InsecureRegistries,
	} {
		if len(addresses) == 0 {
			delete(document, key)
			continue
		}
		document[key], _ = json.Marshal(addresses)
	}
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil || len(content) >= 1<<20 {
		response.Fail(c, response.Invalid, "Docker配置无效或超过1MiB")
		return
	}
	if err = m.packages.SaveSettings(ctx, "docker", string(content)+"\n", in.Revision); err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	m.packages.MarkDefaultDockerMirrorsInitialized()
	response.OK(c, gin.H{"saved": true})
}
