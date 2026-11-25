package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

var menuActions = []struct {
	command, label string
	option, prompt string
}{
	{command: "info", label: "查看面板信息"},
	{command: "status", label: "查看面板状态"},
	{command: "start", label: "启动面板"},
	{command: "stop", label: "停止面板"},
	{command: "restart", label: "重启面板"},
	{command: "logs", label: "查看面板日志"},
	{command: "reset-port", label: "重置监听端口", option: "--port", prompt: "请输入新端口（1–65535）"},
	{command: "reset-entry", label: "重置安全入口", option: "--entry", prompt: "请输入新入口（至少 5 位字母或数字，留空随机生成）"},
	{command: "reset-username", label: "重置管理员用户名", option: "--username", prompt: "请输入新用户名"},
	{command: "reset-password", label: "重置管理员密码"},
	{command: "unlock-login", label: "解除登录锁定", option: "--ip", prompt: "请输入需要解锁的来源 IP"},
	{command: "ready", label: "检查面板就绪状态"},
	{command: "upgrade-check", label: "检查升级前的活动任务"},
	{command: "version", label: "查看版本"},
	{command: "help", label: "查看命令帮助"},
	{command: "external-access", label: "开启或关闭面板外网直连", option: "--mode", prompt: "输入 on 开启，或 off 关闭"},
}

func runMenu() error {
	if !terminal() {
		printHelp()
		return nil
	}
	input := bufio.NewReader(os.Stdin)
	readLine := func(prompt string) (string, error) {
		fmt.Print(prompt, "：")
		text, err := input.ReadString('\n')
		return strings.TrimSpace(text), err
	}
	showMenu := func() {
		fmt.Println("\nEzPanel 运维菜单")
		for i, action := range menuActions {
			fmt.Printf("  %2d. %s\n", i+1, action.label)
		}
		fmt.Println("   0. 退出")
	}
	showMenu()
	for {
		choice, err := readLine("\n请选择操作（? 显示菜单）")
		if errors.Is(err, io.EOF) || choice == "0" {
			return nil
		}
		if err != nil {
			return err
		}
		if choice == "?" {
			showMenu()
			continue
		}
		index, err := strconv.Atoi(choice)
		if err != nil || index < 1 || index > len(menuActions) {
			fmt.Println("请输入菜单中的数字。")
			continue
		}
		action := menuActions[index-1]
		args := []string{action.command}
		if action.option != "" {
			value, err := readLine(action.prompt)
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			args = append(args, action.option+"="+value)
		}
		// 菜单和直接调用共用命令校验与执行流程。
		if err := run(args); err != nil {
			fmt.Fprintln(os.Stderr, "操作失败：", err)
			continue
		}
		switch action.command {
		case "start", "stop", "restart":
			fmt.Println(action.label + "成功。")
		}
	}
}
