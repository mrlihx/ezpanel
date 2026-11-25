package cron

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"ezpanel/internal/pathsafe"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// 解析语法树再检查命令，避免引号、绝对程序路径和命令替换绕过简单文本匹配。
// 这是敏感操作的静态限制，不是对任意管理员程序的安全沙箱。
func (m *Module) checkShell(script string) error {
	return m.checkShellAt(script, m.cfg.InstallDir, 0)
}
func (m *Module) checkShellAt(script, cwd string, depth int) error {
	if depth > 8 {
		return errors.New("Shell嵌套过深，无法检查敏感操作")
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
	if err != nil {
		return errors.New("Shell语法无效，请检查脚本")
	}
	var denied error
	download, externalScript := false, false
	syntax.Walk(file, func(node syntax.Node) bool {
		if denied != nil {
			return false
		}
		fail := func(reason string) { denied = fmt.Errorf("第%d行：%s", node.Pos().Line(), reason) }
		switch n := node.(type) {
		case *syntax.Redirect:
			switch n.Op {
			case syntax.RdrOut, syntax.AppOut, syntax.RdrInOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll:
				path, ok := shellLiteral(n.Word)
				if !ok {
					fail("写入目标需要使用明确路径，不能动态拼接受保护文件")
					break
				}
				if err := m.shellWritePath(path, cwd, false); err != nil {
					fail(err.Error())
				}
			}
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				return true
			}
			args := make([]string, len(n.Args))
			known := make([]bool, len(n.Args))
			for i, word := range n.Args {
				args[i], known[i] = shellLiteral(word)
			}
			// sudo/env等只改变执行环境，必须检查真正执行的命令。
			for len(args) > 0 {
				if !known[0] || args[0] == "" {
					fail("不允许动态生成命令名，请直接填写要执行的命令")
					return false
				}
				command := filepath.Base(args[0])
				skip := 0
				switch command {
				case "sudo", "env", "command", "builtin", "exec", "nohup", "nice", "timeout", "busybox", "toybox":
					skip = 1
				}
				if skip == 0 {
					break
				}
				for skip < len(args) {
					if !known[skip] {
						fail("命令包装器参数不能动态指定")
						return false
					}
					arg := args[skip]
					if strings.HasPrefix(arg, "-") {
						skip++
						if command == "env" && (arg == "-S" || strings.HasPrefix(arg, "--split-string")) || command == "sudo" && (arg == "-s" || arg == "-i") {
							fail("请直接填写命令，不使用拆分字符串或交互Shell包装器")
							return false
						}
						if command == "sudo" && (arg == "-u" || arg == "-g" || arg == "--user" || arg == "--group") || command == "nice" && arg == "-n" || command == "timeout" && (arg == "-s" || arg == "-k" || arg == "--signal" || arg == "--kill-after") || command == "exec" && arg == "-a" || command == "env" && (arg == "-u" || arg == "--unset") {
							skip++
						}
					} else if command == "env" && strings.Contains(arg, "=") {
						skip++
					} else {
						break
					}
				}
				if command == "timeout" {
					skip++
				}
				if skip >= len(args) {
					return true
				}
				args, known = args[skip:], known[skip:]
			}
			command := filepath.Base(args[0])
			rest := args[1:]
			switch command {
			case "xargs", "reboot", "shutdown", "poweroff", "halt", "init", "telinit", "kexec", "dd", "fdisk", "sfdisk", "cfdisk", "gdisk", "sgdisk", "parted", "wipefs", "mkswap", "cryptsetup", "passwd", "chpasswd", "usermod", "useradd", "userdel", "groupmod", "groupadd", "groupdel", "chage", "vipw", "vigr", "visudo", "eval":
				fail("禁止关机重启、磁盘破坏、修改系统认证及动态eval操作")
				return false
			}
			if command == "find" {
				for _, arg := range rest {
					if arg == "-exec" || arg == "-execdir" || arg == "-ok" || arg == "-okdir" || arg == "-delete" {
						fail("find批量执行或删除无法逐项检查，请使用明确的文件路径")
						return false
					}
				}
			}
			if command == "mkfs" || strings.HasPrefix(command, "mkfs.") {
				fail("禁止磁盘格式化命令")
				return false
			}
			if command == "systemctl" {
				for _, arg := range rest {
					switch arg {
					case "reboot", "poweroff", "halt", "kexec", "rescue", "emergency", "isolate":
						fail("禁止切换或关闭系统运行状态")
						return false
					}
				}
			}
			if command == "cd" {
				if len(rest) != 1 || !known[1] {
					cwd = ""
				} else if filepath.IsAbs(rest[0]) {
					cwd = filepath.Clean(rest[0])
				} else if cwd != "" {
					cwd = filepath.Join(cwd, rest[0])
				}
			}
			if command == "curl" || command == "wget" {
				download = true
			}
			if command == "bash" || command == "sh" || command == "dash" || command == "zsh" || command == "ksh" {
				externalScript = true
				for i, arg := range rest {
					if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(strings.TrimLeft(arg, "-"), "c") {
						if i+2 >= len(args) || !known[i+2] {
							fail("Shell内联脚本必须是可检查的固定文本")
							return false
						}
						if err := m.checkShellAt(args[i+2], cwd, depth+1); err != nil {
							fail(err.Error())
							return false
						}
						break
					}
				}

			}
			if strings.Contains(args[0], "/") && !strings.HasPrefix(args[0], "/usr/bin/") && !strings.HasPrefix(args[0], "/bin/") && !strings.HasPrefix(args[0], "/sbin/") && !strings.HasPrefix(args[0], "/usr/sbin/") {
				externalScript = true
			}
			if command == "." || command == "source" || strings.HasSuffix(command, ".sh") || strings.HasPrefix(command, "python") || command == "perl" || command == "php" || command == "ruby" || command == "node" {
				externalScript = true
			}
			destructive := command == "rm" || command == "rmdir" || command == "unlink" || command == "shred" || command == "mv"
			writeAll := destructive || command == "tee" || command == "truncate"
			writeLast := command == "cp" || command == "install" || command == "chmod" || command == "chown" || command == "chgrp" || command == "touch" || command == "ln"
			if command == "sed" {
				for _, arg := range rest {
					if strings.HasPrefix(arg, "-i") || strings.HasPrefix(arg, "--in-place") {
						writeLast = true
					}
				}
			}
			for i, arg := range rest {
				outputFile := (command == "curl" && (i > 0 && (rest[i-1] == "-o" || rest[i-1] == "--output"))) || (command == "wget" && (i > 0 && (rest[i-1] == "-O" || rest[i-1] == "--output-document")))
				absoluteMutation := (command == "sed" || command == "chmod" || command == "chown" || command == "chgrp" || command == "touch") && filepath.IsAbs(arg)
				targetDirectory := (command == "cp" || command == "install") && i > 0 && (rest[i-1] == "-t" || rest[i-1] == "--target-directory")
				if (writeAll || writeLast && i == len(rest)-1 || outputFile || absoluteMutation || targetDirectory) && !strings.HasPrefix(arg, "-") {
					if !known[i+1] {
						fail("删除或写入文件需要明确路径，不能使用动态目标")
						return false
					}
					if err := m.shellWritePath(arg, cwd, destructive); err != nil {
						fail(err.Error())
						return false
					}
				}
				if strings.HasPrefix(arg, "--output=") || strings.HasPrefix(arg, "--output-document=") || strings.HasPrefix(arg, "--target-directory=") {
					_, path, _ := strings.Cut(arg, "=")
					if err := m.shellWritePath(path, cwd, false); err != nil {
						fail(err.Error())
						return false
					}
				}
			}
		}
		return denied == nil
	})
	if denied != nil {
		return denied
	}
	if download && externalScript {
		return errors.New("禁止在同一Shell任务中下载并直接执行脚本；请先单独下载并审阅文件")
	}
	return nil
}
func shellLiteral(word *syntax.Word) (string, bool) {
	fixed := true
	syntax.Walk(word, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ProcSubst, *syntax.ArithmExp:
			fixed = false
		}
		return fixed
	})
	if !fixed {
		return "", false
	}
	value, err := expand.Literal(nil, word)
	return value, err == nil
}
func (m *Module) shellWritePath(path, cwd string, destructive bool) error {
	if path == "/dev/null" || path == "/dev/stdout" || path == "/dev/stderr" {
		return nil
	}
	if strings.HasPrefix(path, "~") {
		path = "/root" + strings.TrimPrefix(path, "~")
	}
	// 通配符按其最宽父目录检查，例如 /* 和 /etc/* 都视作系统目录操作。
	if i := strings.IndexAny(path, "*?[{"); i >= 0 {
		path = filepath.Dir(path[:i] + "_")
	}
	if !filepath.IsAbs(path) {
		if cwd == "" {
			return errors.New("当前目录不明确，请使用绝对路径")
		}
		path = filepath.Join(cwd, path)
	}
	real, err := m.policy.Check(path, true)
	if err != nil {
		return errors.New("禁止修改系统根目录或面板目录")
	}
	for _, authFile := range []string{"/etc/passwd", "/etc/shadow", "/etc/group", "/etc/gshadow", "/etc/sudoers", "/etc/sudoers.d", "/etc/pam.d", "/etc/security", "/etc/ssh", "/root/.ssh"} {
		if pathsafe.Contains(authFile, real) || pathsafe.Contains(real, authFile) {
			return errors.New("禁止修改系统认证文件")
		}
	}
	if destructive {
		for _, root := range []string{"/bin", "/sbin", "/boot", "/lib", "/lib64", "/usr", "/etc", "/root", "/proc", "/sys", "/dev"} {
			if pathsafe.Contains(root, real) {
				return errors.New("禁止删除系统目录或其内容")
			}
		}
	}
	return nil
}
