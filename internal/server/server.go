package server

import (
	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"ezpanel/web"
	"github.com/gin-gonic/gin"
)

type Module interface {
	Name() string
	Routes(*gin.RouterGroup)
}
type Options struct {
	Auth  *auth.Service
	Tasks *task.Manager
}
type Server struct {
	*gin.Engine
	protected *gin.RouterGroup
	auth      *auth.Service
}

func (s *Server) RegisterModule(module Module) {
	module.Routes(s.protected.Group("", s.auth.Audit(module.Name())))
}
func New(options Options) (*Server, error) {
	files, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return nil, err
	}
	router := gin.New()
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false
	if options.Auth == nil {
		return nil, fmt.Errorf("认证服务未初始化")
	}
	router.Use(func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler || c.Writer.Written() {
					panic(http.ErrAbortHandler)
				}
				slog.Error("请求处理异常")
				response.Fail(c, 5000, "")
			}
		}()
		c.Next()
	}, options.Auth.EntryGuard())
	// API和保留路径先认证，再检查来源；登录及面板别名允许匿名。
	router.Use(func(c *gin.Context) {
		p := c.Request.URL.Path
		protected := p == "/api" || strings.HasPrefix(p, "/api/") || p == "/ws" || strings.HasPrefix(p, "/ws/") || p == "/plugin" || strings.HasPrefix(p, "/plugin/")
		if protected && p != "/api/auth/login" && p != "/api/auth/branding" {
			options.Auth.Auth()(c)
			if c.IsAborted() {
				return
			}
		}
	}, options.Auth.SourceGuard())
	options.Auth.Routes(router.Group(""))
	protected := router.Group("")
	if options.Tasks != nil {
		options.Tasks.Routes(protected, options.Auth)
	}
	if err := router.SetTrustedProxies(nil); err != nil {
		return nil, err
	}
	router.NoRoute(func(c *gin.Context) {
		requestPath := c.Request.URL.Path
		for _, prefix := range []string{"/api", "/ws", "/plugin", "/plugin-frame"} {
			if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
				c.Status(http.StatusNotFound)
				return
			}
		}
		if c.Request.Method != http.MethodGet {
			c.Status(http.StatusNotFound)
			return
		}
		name := strings.TrimPrefix(requestPath, "/")
		if name != "" && name != "plugin-frame.html" && fs.ValidPath(name) {
			if file, err := fs.Stat(files, name); err == nil && !file.IsDir() {
				if strings.HasPrefix(name, "assets/") || strings.HasPrefix(name, "shared/v1/") {
					c.Writer.Header().Add("Vary", "Origin")
					if auth.PluginFrameAssetRequest(c) {
						c.Header("Access-Control-Allow-Origin", "null")
					}
				}
				if name == "assets/plugin-frame.js" {
					c.Header("Cache-Control", "no-cache")
				}
				c.FileFromFS(name, http.FS(files))
				return
			}
		}
		// 缺失资源和非规范路径不得回退为 HTML。
		if strings.Contains(requestPath, "..") || path.Ext(requestPath) != "" || strings.HasPrefix(requestPath, "/assets/") {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
	return &Server{Engine: router, protected: protected, auth: options.Auth}, nil
}
