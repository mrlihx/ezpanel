package software

import (
	"context"
	"errors"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"fmt"
	"github.com/gin-gonic/gin"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *Module) guardUnit(ctx context.Context, id string) (string, error) {
	state, err := m.packages.Status(ctx, id)
	if err != nil {
		return "", err
	}
	if !state.Installed || !state.Manageable || state.Unit == "" {
		return "", pkgmgr.ErrUnsupported
	}
	return state.Unit, nil
}

func (m *Module) applicationGuard(c *gin.Context) {
	ctx := c.Request.Context()
	unit, err := m.guardUnit(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	bin, err := m.packages.LookPath("systemctl")
	if err != nil {
		fail(c, err)
		return
	}
	result, err := m.packages.Run(ctx, 15*time.Second, nil, bin, "show", unit, "--property=Restart", "--property=RestartUSec")
	if err != nil {
		fail(c, err)
		return
	}
	values := map[string]string{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	if values["Restart"] == "" {
		response.Fail(c, response.System, "无法读取 systemd 守护策略")
		return
	}
	restartSeconds := 5
	if delay, err := time.ParseDuration(strings.ReplaceAll(strings.ReplaceAll(values["RestartUSec"], "min", "m"), " ", "")); err == nil {
		restartSeconds = min(300, max(1, int(delay.Seconds())))
	}
	response.OK(c, gin.H{"enabled": values["Restart"] != "no", "policy": values["Restart"], "delay": values["RestartUSec"], "restartSeconds": restartSeconds, "path": "/etc/systemd/system/" + unit + ".d/ezpanel-guard.conf"})
}

func (m *Module) saveApplicationGuard(c *gin.Context) {
	var in struct {
		Enabled        bool `json:"enabled"`
		RestartSeconds int  `json:"restartSeconds"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.RestartSeconds < 1 || in.RestartSeconds > 300 {
		response.Fail(c, response.Invalid, "重启间隔须为 1–300 秒")
		return
	}
	err := m.packages.ApplicationChange(c.Param("id"), func() error {
		ctx := c.Request.Context()
		unit, err := m.guardUnit(ctx, c.Param("id"))
		if err != nil {
			return err
		}
		bin, err := m.packages.LookPath("systemctl")
		if err != nil {
			return err
		}
		path := "/etc/systemd/system/" + unit + ".d/ezpanel-guard.conf"
		previous, err := os.ReadFile(path)
		existed := err == nil
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		policy := "no"
		if in.Enabled {
			policy = "always"
		}
		// 只写面板自有 drop-in，不改发行版 unit，也不会自动启动或重启服务。
		file, err := os.CreateTemp(filepath.Dir(path), ".ezpanel-guard-")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		_, err = fmt.Fprintf(file, "[Service]\nRestart=%s\nRestartSec=%ds\n", policy, in.RestartSeconds)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err = os.Chmod(file.Name(), 0644); err != nil {
			return err
		}
		if err = os.Rename(file.Name(), path); err != nil {
			return err
		}
		if _, err = m.packages.Run(ctx, 15*time.Second, nil, bin, "daemon-reload"); err != nil {
			var restoreErr error
			if existed {
				restoreErr = os.WriteFile(path, previous, 0644)
			} else {
				restoreErr = os.Remove(path)
			}
			_, reloadErr := m.packages.Run(context.WithoutCancel(ctx), 15*time.Second, nil, bin, "daemon-reload")
			return errors.Join(errors.New("systemd 重载失败，已尝试恢复原守护设置"), restoreErr, reloadErr)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pkgmgr.ErrBusy) || errors.Is(err, pkgmgr.ErrUnsupported) {
			fail(c, err)
		} else {
			response.Fail(c, response.System, response.Summary(err.Error()))
		}
		return
	}
	response.OK(c, gin.H{"saved": true})
}
