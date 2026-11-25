package oplog

import (
	"fmt"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

type appLogTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (m *Module) appLogTargets(c *gin.Context) {
	app := c.Param("app")
	ctx := c.Request.Context()
	targets := []appLogTarget{}
	if pkgmgr.IsDatabaseContainer(app) {
		rows, err := m.packages.DatabaseInstances(ctx, app)
		if err != nil {
			response.Fail(c, response.System, "数据库实例读取失败")
			return
		}
		for _, row := range rows {
			targets = append(targets, appLogTarget{ID: row.ID, Name: fmt.Sprintf("%s · %s · %d", row.Name, row.Version, row.Port)})
		}
	}
	response.OK(c, targets)
}

func (m *Module) appLogSources(c *gin.Context) ([]pkgmgr.LogSource, error) {
	app, target := c.Param("app"), c.Query("target")
	ctx := c.Request.Context()
	switch {
	case app == "php":
		if !pkgmgr.IsPHP(target) || !pkgmgr.ValidID(target) {
			return []pkgmgr.LogSource{}, nil
		}
		return m.packages.LogSources(ctx, target)
	case pkgmgr.IsDatabaseContainer(app) && target != "":
		row, err := m.packages.DatabaseInstance(ctx, app, target)
		if err != nil {
			return nil, err
		}
		return m.packages.DatabaseLogSources(ctx, app, row)
	case pkgmgr.IsProjectRuntime(app):
		return []pkgmgr.LogSource{}, nil
	default:
		return m.packages.LogSources(ctx, app)
	}
}
