package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const projectVenv = "/opt/ezpanel-venv"
const activateProjectVenv = `unset PYTHONHOME; export VIRTUAL_ENV=` + projectVenv + `; if [ ! -f "$VIRTUAL_ENV/.ready" ]; then python -I -m venv "$VIRTUAL_ENV" && touch "$VIRTUAL_ENV/.ready" || exit; fi; export PATH="$VIRTUAL_ENV/bin:$PATH"; `

type PythonPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (m *Manager) pythonProjectPath(id, environment string) string {
	return filepath.Join(m.cfg.DataDir(), "python-projects", id, environment)
}

func (m *Manager) pythonProjectMount(id, environment string) (string, error) {
	dir := m.pythonProjectPath(id, environment)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return "type=bind,src=" + dir + ",dst=" + projectVenv, nil
}

func (m *Manager) ProjectUsesPythonEnvironment(ctx context.Context, id string) (bool, error) {
	info, _, err := m.inspectContainer(ctx, "ezpanel-site-"+id)
	return info.Config.Labels["ezpanel.python-venv"] == "true", err
}

func (m *Manager) RemovePythonProjectEnvironment(id string) error {
	return os.RemoveAll(m.pythonProjectPath(id, ""))
}

func (m *Manager) PythonProjectPackages(ctx context.Context, id string, runtime RuntimeEnvironment) ([]PythonPackage, error) {
	packages := []PythonPackage{}
	dir := m.pythonProjectPath(id, runtime.ID)
	if _, err := os.Stat(filepath.Join(dir, ".ready")); os.IsNotExist(err) {
		return packages, nil
	} else if err != nil {
		return nil, err
	}
	result, err := m.docker(ctx, 30*time.Second, "run", "--rm", "--pull=never", "--network=none",
		"--mount", "type=bind,src="+dir+",dst="+projectVenv+",readonly", runtime.Image,
		projectVenv+"/bin/python", "-m", "pip", "--disable-pip-version-check", "list", "--format=json")
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(result.Stdout), &packages)
	return packages, err
}

func (m *Manager) ChangePythonProjectPackage(ctx context.Context, id string, runtime RuntimeEnvironment, action, name, registry string, onLine func(string)) error {
	mount, err := m.pythonProjectMount(id, runtime.ID)
	if err != nil {
		return err
	}
	docker, err := m.LookPath("docker")
	if err != nil {
		return err
	}
	container := "ezpanel-python-packages-" + id
	args := []string{"run", "--rm", "--name", container, "--pull=never", "--security-opt", "no-new-privileges=true", "--cap-drop", "ALL",
		"--mount", mount}
	args = appendProjectRegistry(args, "python", registry)
	args = append(args, runtime.Image, "sh", "-c", activateProjectVenv+`exec python -m pip --disable-pip-version-check "$@"`, "pip", action)
	if action == "uninstall" {
		args = append(args, "--yes")
	}
	args = append(args, "--", name)
	_, err = m.Stream(ctx, 10*time.Minute, nil, onLine, docker, args...)
	if err != nil {
		_, _ = m.docker(context.WithoutCancel(ctx), 15*time.Second, "rm", "-f", container)
		return errors.New("Python 依赖操作失败，请查看任务日志")
	}
	return nil
}
