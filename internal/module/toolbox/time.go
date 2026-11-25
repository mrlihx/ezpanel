package toolbox

import (
	"context"
	"ezpanel/internal/response"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type timeStatus struct {
	Zone          string
	Synchronized  bool
	Service       string
	ServiceActive bool
}

func readTimeStatus(ctx context.Context) (timeStatus, error) {
	var status timeStatus
	out, err := command(ctx, "timedatectl", "show", "--property=Timezone", "--property=NTPSynchronized")
	if err != nil {
		return status, err
	}
	for _, line := range strings.Split(out, "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "Timezone":
			status.Zone = value
		case "NTPSynchronized":
			status.Synchronized = value == "yes"
		}
	}
	out, err = command(ctx, "systemctl", "show", "--property=Id", "--property=LoadState", "--property=ActiveState", "chrony.service", "chronyd.service", "systemd-timesyncd.service", "ntp.service", "ntpd.service", "ntpsec.service")
	if err != nil {
		return status, err
	}
	for _, unit := range strings.Split(strings.TrimSpace(out), "\n\n") {
		var id, loadState, activeState string
		for _, line := range strings.Split(unit, "\n") {
			key, value, _ := strings.Cut(line, "=")
			switch key {
			case "Id":
				id = value
			case "LoadState":
				loadState = value
			case "ActiveState":
				activeState = value
			}
		}
		if loadState != "loaded" {
			continue
		}
		if status.Service == "" || activeState == "active" {
			status.Service, status.ServiceActive = id, activeState == "active"
		}
		if status.ServiceActive {
			break
		}
	}
	if status.ServiceActive && (status.Service == "chrony.service" || status.Service == "chronyd.service") {
		_, err := command(ctx, "chronyc", "waitsync", "1", "0.1", "0", "1")
		status.Synchronized = err == nil
	}
	return status, nil
}

func (m *Module) syncTime(c *gin.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx := c.Request.Context()
	status, err := readTimeStatus(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	if status.Service == "" {
		fail(c, fmt.Errorf("未检测到受支持的 NTP 服务，请先安装或配置 chrony、systemd-timesyncd 或 ntpd"))
		return
	}
	if !status.ServiceActive {
		if _, err = command(ctx, "systemctl", "start", status.Service); err != nil {
			fail(c, err)
			return
		}
	}
	synchronized := false
	switch status.Service {
	case "chrony.service", "chronyd.service":
		// 仅让下一次新测量按需跳时，不改配置，也不切换已在用的 NTP 服务。
		if _, err = command(ctx, "chronyc", "makestep", "0.1", "1"); err == nil {
			_, err = command(ctx, "chronyc", "burst", "1/2")
		}
		if err == nil {
			// 选中时间源不等于系统时钟已追平，确认剩余修正小于 100ms。
			_, waitErr := command(ctx, "chronyc", "waitsync", "15", "0.1", "0", "1")
			synchronized = waitErr == nil
		}
	default:
		if status.ServiceActive {
			_, err = command(ctx, "systemctl", "restart", status.Service)
		}
		if err == nil {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			deadline := time.NewTimer(15 * time.Second)
			defer deadline.Stop()
		wait:
			for {
				var out string
				out, err = command(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value")
				if err != nil || strings.TrimSpace(out) == "yes" {
					synchronized = err == nil
					break
				}
				select {
				case <-ctx.Done():
					err = ctx.Err()
					break wait
				case <-deadline.C:
					break wait
				case <-ticker.C:
				}
			}
		}
	}
	if err != nil {
		fail(c, err)
		return
	}
	message := "时间已同步（" + status.Service + "）"
	if !synchronized {
		message = "已向 " + status.Service + " 请求校时，15 秒内尚未确认同步；请稍后刷新，或检查时间源和网络"
	}
	response.OK(c, gin.H{"synchronized": synchronized, "service": status.Service, "message": message})
}
