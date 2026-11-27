package auth

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestHardeningAuditDescription(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, item := range []struct{ method, path, action, expected string }{
		{"PUT", "/api/security/hardening", "enable", "开启系统加固"},
		{"PUT", "/api/security/hardening", "disable", "关闭系统加固"},
		{"POST", "/api/security/hardening/rules/:id/action", "pause", "临时解除加固保护[配置文件]"},
		{"DELETE", "/api/security/hardening/rules/:id", "DELETE", "删除加固规则[配置文件]"},
	} {
		router := gin.New()
		router.Handle(item.method, item.path, func(c *gin.Context) {
			_, target, skip := classifyAudit(c, "security", item.method, item.action, "配置文件")
			if skip || target != item.expected {
				t.Errorf("%s: target=%q skip=%v", item.action, target, skip)
			}
		})
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(item.method, item.path, nil))
	}
}
