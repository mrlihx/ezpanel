package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

func (m *Module) loadImage(c *gin.Context) {
	var in struct {
		Path string `json:"path"`
	}
	if !response.Bind(c, &in) {
		return
	}
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		fail(c, err)
		return
	}
	path, err := policy.Check(in.Path, false)
	if err != nil {
		response.Fail(c, response.Invalid, "请输入可读取的服务器镜像归档绝对路径")
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		response.Fail(c, response.Invalid, "镜像归档不存在或不是普通文件")
		return
	}
	binary, err := executor.LookPath("docker")
	if err != nil {
		response.Fail(c, response.Invalid, "缺少Docker命令行，请在应用商店安装完整Docker环境")
		return
	}
	id, err := m.tasks.Submit(task.Meta{Module: "docker", Action: "load", Target: filepath.Base(path), ResourceID: path, LockKey: "docker:image-load", Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		log.Line("开始从服务器归档导入镜像，关闭窗口后任务继续")
		result, err := executor.Stream(ctx, 2*time.Hour, append(executor.DefaultEnv(), "DOCKER_HOST=unix:///var/run/docker.sock"), log.Line, binary, "load", "--input", path)
		log.ExitCode(result.ExitCode)
		return nil, err
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) buildImage(c *gin.Context) {
	var in struct {
		Tag         string `json:"tag"`
		Dockerfile  string `json:"dockerfile"`
		FilePath    string `json:"filePath"`
		ContextPath string `json:"contextPath"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !validImage(in.Tag) || strings.HasPrefix(in.Tag, "-") || strings.Contains(in.Tag, "@") || (in.Dockerfile == "") == (in.FilePath == "") {
		response.Fail(c, response.Invalid, "请输入有效镜像名称，并选择一种Dockerfile来源")
		return
	}
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		fail(c, err)
		return
	}
	if in.FilePath != "" {
		path, err := policy.Check(in.FilePath, false)
		if err != nil {
			response.Fail(c, response.Invalid, "Dockerfile路径不可读取")
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			response.Fail(c, response.Invalid, "Dockerfile必须是1MiB内的普通文件")
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fail(c, err)
			return
		}
		in.Dockerfile = string(data)
		if in.ContextPath == "" {
			in.ContextPath = filepath.Dir(path)
		}
	}
	if len(in.Dockerfile) > 1<<20 || !utf8.ValidString(in.Dockerfile) || strings.ContainsRune(in.Dockerfile, 0) {
		response.Fail(c, response.Invalid, "Dockerfile须为1MiB内的UTF-8文本")
		return
	}
	if in.ContextPath != "" {
		path, err := policy.Check(in.ContextPath, false)
		if err != nil {
			response.Fail(c, response.Invalid, "构建目录不可读取")
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			response.Fail(c, response.Invalid, "构建目录不存在")
			return
		}
		if err = policy.CheckTree(path); err != nil {
			response.Fail(c, response.Invalid, "构建目录包含系统或面板受保护路径")
			return
		}
		in.ContextPath = path
	}
	binary, err := executor.LookPath("docker")
	if err != nil {
		response.Fail(c, response.Invalid, "缺少Docker命令行，请在应用商店安装完整Docker环境")
		return
	}
	id, err := m.tasks.Submit(task.Meta{Module: "docker", Action: "build", Target: in.Tag, ResourceID: in.Tag, LockKey: "docker:image:" + in.Tag, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		root := filepath.Join(m.cfg.Layout().TempDir(), "docker-build")
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
		work, err := os.MkdirTemp(root, "image-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(work)
		file := filepath.Join(work, "Dockerfile")
		if err = os.WriteFile(file, []byte(in.Dockerfile), 0600); err != nil {
			return nil, err
		}
		contextPath := in.ContextPath
		if contextPath == "" {
			contextPath = work
		}
		// 交由Docker处理构建上下文及.dockerignore，不自行打包或执行Dockerfile指令。
		log.Line("开始构建 " + in.Tag + "；关闭窗口后任务继续")
		result, err := executor.Stream(ctx, 2*time.Hour, append(executor.DefaultEnv(), "DOCKER_HOST=unix:///var/run/docker.sock"), log.Line, binary, "build", "--tag", in.Tag, "--file", file, contextPath)
		log.ExitCode(result.ExitCode)
		if err != nil {
			return nil, errors.New("镜像构建失败，请查看任务日志；若提示缺少buildx，请安装Docker Buildx组件")
		}
		return gin.H{"image": in.Tag}, nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
