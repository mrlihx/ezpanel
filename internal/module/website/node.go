package website

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"ezpanel/internal/auth"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

var nodeScriptName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_-]{0,127}$`)
var nodePackageName = regexp.MustCompile(`^(@[a-z0-9][a-z0-9_.-]*/)?[a-z0-9][a-z0-9_.-]*$`)
var nodePackageVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$`)

type nodeManifest struct {
	PackageManager  string            `json:"packageManager"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func readNodeManifest(dir string) (nodeManifest, bool, error) {
	var manifest nodeManifest
	root, err := os.OpenRoot(dir)
	if err != nil {
		return manifest, false, err
	}
	defer root.Close()
	info, err := root.Stat("package.json")
	if os.IsNotExist(err) {
		return manifest, false, nil
	}
	if err != nil {
		return manifest, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return manifest, false, errors.New("package.json 无效或超过 1 MiB")
	}
	f, err := root.Open("package.json")
	if err != nil {
		return manifest, false, err
	}
	defer f.Close()
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&manifest); err != nil {
		return manifest, false, errors.New("package.json 格式无效")
	}
	if name, _, ok := strings.Cut(manifest.PackageManager, "@"); ok && (name == "npm" || name == "pnpm" || name == "yarn") {
		manifest.PackageManager = name
	} else if _, err := root.Stat("pnpm-lock.yaml"); err == nil {
		manifest.PackageManager = "pnpm"
	} else if _, err := root.Stat("yarn.lock"); err == nil {
		manifest.PackageManager = "yarn"
	} else {
		manifest.PackageManager = "npm"
	}
	return manifest, true, nil
}

func (in *input) nodeLaunch(old store.Website) error {
	if in.RuntimeLaunch == "" {
		in.RuntimeLaunch = old.RuntimeLaunch
		if in.RuntimeLaunch == "" {
			in.RuntimeLaunch = "command"
		}
	}
	if in.RuntimePackageManager == "" {
		in.RuntimePackageManager = "npm"
	}
	if in.RuntimePackageManager != "npm" && in.RuntimePackageManager != "pnpm" && in.RuntimePackageManager != "yarn" {
		return errors.New("请选择 npm、pnpm 或 Yarn")
	}
	setup, tool := pkgmgr.NodePackageTool(in.RuntimePackageManager)
	install := tool + " install"
	if in.RuntimePackageManager == "npm" {
		install = "if [ -f package-lock.json ] || [ -f npm-shrinkwrap.json ]; then npm ci; else npm install; fi"
	}
	dependencies := setup + `if [ -f package.json ] && [ ! -d node_modules ] && [ ! -f .pnp.cjs ] && [ ! -f .pnp.js ]; then ` + install + ` || exit; fi; `
	// npm 的分支需在同一条件块内传播安装失败。
	if in.RuntimePackageManager == "npm" {
		dependencies = `if [ -f package.json ] && [ ! -d node_modules ]; then (` + install + `) || exit; fi; `
	}
	switch in.RuntimeLaunch {
	case "command":
		in.RuntimeFile, in.RuntimeScript = "", ""
	case "file":
		if in.RuntimeFile == "" {
			return errors.New("请选择 Node.js 入口文件")
		}
		in.RuntimeScript = ""
		launcher := "node"
		if in.RuntimePackageManager == "yarn" {
			launcher = tool + " node"
		}
		in.RuntimeCommand = dependencies + `exec ` + launcher + ` "$PROJECT_FILE"`
	case "script":
		if !nodeScriptName.MatchString(in.RuntimeScript) {
			return errors.New("请选择有效的 package.json 启动脚本")
		}
		in.RuntimeFile = ""
		in.RuntimeCommand = dependencies + "exec " + tool + " run '" + in.RuntimeScript + "'"
	default:
		return errors.New("Node.js 启动方式无效")
	}
	return nil
}

func (m *Module) nodePackages(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.ProjectType != "nodejs" || row.RuntimeEnvironment == "" {
		response.Fail(c, response.Invalid, "请先为 Node.js 项目选择运行环境")
		return
	}
	manifest, exists, err := readNodeManifest(row.Root)
	if err != nil {
		failure(c, err)
		return
	}
	scripts := []string{}
	for script := range manifest.Scripts {
		if nodeScriptName.MatchString(script) {
			scripts = append(scripts, script)
		}
	}
	slices.Sort(scripts)
	response.OK(c, gin.H{"exists": exists, "packageManager": manifest.PackageManager, "scripts": scripts, "dependencies": manifest.Dependencies, "devDependencies": manifest.DevDependencies})
}

func (m *Module) changeNodePackage(c *gin.Context) {
	var in struct {
		Action  string `json:"action"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if !response.Bind(c, &in) {
		return
	}
	in.Name, in.Version = strings.TrimSpace(in.Name), strings.TrimSpace(in.Version)
	switch in.Action {
	case "sync":
		if in.Name != "" || in.Version != "" {
			response.Fail(c, response.Invalid, "安装项目依赖时无需填写包名")
			return
		}
	case "install", "update", "uninstall":
		if !nodePackageName.MatchString(in.Name) || len(in.Name) > 214 ||
			(in.Version != "" && (in.Action != "install" || !nodePackageVersion.MatchString(in.Version))) {
			response.Fail(c, response.Invalid, "Node.js 依赖名称或版本无效")
			return
		}
	default:
		response.Fail(c, response.Invalid, "操作无效")
		return
	}
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.ProjectType != "nodejs" || row.RuntimeEnvironment == "" {
		response.Fail(c, response.Invalid, "请先为 Node.js 项目选择运行环境")
		return
	}
	spec := in.Name
	if in.Version != "" {
		spec += "@" + in.Version
	} else if in.Action == "update" {
		spec += "@latest"
	}
	id, err := m.tasks.Submit(task.Meta{Module: "website", Action: "node-" + in.Action, Target: row.Name + " " + spec, ResourceID: row.ID, LockKey: "website:" + row.ID, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		var current store.Website
		if err := m.db.First(&current, "id = ?", row.ID).Error; err != nil {
			return nil, err
		}
		manifest, exists, err := readNodeManifest(current.Root)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, errors.New("请先上传项目的 package.json")
		}
		dev := false
		if in.Action == "update" {
			_, production := manifest.Dependencies[in.Name]
			_, development := manifest.DevDependencies[in.Name]
			if !production && !development {
				return nil, errors.New("该模块未在项目依赖中声明")
			}
			dev = !production && development
		}
		manager := current.RuntimePackageManager
		if manager == "" {
			manager = manifest.PackageManager
		}
		runtime, err := m.packages.RuntimeEnvironment("nodejs", current.RuntimeEnvironment)
		if err != nil {
			return nil, err
		}
		return nil, m.packages.ChangeNodeProjectPackage(ctx, row.ID, current.Root, manager, in.Action, spec, dev, runtime, current.RuntimeRegistry, log.Line)
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
