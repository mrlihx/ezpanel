package website

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

var pythonPackageName = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9_.-]*[a-zA-Z0-9])?$`)
var pythonPackageVersion = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+!_-]*$`)
var pythonNameSeparators = regexp.MustCompile(`[-_.]+`)
var pythonRequirementLine = regexp.MustCompile(`^([a-zA-Z0-9](?:[a-zA-Z0-9_.-]*[a-zA-Z0-9])?)(?:[ \t]*\[[^\]]+\])?[ \t]*([<>=!~(@][^;]*)?(?:;[ \t]*(.+))?$`)
var pythonRequirementComment = regexp.MustCompile(`[ \t]+(?:#|--(?:hash|config-settings)\b)`)

type pythonDependency struct {
	Name        string `json:"name"`
	Requirement string `json:"requirement"`
	Version     string `json:"version"`
}

func pythonRequirementsFile(file string) string {
	if file == "" {
		return "requirements.txt"
	}
	return file
}

func pythonNameKey(name string) string {
	return pythonNameSeparators.ReplaceAllString(strings.ToLower(name), "-")
}

func readPythonRequirements(dir, file string) (map[string]pythonDependency, bool, error) {
	const maxSize = 1 << 20
	root, err := os.OpenRoot(dir)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	info, err := root.Stat(file)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSize {
		return nil, false, errors.New("依赖文件必须是 1 MiB 以内的普通文件")
	}
	f, err := root.Open(file)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxSize {
		return nil, false, errors.New("依赖文件超过 1 MiB")
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	rows := map[string]pythonDependency{}
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\\\n", ""), "\n") {
		if index := pythonRequirementComment.FindStringIndex(line); index != nil {
			line = line[:index[0]]
		}
		match := pythonRequirementLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		name, requirement := match[1], strings.TrimSpace(match[2])
		switch {
		case requirement == "":
			requirement = "不限版本"
		case strings.HasPrefix(requirement, "@"):
			// 直接引用可能带有私有仓库凭据，列表仅展示引用类型。
			requirement = "直接引用"
		}
		if match[3] != "" {
			requirement += "; " + match[3]
		}
		key := pythonNameKey(name)
		if row, ok := rows[key]; ok && row.Requirement != requirement {
			requirement = row.Requirement + "\n" + requirement
		}
		rows[key] = pythonDependency{Name: name, Requirement: requirement}
	}
	return rows, true, nil
}

func (m *Module) pythonPackages(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.ProjectType != "python" || row.RuntimeEnvironment == "" {
		response.Fail(c, response.Invalid, "请先为 Python 项目选择运行环境")
		return
	}
	runtime, err := m.packages.RuntimeEnvironment("python", row.RuntimeEnvironment)
	if err != nil {
		failure(c, err)
		return
	}
	file := pythonRequirementsFile(row.RuntimeRequirements)
	dependencies, exists, err := readPythonRequirements(row.Root, file)
	if err != nil {
		response.Fail(c, response.Invalid, "无法读取项目依赖文件，请检查路径和文件内容")
		return
	}
	if dependencies == nil {
		dependencies = map[string]pythonDependency{}
	}
	packages, err := m.packages.PythonProjectPackages(c.Request.Context(), row.ID, runtime)
	if err != nil {
		failure(c, err)
		return
	}
	for _, pkg := range packages {
		key := pythonNameKey(pkg.Name)
		dependency := dependencies[key]
		if dependency.Name == "" {
			dependency.Name = pkg.Name
		}
		dependency.Version = pkg.Version
		dependencies[key] = dependency
	}
	list := make([]pythonDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		list = append(list, dependency)
	}
	slices.SortFunc(list, func(a, b pythonDependency) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	response.OK(c, gin.H{"file": file, "fileExists": exists, "list": list})
}

func (m *Module) savePythonRequirements(c *gin.Context) {
	var in struct {
		File string `json:"file"`
	}
	if !response.Bind(c, &in) {
		return
	}
	file := filepath.Clean(pythonRequirementsFile(strings.TrimSpace(in.File)))
	if !filepath.IsLocal(file) || file == "." || len(file) > 4096 || strings.ContainsRune(file, '\\') || strings.IndexFunc(file, unicode.IsControl) >= 0 {
		response.Fail(c, response.Invalid, "请填写项目目录内的相对文件路径")
		return
	}
	release, err := m.tasks.Acquire("website:" + c.Param("id"))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.ProjectType != "python" {
		response.Fail(c, response.Invalid, "仅 Python 项目支持设置依赖文件")
		return
	}
	if _, _, err := readPythonRequirements(row.Root, file); err != nil {
		response.Fail(c, response.Invalid, "无法读取项目依赖文件，请检查路径和文件内容")
		return
	}
	auditSite(c, "", "依赖文件 "+pythonRequirementsFile(row.RuntimeRequirements)+" → "+file)
	updates := map[string]any{"runtime_requirements": file}
	if file != pythonRequirementsFile(row.RuntimeRequirements) && row.ProxyTarget != "" && (row.RuntimeLaunch == "gunicorn" || row.RuntimeLaunch == "uwsgi") {
		updates["runtime_pending"] = true
	}
	if err := m.db.Model(&row).Updates(updates).Error; err != nil {
		failure(c, errStorage)
		return
	}
	response.OK(c, gin.H{"file": file})
}

func (m *Module) changePythonPackage(c *gin.Context) {
	var in struct {
		Action  string `json:"action"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if !response.Bind(c, &in) {
		return
	}
	in.Name, in.Version = strings.TrimSpace(in.Name), strings.TrimSpace(in.Version)
	if !pythonPackageName.MatchString(in.Name) || len(in.Name) > 128 || len(in.Version) > 64 ||
		(in.Version != "" && (in.Action != "install" || !pythonPackageVersion.MatchString(in.Version))) ||
		(in.Action != "install" && in.Action != "uninstall") {
		response.Fail(c, response.Invalid, "Python 依赖名称、版本或操作无效")
		return
	}
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.ProjectType != "python" || row.RuntimeEnvironment == "" {
		response.Fail(c, response.Invalid, "请先为 Python 项目选择运行环境")
		return
	}
	spec := in.Name
	if in.Version != "" {
		spec += "==" + in.Version
	}
	id, err := m.tasks.Submit(task.Meta{Module: "website", Action: "python-" + in.Action, Target: row.Name + "/" + spec, ResourceID: row.ID, LockKey: "website:" + row.ID, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		var current store.Website
		if err := m.db.First(&current, "id = ?", row.ID).Error; err != nil {
			return nil, err
		}
		name := strings.ToLower(in.Name)
		if in.Action == "uninstall" && (name == "pip" || (name == "gunicorn" && current.RuntimeLaunch == "gunicorn") || (name == "pyuwsgi" && current.RuntimeLaunch == "uwsgi")) {
			return nil, errors.New("不能卸载项目当前使用的启动或依赖管理工具")
		}
		runtime, err := m.packages.RuntimeEnvironment("python", current.RuntimeEnvironment)
		if err != nil {
			return nil, err
		}
		return nil, m.packages.ChangePythonProjectPackage(ctx, row.ID, runtime, in.Action, spec, current.RuntimeRegistry, log.Line)
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
