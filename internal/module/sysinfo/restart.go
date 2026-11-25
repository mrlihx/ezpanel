package sysinfo

import (
	"os"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func (m *Module) restart(c *gin.Context) {
	var input struct {
		Target string `json:"target"`
	}
	if !response.Bind(c, &input) {
		return
	}
	if input.Target != "server" && input.Target != "panel" {
		response.Fail(c, response.Invalid, "请选择重启服务器或重启面板")
		return
	}
	c.Set("panel.auditName", input.Target)
	ctx := c.Request.Context()
	var running int64
	if m.db == nil || m.db.WithContext(ctx).Model(&store.Task{}).Where("status = ?", "running").Count(&running).Error != nil {
		response.Fail(c, response.System, "无法读取面板任务状态")
		return
	}
	if running > 0 {
		response.Fail(c, response.Conflict, "请等待面板任务完成后再重启")
		return
	}
	bin, err := executor.LookPath("systemctl")
	if err != nil {
		response.Fail(c, response.Invalid, "此环境不支持通过 systemd 重启")
		return
	}
	runner, err := executor.LookPath("systemd-run")
	if err != nil {
		response.Fail(c, response.Invalid, "此环境缺少 systemd-run，无法安排重启")
		return
	}
	args := []string{"reboot"}
	if input.Target == "panel" {
		const unit = "ezpanel.service"
		result, err := executor.Run(ctx, 5*time.Second, bin, "show", "--property=MainPID", "--value", unit)
		if err != nil || strings.TrimSpace(result.Stdout) != strconv.Itoa(os.Getpid()) {
			response.Fail(c, response.Invalid, "当前面板未由 ezpanel.service 启动")
			return
		}
		args = []string{"--no-block", "restart", unit}
	}
	// 由独立的定时单元执行，给当前请求留出返回响应和写审计的时间。
	job := append([]string{"--quiet", "--collect", "--unit=ezpanel-restart", "--on-active=2s", "--timer-property=AccuracySec=1s", bin}, args...)
	if _, err = executor.Run(ctx, 10*time.Second, runner, job...); err != nil {
		response.Fail(c, response.System, "重启请求提交失败，请检查 systemd 服务状态")
		return
	}
	response.OK(c, nil)
}
