package pkgmgr

import (
	"context"
	"errors"
	"time"
)

func NodePackageTool(manager string) (setup, tool string) {
	if manager == "npm" {
		return "", "npm"
	}
	return `if ! command -v corepack >/dev/null 2>&1; then npm install --prefix /tmp/ezpanel-corepack corepack@0.34.7 || exit; export PATH="/tmp/ezpanel-corepack/node_modules/.bin:$PATH"; fi; `, "corepack " + manager
}

func (m *Manager) ChangeNodeProjectPackage(ctx context.Context, id, root, manager, action, spec string, dev bool, runtime RuntimeEnvironment, registry string, onLine func(string)) error {
	setup, tool := NodePackageTool(manager)
	verb := "install"
	if action == "uninstall" {
		verb = "remove"
	} else if action != "sync" && manager != "npm" {
		verb = "add"
	}
	docker, err := m.LookPath("docker")
	if err != nil {
		return err
	}
	container := "ezpanel-node-packages-" + id
	args := []string{"run", "--rm", "--name", container, "--pull=never", "--security-opt", "no-new-privileges=true", "--cap-drop", "ALL",
		"--mount", "type=bind,src=" + root + ",dst=/app", "--workdir", "/app"}
	args = appendProjectRegistry(args, "nodejs", registry)
	args = append(args, runtime.Image, "sh", "-c", setup+"exec "+tool+" "+verb+` "$@"`, "packages")
	if dev {
		args = append(args, "-D")
	}
	if spec != "" {
		args = append(args, spec)
	}
	_, err = m.Stream(ctx, 10*time.Minute, nil, onLine, docker, args...)
	if err != nil {
		_, _ = m.docker(context.WithoutCancel(ctx), 15*time.Second, "rm", "-f", container)
		return errors.New("Node.js 依赖操作失败，请查看任务日志")
	}
	return nil
}
