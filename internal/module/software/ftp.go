package software

import (
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"

	"github.com/gin-gonic/gin"
)

func (m *Module) ftpRoutes(r *gin.RouterGroup) {
	r.GET("/ftp/visual", m.ftpVisual)
	r.PUT("/ftp/visual", m.ftpVisual)
	r.GET("/ftp/users", m.ftpUsers)
	r.POST("/ftp/users", m.changeFTPUser)
}

func (m *Module) ftpVisual(c *gin.Context) {
	if c.Request.Method == "PUT" {
		var in pkgmgr.FTPVisual
		if !response.Bind(c, &in) {
			return
		}
		release, err := m.tasks.Acquire("software-install:ftp")
		if err != nil {
			fail(c, err)
			return
		}
		defer release()
		if err = m.packages.SaveFTPSettings(c.Request.Context(), in); err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
	}
	settings, err := m.packages.FTPSettings(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, settings)
}

func (m *Module) ftpUsers(c *gin.Context) {
	rows, err := m.packages.FTPUsers(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, rows)
}

func (m *Module) changeFTPUser(c *gin.Context) {
	var in struct {
		Action string `json:"action"`
		pkgmgr.FTPUser
	}
	if !response.Bind(c, &in) {
		return
	}
	release, err := m.tasks.Acquire("software-install:ftp")
	if err != nil {
		fail(c, err)
		return
	}
	defer release()
	if err = m.packages.ChangeFTPUser(c.Request.Context(), in.Action, in.FTPUser); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error(), in.Password))
		return
	}
	response.OK(c, nil)
}
