package software

import (
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"

	"github.com/gin-gonic/gin"
)

func (m *Module) runtimeEnvironments(c *gin.Context) {
	id := c.Param("id")
	if !pkgmgr.IsProjectRuntime(id) {
		response.Fail(c, response.Invalid, "运行环境无效")
		return
	}
	items, err := m.packages.RuntimeEnvironments(id)
	if err != nil {
		fail(c, err)
		return
	}
	type project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type environment struct {
		ID       string    `json:"id"`
		Version  string    `json:"version"`
		Projects []project `json:"projects"`
	}
	var sites []store.Website
	if len(items) > 0 {
		err = m.db.Select("id", "name", "runtime_environment").
			Where("project_type = ? AND runtime_environment <> ''", id).Order("name").Find(&sites).Error
		if err != nil {
			response.Fail(c, response.Internal, "")
			return
		}
	}
	projects := map[string][]project{}
	for _, site := range sites {
		projects[site.RuntimeEnvironment] = append(projects[site.RuntimeEnvironment], project{ID: site.ID, Name: site.Name})
	}
	list := make([]environment, 0, len(items))
	for _, item := range items {
		list = append(list, environment{ID: item.ID, Version: item.Version, Projects: append([]project{}, projects[item.ID]...)})
	}
	response.OK(c, gin.H{"list": list})
}
