package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	composeManaged      = "managed"
	composeExternal     = "external"
	composeProjectLabel = "com.docker.compose.project"
	composeWorkDirLabel = "com.docker.compose.project.working_dir"
	composeFilesLabel   = "com.docker.compose.project.config_files"
	composeEnvFileLabel = "com.docker.compose.project.environment_file"
)

type composeContainer struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Created int64             `json:"Created"`
	Labels  map[string]string `json:"Labels"`
	Ports   json.RawMessage   `json:"Ports"`
}

type composeProjectView struct {
	store.DockerComposeProject
	ComposeFile        string             `json:"composeFile"`
	MissingComposeFile bool               `json:"missingComposeFile"`
	Registered         bool               `json:"registered"`
	Status             string             `json:"status"`
	Running            int                `json:"running"`
	Total              int                `json:"total"`
	Containers         []composeContainer `json:"-"`
}

func validExistingComposeName(name string) bool {
	legacy, prefixed := strings.CutPrefix(name, "ezpanel-")
	return composeProjectName.MatchString(name) || prefixed && composeProjectName.MatchString(legacy)
}

func composeNameParam(c *gin.Context) (string, bool) {
	name := c.Param("name")
	if !validExistingComposeName(name) {
		response.Fail(c, response.Invalid, "Compose 项目名称无效")
		return "", false
	}
	return name, true
}

func (m *Module) composeProjectRoot() string {
	return filepath.Join(m.cfg.DataDir(), "docker-compose")
}

func (m *Module) composePath(path string, write bool) (string, error) {
	if strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("Compose 路径含有无效字符")
	}
	policy, err := pathsafe.NewFileManager(m.cfg.InstallDir)
	if err != nil {
		return "", err
	}
	real, err := policy.Check(path, write)
	if err != nil {
		return "", err
	}
	install, err := policy.Check(m.cfg.InstallDir, false)
	if err != nil {
		return "", err
	}
	// 仅允许使用面板数据目录下的专用编排子目录。
	root := filepath.Join(install, "data", "docker-compose")
	if real == root || (pathsafe.Contains(install, real) && !pathsafe.Contains(root, real)) {
		return "", pathsafe.ErrProtected
	}
	for _, root := range []string{"/home", "/srv", "/opt", "/var/lib", "/var/log", "/var/cache", "/var/run", "/run"} {
		if real == root {
			return "", pathsafe.ErrProtected
		}
	}
	for _, system := range []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root/.ssh", "/sbin", "/sys", "/usr", "/var/lib/docker", "/var/lib/containerd"} {
		if pathsafe.Contains(system, real) {
			return "", pathsafe.ErrProtected
		}
	}
	return real, nil
}

func composeView(row store.DockerComposeProject) composeProjectView {
	project := composeProjectView{DockerComposeProject: row, Registered: row.ID != "", Status: "stopped", Containers: []composeContainer{}}
	if len(row.ComposeFiles) > 0 {
		project.ComposeFile = row.ComposeFiles[0]
	} else {
		project.ComposeFiles = []string{}
	}
	return project
}

func (m *Module) discoverComposeProjects(ctx context.Context) (map[string]composeProjectView, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var containers []composeContainer
	filters := url.QueryEscape(`{"label":["` + composeProjectLabel + `"]}`)
	if err := m.json(ctx, "GET", "/containers/json?all=true&filters="+filters, nil, &containers); err != nil {
		return nil, err
	}
	projects := make(map[string]composeProjectView)
	for _, container := range containers {
		name := container.Labels[composeProjectLabel]
		if !validExistingComposeName(name) {
			continue
		}
		project, exists := projects[name]
		workDir := container.Labels[composeWorkDirLabel]
		filesLabel := container.Labels[composeFilesLabel]
		if !exists || (project.WorkDir == "" && workDir != "") || (len(project.ComposeFiles) == 0 && filesLabel != "" && workDir != "") {
			var files []string
			for _, file := range strings.Split(filesLabel, ",") {
				if file != "" {
					files = append(files, composeAbsolute(workDir, file))
				}
			}
			if workDir == "" && len(files) > 0 && filepath.IsAbs(files[0]) {
				workDir = filepath.Dir(files[0])
			}
			env := container.Labels[composeEnvFileLabel]
			if env == "" {
				env = ".env"
			}
			view := composeView(store.DockerComposeProject{Name: name, Source: composeExternal, WorkDir: workDir, ComposeFiles: files, EnvFile: composeAbsolute(workDir, env)})
			view.Containers = append(view.Containers, project.Containers...)
			project = view
		}
		project.Containers = append(project.Containers, container)
		projects[name] = project
	}
	for name, project := range projects {
		if len(project.ComposeFiles) == 0 && filepath.IsAbs(project.WorkDir) {
			project.ComposeFiles = defaultComposeFiles(project.WorkDir)
			if len(project.ComposeFiles) > 0 {
				project.ComposeFile = project.ComposeFiles[0]
			}
		}
		for _, item := range project.Containers {
			if item.Created > 0 {
				created := time.Unix(item.Created, 0)
				if project.CreatedAt.IsZero() || created.Before(project.CreatedAt) {
					project.CreatedAt = created
				}
			}
		}
		project.setContainerStatus(project.Containers)
		projects[name] = project
	}
	return projects, nil
}

func (p *composeProjectView) setContainerStatus(containers []composeContainer) {
	if containers != nil {
		p.Containers = containers
	}
	p.Total = len(containers)
	p.Running = 0
	failed := false
	for _, item := range containers {
		switch item.State {
		case "running":
			p.Running++
			failed = failed || strings.Contains(item.Status, "(unhealthy)")
		case "dead", "restarting":
			failed = true
		case "exited":
			failed = failed || (strings.HasPrefix(item.Status, "Exited (") && !strings.HasPrefix(item.Status, "Exited (0)"))
		}
	}
	switch {
	case failed:
		p.Status = "error"
	case p.Running > 0 && p.Running == p.Total:
		p.Status = "running"
	case p.Running > 0:
		p.Status = "partial"
	default:
		p.Status = "stopped"
	}
}

func (m *Module) listComposeProjects(c *gin.Context) {
	ctx := c.Request.Context()
	var rows []store.DockerComposeProject
	if err := m.db.WithContext(ctx).Find(&rows).Error; err != nil {
		fail(c, err)
		return
	}
	discovered, err := m.discoverComposeProjects(ctx)
	if err != nil && len(rows) == 0 {
		// Docker 未安装或未启动时仍可打开空列表。
		response.OK(c, []composeProjectView{})
		return
	}
	projects := make([]composeProjectView, 0, len(rows)+len(discovered))
	for _, row := range rows {
		project := composeView(row)
		project.setContainerStatus(discovered[row.Name].Containers)
		projects = append(projects, project)
		delete(discovered, row.Name)
	}
	for _, project := range discovered {
		projects = append(projects, project)
	}
	for i := range projects {
		_, err := m.checkComposeFiles(projects[i], true)
		projects[i].MissingComposeFile = errors.Is(err, errComposeConfigMissing)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	response.OK(c, projects)
}

func (m *Module) getComposeProject(ctx context.Context, name string) (composeProjectView, error) {
	var row store.DockerComposeProject
	err := m.db.WithContext(ctx).First(&row, "name = ?", name).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return composeProjectView{}, err
	}
	discovered, discoveryErr := m.discoverComposeProjects(ctx)
	if row.ID != "" {
		project := composeView(row)
		project.setContainerStatus(discovered[name].Containers)
		return project, nil
	}
	if discoveryErr != nil {
		return composeProjectView{}, fmt.Errorf("无法读取 Docker Compose 项目：%w", discoveryErr)
	}
	if project, ok := discovered[name]; ok {
		return project, nil
	}
	return composeProjectView{}, gorm.ErrRecordNotFound
}

func (m *Module) composeProjectFromRequest(c *gin.Context) (composeProjectView, bool) {
	name, ok := composeNameParam(c)
	if !ok {
		return composeProjectView{}, false
	}
	project, err := m.getComposeProject(c.Request.Context(), name)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.Fail(c, response.NotFound, "Compose 项目不存在")
	} else if err != nil {
		fail(c, err)
	}
	return project, err == nil
}

func (m *Module) composeProjectDetail(c *gin.Context) {
	project, ok := m.composeProjectFromRequest(c)
	if !ok {
		return
	}
	yaml, env, err := m.readComposeProjectFiles(project)
	_, configErr := m.checkComposeFiles(project, true)
	project.MissingComposeFile = errors.Is(configErr, errComposeConfigMissing)
	fileError := ""
	if err != nil {
		fileError = response.Summary(err.Error())
	}
	if c.Query("revealEnv") != "true" {
		env = maskComposeEnv(env)
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, struct {
		composeProjectView
		YAML       string             `json:"yaml"`
		Env        string             `json:"env"`
		Containers []composeContainer `json:"containers"`
		FileError  string             `json:"fileError,omitempty"`
	}{project, yaml, env, project.Containers, fileError})
}
