package software

import (
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

func (m *Module) rsyncVisual(c *gin.Context) {
	if c.Request.Method == "PUT" {
		var in pkgmgr.RsyncVisual
		if !response.Bind(c, &in) {
			return
		}
		m.rsyncMu.Lock()
		defer m.rsyncMu.Unlock()
		if err := m.packages.SaveRsyncVisual(c.Request.Context(), in); err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
	}
	out, err := m.packages.RsyncVisual(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, out)
}

func (m *Module) rsyncReceiverPassword(c *gin.Context) {
	password, err := m.packages.RsyncReceiverPassword(c.Request.Context(), c.Param("name"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"password": password})
}
