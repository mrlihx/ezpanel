//go:build linux

package pkgmgr

import (
	"context"
	"errors"
	"ezpanel/internal/executor"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"strconv"
	"strings"
	"time"
)

func (m *Manager) pinNginx(p oldProcess) (int, error) {
	fd, e := unix.PidfdOpen(p.PID, 0)
	if e != nil {
		return -1, e
	}
	current, e := processInfo(p.PID)
	exe, readErr := os.Readlink(fmt.Sprintf("/proc/%d/exe", p.PID))
	if e != nil || readErr != nil || current.Started != p.Started || !strings.HasPrefix(exe, m.installRoot("nginx")+"/") {
		unix.Close(fd)
		return -1, errors.New("Nginx进程身份已改变")
	}
	return fd, nil
}
func (m *Manager) notifyMain(ctx context.Context, pid int) error {
	bin, e := m.LookPath("systemd-notify")
	if e != nil {
		return e
	}
	_, e = m.Run(ctx, 30*time.Second, append(executor.DefaultEnv(), "NOTIFY_SOCKET=/run/systemd/notify"), bin, "--pid="+strconv.Itoa(pid), "MAINPID="+strconv.Itoa(pid))
	return e
}
func (m *Manager) hotNginx(ctx context.Context, prefix, unit string, old oldProcess, log func(string)) (failure error) {
	fd, e := m.pinNginx(old)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	if e = unix.PidfdSendSignal(fd, unix.SIGUSR2, nil, 0); e != nil {
		return e
	}
	log("Nginx已启动新主进程，旧连接继续运行")
	newFD := -1
	defer func() {
		if newFD >= 0 {
			if failure != nil {
				recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = m.notifyMain(recovery, old.PID)
				_ = unix.PidfdSendSignal(newFD, unix.SIGQUIT, nil, 0)
			}
			unix.Close(newFD)
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	newPID := 0
	for {
		b, e := os.ReadFile("/run/nginx.pid")
		if e == nil {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 2 && pid != old.PID {
				exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
				children, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
				if strings.HasPrefix(exe, prefix+"/") && newFD < 0 {
					p, e := processInfo(pid)
					if e != nil {
						return e
					}
					newFD, e = m.pinNginx(p)
					if e != nil {
						return e
					}
					newPID = pid
				}
				if newPID == pid && newFD >= 0 && len(strings.Fields(string(children))) > 0 {
					break
				}
			}
		}
		if time.Now().After(deadline) {
			return errors.New("Nginx新主进程未就绪，旧服务继续运行")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if e = m.notifyMain(ctx, newPID); e != nil {
		return fmt.Errorf("systemd主进程交接失败：%w", e)
	}
	pid, e := m.mainPID(ctx, unit)
	if e != nil || pid != newPID {
		return errors.New("systemd尚未确认新的Nginx主进程，保留旧版本")
	}
	if e = unix.PidfdSendSignal(fd, unix.SIGQUIT, nil, 0); e != nil {
		return e
	}
	log("Nginx主进程已交接，旧工作进程正在平滑退出")
	return nil
}
