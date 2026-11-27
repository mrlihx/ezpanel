package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/hardening"
)

func recoverHardening(installDir string) error {
	// 先停止面板，避免运行中的管理器把恢复结果覆盖回旧配置。
	running := false
	if runtime.GOOS == "linux" {
		result, _ := executor.Run(context.Background(), 10*time.Second, "systemctl", "is-active", panelUnit)
		running = strings.TrimSpace(result.Stdout) == "active"
		if running {
			result, err := executor.Run(context.Background(), 10*time.Second, "systemctl", "show", "--property=WorkingDirectory", "--value", panelUnit)
			if err != nil {
				return err
			}
			if filepath.Clean(strings.TrimSpace(result.Stdout)) != filepath.Clean(installDir) {
				return fmt.Errorf("运行中的面板安装目录与 %s 不同，请先停止对应面板再恢复", installDir)
			}
			if _, err = executor.Run(context.Background(), 45*time.Second, "systemctl", "stop", panelUnit); err != nil {
				return err
			}
		}
	}
	manager, err := hardening.New(filepath.Join(installDir, "data", "hardening"))
	if err == nil {
		err = manager.Recover()
	}
	if err != nil {
		return fmt.Errorf("恢复未完成；请检查文件保护状态后手动启动面板：%w", err)
	}
	fmt.Println("系统加固已关闭，已解除本功能添加的保护，原有文件属性与规则已保留。")
	if running {
		if _, err = executor.Run(context.Background(), 45*time.Second, "systemctl", "start", panelUnit); err != nil {
			return err
		}
		fmt.Println("面板服务已恢复运行。")
	}
	return nil
}
