package file

import (
	"context"
	"ezpanel/internal/auth"
	"github.com/gin-gonic/gin"
	"io"
	"time"
)

// 长上传、下载与预览沿用建连时的会话，每分钟检查撤销。
func (m *Module) streamSession(c *gin.Context) {
	if m.auth == nil {
		c.Next()
		return
	}
	switch c.FullPath() {
	case "/api/files/download", "/api/files/upload", "/api/files/preview", "/api/files/uploads/:id", "/api/files/uploads/:id/finish":
	default:
		c.Next()
		return
	}
	session := auth.Current(c)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	body := c.Request.Body
	go func() {
		ticker := time.NewTicker(auth.SessionRecheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, e := m.auth.CheckSession(session.Token); e != nil {
					cancel()
					body.Close()
					return
				}
			}
		}
	}()
	c.Next()
}

type contextReadSeeker struct {
	ctx context.Context
	io.ReadSeeker
}

func (r contextReadSeeker) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.ReadSeeker.Read(p)
}
