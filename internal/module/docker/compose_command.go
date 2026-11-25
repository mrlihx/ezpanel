package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/task"
)

const composeConfigTimeout = 15 * time.Second
const composeActionTimeout = 30 * time.Minute

type composeCLI struct {
	binary string
	env    []string
	v2     bool
}

func detectComposeCLI(ctx context.Context) (composeCLI, error) {
	env := append(executor.DefaultEnv(), "DOCKER_HOST=unix:///var/run/docker.sock", "HOME=/root")
	// 镜像加速由本机 Docker daemon 处理，命令继承其代理配置。
	var daemon struct {
		Proxies struct {
			HTTP  string `json:"http-proxy"`
			HTTPS string `json:"https-proxy"`
			No    string `json:"no-proxy"`
		} `json:"proxies"`
	}
	if data, err := os.ReadFile("/etc/docker/daemon.json"); err == nil {
		_ = json.Unmarshal(data, &daemon)
	}
	for _, proxy := range []struct{ name, value string }{{"HTTP_PROXY", daemon.Proxies.HTTP}, {"HTTPS_PROXY", daemon.Proxies.HTTPS}, {"NO_PROXY", daemon.Proxies.No}} {
		value := proxy.value
		if value == "" {
			value = os.Getenv(proxy.name)
		}
		if value == "" {
			value = os.Getenv(strings.ToLower(proxy.name))
		}
		if value != "" {
			env = append(env, proxy.name+"="+value, strings.ToLower(proxy.name)+"="+value)
		}
	}
	if binary, err := executor.LookPath("docker"); err == nil {
		if _, err := executor.RunEnv(ctx, 5*time.Second, env, binary, "compose", "version"); err == nil {
			return composeCLI{binary: binary, env: env, v2: true}, nil
		}
	}
	if binary, err := executor.LookPath("docker-compose"); err == nil {
		if _, err := executor.RunEnv(ctx, 5*time.Second, env, binary, "version"); err == nil {
			return composeCLI{binary: binary, env: env}, nil
		}
	}
	return composeCLI{}, errors.New("Docker Compose 不可用，请安装 Docker Compose 插件")
}

func (cli composeCLI) args(project composeProjectView, command ...string) []string {
	var args []string
	if cli.v2 {
		args = []string{"compose", "--ansi", "never"}
	} else {
		args = []string{"--no-ansi"}
	}
	args = append(args, "--project-name", project.Name, "--project-directory", project.WorkDir)
	for _, file := range project.ComposeFiles {
		args = append(args, "--file", file)
	}
	if _, err := os.Stat(project.EnvFile); !errors.Is(err, os.ErrNotExist) {
		args = append(args, "--env-file", project.EnvFile)
	}
	return append(args, command...)
}

var composeYAMLLine = regexp.MustCompile(`(?i)\bline\s+(\d+)\b`)

func (cli composeCLI) check(ctx context.Context, project composeProjectView, cwd, newEnvFile string) error {
	ctx, cancel := context.WithTimeout(ctx, composeConfigTimeout)
	defer cancel()
	args := []string{"config", "--quiet"}
	out, err := executor.RunFixed(ctx, composeConfigTimeout, cwd, cli.env, cli.binary, cli.args(project, args...)...)
	if cli.v2 && err != nil && newEnvFile != "" && strings.Contains(out.Stderr, "env file "+newEnvFile+" not found") {
		// 服务引用的新 .env 尚未替换到目标位置，Compose 已用暂存文件完成全局插值。
		args = append(args, "--no-env-resolution")
		out, err = executor.RunFixed(ctx, composeConfigTimeout, cwd, cli.env, cli.binary, cli.args(project, args...)...)
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	// Compose 的原始诊断可能带入插值后的配置值。
	message := strings.ToLower(out.Stderr)
	switch {
	case strings.Contains(message, "yaml:") || strings.Contains(message, "while parsing"):
		if line := composeYAMLLine.FindStringSubmatch(message); len(line) > 0 {
			return fmt.Errorf("Compose YAML 第 %s 行语法无效", line[1])
		}
		return errors.New("Compose YAML 语法无效")
	case strings.Contains(message, "dotenv") || strings.Contains(message, "env file") || strings.Contains(message, "env_file"):
		return errors.New("环境变量文件无效或不存在，请检查 .env 或 env_file 配置")
	case strings.Contains(message, "required variable") || strings.Contains(message, "missing mandatory value"):
		return errors.New("Compose 配置缺少必填环境变量")
	case strings.Contains(message, "undefined"):
		return errors.New("Compose 配置引用了未定义的服务、网络、数据卷或其他资源")
	case strings.Contains(message, "additional property") || strings.Contains(message, "extra fields"):
		return errors.New("Compose 配置包含当前版本不支持的字段")
	default:
		return errors.New("Compose 配置校验失败，请检查服务、文件路径与字段类型")
	}
}

func (cli composeCLI) run(ctx context.Context, project composeProjectView, log *task.Log, command ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if file, err := os.Open(project.EnvFile); err == nil {
		env, err := io.ReadAll(io.LimitReader(file, composeTextMax+1))
		file.Close()
		if err == nil && len(env) <= composeTextMax {
			log.Secrets(composeEnvValues(string(env))...)
		}
	}
	for _, entry := range cli.env {
		key, value, _ := strings.Cut(entry, "=")
		if key == "HTTP_PROXY" || key == "HTTPS_PROXY" {
			log.Secrets(value)
		}
	}
	log.Line("执行 Compose " + strings.Join(command, " "))
	result, err := executor.StreamFixed(ctx, composeActionTimeout, project.WorkDir, cli.env, log.Line, cli.binary, cli.args(project, command...)...)
	log.ExitCode(result.ExitCode)
	if err != nil {
		return fmt.Errorf("Compose %s 执行失败：%w", command[0], err)
	}
	return nil
}

func (cli composeCLI) up(ctx context.Context, project composeProjectView, log *task.Log, pull, removeOrphans, forceRecreate bool) error {
	args := []string{"up", "--detach"}
	if pull {
		if cli.v2 {
			args = append(args, "--pull", "always")
		} else if err := cli.run(ctx, project, log, "pull"); err != nil {
			return err
		}
	}
	if removeOrphans {
		args = append(args, "--remove-orphans")
	}
	if forceRecreate {
		args = append(args, "--force-recreate")
	}
	return cli.run(ctx, project, log, args...)
}
