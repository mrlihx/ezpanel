package docker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"ezpanel/internal/auth"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type composeCreateInput struct {
	Name           string  `json:"name"`
	Source         string  `json:"source"`
	WorkDir        string  `json:"workDir"`
	ComposeFile    string  `json:"composeFile"`
	YAML           *string `json:"yaml"`
	Env            *string `json:"env"`
	Remark         string  `json:"remark"`
	TemplateID     string  `json:"templateId"`
	Deploy         bool    `json:"deploy"`
	Pull           bool    `json:"pull"`
	RemoveOrphans  bool    `json:"removeOrphans"`
	ForceRecreate  bool    `json:"forceRecreate"`
	legacyTemplate bool
}

func (m *Module) submitComposeTask(c *gin.Context, name, action string, fn func(context.Context, *task.Log) error) {
	m.submitComposeTaskWithResult(c, name, action, func(ctx context.Context, log *task.Log) (any, error) {
		if err := fn(ctx, log); err != nil {
			return nil, err
		}
		return gin.H{"project": name}, nil
	})
}

func (m *Module) submitComposeTaskWithResult(c *gin.Context, name, action string, fn func(context.Context, *task.Log) (any, error)) {
	id, err := m.tasks.Submit(task.Meta{Module: "docker", Action: "compose-" + action, ResourceID: name, Target: name, LockKey: "docker:compose:" + name, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, composeActionTimeout)
		defer cancel()
		result, err := fn(ctx, log)
		if err != nil {
			log.Line(err.Error())
		}
		return result, err
	})
	if errors.Is(err, task.ErrBusy) {
		response.Fail(c, response.Conflict, "项目另有任务正在执行")
	} else if err != nil {
		fail(c, err)
	} else {
		c.Set("panel.taskSubmitted", true)
		response.OK(c, gin.H{"taskId": id})
	}
}

func (m *Module) createComposeProject(c *gin.Context) {
	var in composeCreateInput
	if response.Bind(c, &in) {
		m.submitComposeCreate(c, in)
	}
}

func (m *Module) submitComposeCreate(c *gin.Context, in composeCreateInput) {
	if !composeProjectName.MatchString(in.Name) && !(in.legacyTemplate && validExistingComposeName(in.Name)) {
		response.Fail(c, response.Invalid, "项目名须以小写字母或数字开头，最多 63 个字符，可包含 - 和 _")
		return
	}
	if in.Source == "" {
		in.Source = composeManaged
	}
	if in.Source != composeManaged && in.Source != composeExternal {
		response.Fail(c, response.Invalid, "Compose 项目来源无效")
		return
	}
	in.Remark = strings.TrimSpace(in.Remark)
	if utf8.RuneCountInString(in.Remark) > 256 || strings.IndexFunc(in.Remark, unicode.IsControl) >= 0 {
		response.Fail(c, response.Invalid, "备注最多 256 个字符，且不能包含控制字符")
		return
	}
	if in.TemplateID != "" {
		item, err := m.loadComposeTemplate(in.TemplateID)
		if err != nil {
			response.Fail(c, response.Invalid, "无法读取所选 Compose 模板")
			return
		}
		if in.YAML == nil {
			in.YAML = &item.YAML
		}
	}
	if err := validateComposeText(in.YAML, in.Env); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	if (in.Source == composeManaged && in.YAML == nil) || (in.Source == composeExternal && (in.YAML != nil || in.Env != nil)) {
		response.Fail(c, response.Invalid, "在线创建需要填写 YAML，路径导入会直接使用原有文件")
		return
	}
	username := auth.Current(c).User.Username
	m.submitComposeTask(c, in.Name, "create", func(ctx context.Context, log *task.Log) error {
		var existing composeProjectView
		if err := m.db.WithContext(ctx).First(&existing.DockerComposeProject, "name = ?", in.Name).Error; err == nil {
			existing = composeView(existing.DockerComposeProject)
			if !in.legacyTemplate || existing.Source != composeManaged || existing.WorkDir != filepath.Join(m.composeProjectRoot(), in.Name) {
				return errors.New("该项目已在面板中登记")
			}
			existing.TemplateID = in.TemplateID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if !existing.Registered {
			discovered, err := m.discoverComposeProjects(ctx)
			if err != nil && in.Deploy {
				return err
			}
			existing = discovered[in.Name]
			if existing.Name != "" && in.Source != composeExternal {
				return errors.New("宿主机已存在同名 Compose 项目，请通过路径导入纳管")
			}
		}
		project, err := m.composeInputProject(in, existing)
		if err != nil {
			return err
		}
		cli, err := detectComposeCLI(ctx)
		if err != nil {
			return err
		}
		if in.Source == composeExternal {
			project, err = m.checkComposeFiles(project, true)
			if err != nil {
				return err
			}
			log.Line("正在校验导入项目的编排配置")
			if err := cli.check(ctx, project, project.WorkDir, ""); err != nil {
				return err
			}
			project.ID = store.ID()
			if err := m.db.WithContext(ctx).Create(&project.DockerComposeProject).Error; err != nil {
				return err
			}
		} else {
			release, err := m.tasks.Acquire("docker:compose-dir:" + project.WorkDir)
			if err != nil {
				return err
			}
			defer release()
			if !in.legacyTemplate {
				if _, err := os.Lstat(project.ComposeFile); !errors.Is(err, os.ErrNotExist) {
					return errors.New("目标 Compose 文件已存在，请通过路径导入纳管")
				}
				if in.Env != nil && *in.Env == "" {
					in.Env = nil
				} else if in.Env != nil {
					if _, err := os.Lstat(project.EnvFile); !errors.Is(err, os.ErrNotExist) {
						return errors.New("目标 .env 已存在，请选择空目录或保留原有环境变量")
					}
				}
			}
			_, statErr := os.Stat(project.WorkDir)
			project.OwnedDir = project.OwnedDir || errors.Is(statErr, os.ErrNotExist)
			if err := os.MkdirAll(project.WorkDir, 0700); err != nil {
				return err
			}
			log.Line("正在校验并保存编排文件")
			if err := m.persistComposeFiles(ctx, cli, project, composeFileChange{YAML: in.YAML, Env: in.Env}, username); err != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					_ = os.Remove(project.WorkDir)
				}
				return err
			}
		}
		if in.Deploy {
			return cli.up(ctx, project, log, in.Pull, in.RemoveOrphans, in.ForceRecreate)
		}
		return nil
	})
}

func (m *Module) composeInputProject(in composeCreateInput, existing composeProjectView) (composeProjectView, error) {
	if existing.Name != "" {
		if in.WorkDir != "" {
			dir, err := m.composePath(in.WorkDir, true)
			actual, actualErr := m.composePath(existing.WorkDir, true)
			if err != nil || actualErr != nil || dir != actual {
				return existing, errors.New("所选工作目录与已存在的项目不一致")
			}
		}
		if in.ComposeFile != "" && existing.ComposeFile != "" {
			file, err := m.composePath(composeAbsolute(existing.WorkDir, in.ComposeFile), true)
			actual, actualErr := m.composePath(existing.ComposeFile, true)
			if err != nil || actualErr != nil || file != actual {
				return existing, errors.New("所选 Compose 文件与已存在的项目不一致")
			}
		}
		if len(existing.ComposeFiles) != 0 || in.ComposeFile == "" {
			if !existing.Registered {
				existing.Remark = in.Remark
			}
			return existing, nil
		}
		if in.WorkDir == "" {
			in.WorkDir = existing.WorkDir
		}
	}
	if in.WorkDir == "" {
		if in.Source == composeExternal {
			if !filepath.IsAbs(in.ComposeFile) {
				return existing, errors.New("请选择已有项目的工作目录")
			}
			in.WorkDir = filepath.Dir(in.ComposeFile)
		} else {
			in.WorkDir = filepath.Join(m.composeProjectRoot(), in.Name)
		}
	}
	workDir, err := m.composePath(in.WorkDir, true)
	if err != nil {
		return existing, err
	}
	var files []string
	switch {
	case in.Source == composeExternal && in.ComposeFile == "":
		files = defaultComposeFiles(workDir)
	case in.ComposeFile != "":
		files = []string{composeAbsolute(workDir, in.ComposeFile)}
		if in.Source == composeExternal {
			defaults := defaultComposeFiles(workDir)
			if len(defaults) > 0 && defaults[0] == files[0] {
				files = defaults
			}
		}
	default:
		files = []string{filepath.Join(workDir, "compose.yaml")}
	}
	if len(files) == 0 {
		return existing, errors.New("所选目录内没有找到 Compose 文件")
	}
	existing.Name, existing.Source, existing.WorkDir = in.Name, in.Source, workDir
	existing.ComposeFiles, existing.ComposeFile, existing.EnvFile = files, files[0], filepath.Join(workDir, ".env")
	existing.Remark, existing.TemplateID = in.Remark, in.TemplateID
	return existing, nil
}

func (m *Module) validateComposeProject(c *gin.Context) {
	var in composeCreateInput
	if !response.Bind(c, &in) {
		return
	}
	if !validExistingComposeName(in.Name) {
		response.Fail(c, response.Invalid, "Compose 项目名称无效")
		return
	}
	if err := validateComposeText(in.YAML, in.Env); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	ctx := c.Request.Context()
	existing, lookupErr := m.getComposeProject(ctx, in.Name)
	// Docker daemon 停止时，Compose 仍可校验新的离线配置。
	if lookupErr != nil && in.YAML == nil && in.WorkDir == "" && in.ComposeFile == "" {
		response.Fail(c, response.Invalid, "请先选择 Compose 文件或填写 YAML")
		return
	}
	project, err := m.composeInputProject(in, existing)
	if err == nil {
		project, err = m.checkComposeFiles(project, in.YAML == nil)
	}
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	dir, err := os.MkdirTemp("", "ezpanel-compose-*")
	if err != nil {
		fail(c, err)
		return
	}
	defer os.RemoveAll(dir)
	staged, previewOnly, err := prepareComposePreview(project, composeFileChange{YAML: in.YAML, Env: in.Env}, dir)
	if err != nil {
		fail(c, err)
		return
	}
	cli, err := detectComposeCLI(ctx)
	if err == nil {
		newEnv := ""
		if in.Env != nil && !previewOnly {
			if _, statErr := os.Stat(project.EnvFile); errors.Is(statErr, os.ErrNotExist) {
				newEnv = project.EnvFile
			}
		}
		err = cli.check(ctx, staged, staged.WorkDir, newEnv)
	}
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	response.OK(c, gin.H{"valid": true, "previewOnly": previewOnly})
}

func (m *Module) updateComposeFiles(c *gin.Context) {
	name, ok := composeNameParam(c)
	if !ok {
		return
	}
	var change composeFileChange
	if !response.Bind(c, &change) {
		return
	}
	if change.YAML == nil && change.Env == nil {
		response.Fail(c, response.Invalid, "请填写 Compose YAML 或 .env")
		return
	}
	if err := validateComposeText(change.YAML, change.Env); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	username := auth.Current(c).User.Username
	action := "files"
	if change.Env != nil && change.YAML == nil {
		action = "env"
	}
	m.submitComposeTaskWithResult(c, name, action, func(ctx context.Context, log *task.Log) (any, error) {
		project, err := m.getComposeProject(ctx, name)
		if err != nil {
			return nil, err
		}
		saved, err := m.applyComposeChange(ctx, log, project, change, username)
		return gin.H{"project": name, "saved": saved, "applied": change.Apply && err == nil}, err
	})
}

func (m *Module) applyComposeChange(ctx context.Context, log *task.Log, project composeProjectView, change composeFileChange, username string) (bool, error) {
	if change.Apply {
		if err := m.json(ctx, http.MethodGet, "/_ping", nil, nil); err != nil {
			return false, err
		}
	}
	cli, err := detectComposeCLI(ctx)
	if err != nil {
		return false, err
	}
	log.Line("正在校验并保存编排文件")
	if err := m.persistComposeFiles(ctx, cli, project, change, username); err != nil {
		return false, err
	}
	if change.Apply {
		return true, cli.up(ctx, project, log, false, true, false)
	}
	return true, nil
}

func (m *Module) composeProjectAction(c *gin.Context) {
	name, ok := composeNameParam(c)
	if !ok {
		return
	}
	var in struct {
		Action        string `json:"action"`
		RemoveVolumes bool   `json:"removeVolumes"`
		RemoveImages  bool   `json:"removeImages"`
	}
	if !response.Bind(c, &in) {
		return
	}
	switch in.Action {
	case "up", "start", "stop", "restart", "down", "pull", "rebuild", "pull-up":
	default:
		response.Fail(c, response.Invalid, "不支持的 Compose 操作")
		return
	}
	m.submitComposeTask(c, name, in.Action, func(ctx context.Context, log *task.Log) error {
		project, err := m.getComposeProject(ctx, name)
		if err != nil {
			return err
		}
		project, err = m.checkComposeFiles(project, true)
		if err != nil {
			return err
		}
		cli, err := detectComposeCLI(ctx)
		if err != nil {
			return err
		}
		switch in.Action {
		case "up":
			return cli.up(ctx, project, log, false, false, false)
		case "pull-up":
			if err := cli.run(ctx, project, log, "pull"); err != nil {
				return err
			}
			return cli.up(ctx, project, log, false, true, false)
		case "rebuild":
			return cli.run(ctx, project, log, "up", "--detach", "--build", "--force-recreate", "--remove-orphans")
		case "down":
			return cli.run(ctx, project, log, composeDownArgs(in.RemoveVolumes, in.RemoveImages)...)
		default:
			return cli.run(ctx, project, log, in.Action)
		}
	})
}

func composeDownArgs(removeVolumes, removeImages bool) []string {
	args := []string{"down", "--remove-orphans"}
	if removeVolumes {
		args = append(args, "--volumes")
	}
	if removeImages {
		args = append(args, "--rmi", "local")
	}
	return args
}

func (m *Module) deleteComposeProject(c *gin.Context) {
	name, ok := composeNameParam(c)
	if !ok {
		return
	}
	removeContainers := c.DefaultQuery("removeContainers", "true") == "true"
	removeVolumes := c.Query("removeVolumes") == "true"
	removeFiles := c.Query("removeFiles") == "true"
	allowMissingConfig := c.Query("allowMissingConfig") == "true"
	confirmName := c.Query("confirmName")
	if removeVolumes && !removeContainers {
		response.Fail(c, response.Invalid, "移除数据卷需要同时移除容器和网络")
		return
	}
	if removeFiles && !removeContainers {
		response.Fail(c, response.Invalid, "删除工作目录需要同时移除容器和网络")
		return
	}
	if allowMissingConfig && (!removeContainers || removeVolumes || removeFiles || confirmName != name) {
		response.Fail(c, response.Invalid, "配置缺失时只能移除容器，请确认项目名称")
		return
	}
	m.submitComposeTask(c, name, "delete", func(ctx context.Context, log *task.Log) error {
		project, err := m.getComposeProject(ctx, name)
		if err != nil {
			return err
		}
		if !removeContainers && !project.Registered {
			return errors.New("项目尚未纳管，请选择移除容器")
		}
		if removeFiles {
			release, err := m.tasks.Acquire("docker:compose-dir:" + project.WorkDir)
			if err != nil {
				return err
			}
			defer release()
			if err := m.checkComposeDeleteDir(ctx, project, confirmName); err != nil {
				return err
			}
		}
		if removeContainers {
			if err := m.removeComposeProjectContainers(ctx, log, project, removeVolumes, allowMissingConfig); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if removeFiles {
			if err := os.RemoveAll(project.WorkDir); err != nil {
				return err
			}
		}
		if project.Registered {
			return m.db.WithContext(ctx).Delete(&project.DockerComposeProject).Error
		}
		return nil
	})
}

func (m *Module) removeComposeProjectContainers(ctx context.Context, log *task.Log, project composeProjectView, removeVolumes, allowMissingConfig bool) error {
	checked, err := m.checkComposeFiles(project, true)
	switch {
	case err == nil && !allowMissingConfig:
		cli, err := detectComposeCLI(ctx)
		if err != nil {
			return err
		}
		if err := cli.run(ctx, checked, log, composeDownArgs(removeVolumes, false)...); err != nil {
			return err
		}
	case errors.Is(err, errComposeConfigMissing) && allowMissingConfig:
		// 配置已丢失时仅移除已核对归属的容器，不触碰卷与网络。
		projects, err := m.discoverComposeProjects(ctx)
		if err != nil {
			return err
		}
		containers := projects[project.Name].Containers
		for _, item := range containers {
			workDir := item.Labels[composeWorkDirLabel]
			if item.Labels[composeProjectLabel] != project.Name || !resourceName.MatchString(item.ID) ||
				(project.WorkDir != "" && filepath.Clean(workDir) != filepath.Clean(project.WorkDir)) {
				return errors.New("发现同名或无法核对工作目录的容器，停止移除")
			}
		}
		for _, item := range containers {
			log.Line("正在移除容器 " + item.ID)
			path := "/containers/" + item.ID
			switch item.State {
			case "running", "paused", "restarting":
				if err := m.json(ctx, http.MethodPost, path+"/stop", nil, nil); err != nil {
					return fmt.Errorf("停止容器 %s 失败：%w", item.ID, err)
				}
			}
			if err := m.json(ctx, http.MethodDelete, path+"?v=false", nil, nil); err != nil {
				return fmt.Errorf("移除容器 %s 失败：%w", item.ID, err)
			}
		}
	case errors.Is(err, errComposeConfigMissing):
		return errors.New("Compose 配置文件不存在，请确认仅移除项目容器或取消纳管")
	case err != nil:
		return err
	default:
		return errors.New("Compose 配置文件已恢复，请重新确认移除方式")
	}
	projects, err := m.discoverComposeProjects(ctx)
	if err != nil {
		return err
	}
	if len(projects[project.Name].Containers) != 0 {
		return errors.New("项目仍有容器，保留纳管记录")
	}
	return nil
}

func (m *Module) checkComposeDeleteDir(ctx context.Context, project composeProjectView, confirmName string) error {
	if project.Source != composeManaged || !project.OwnedDir {
		return errors.New("只允许删除面板创建的项目专用工作目录")
	}
	dir, err := m.composePath(project.WorkDir, true)
	if err != nil || dir != project.WorkDir {
		return errors.New("工作目录已变更或指向受保护路径")
	}
	if dir != filepath.Join(m.composeProjectRoot(), project.Name) && confirmName != project.Name {
		return errors.New("删除自定义工作目录前须确认项目名称")
	}
	var otherDirs []string
	if err := m.db.WithContext(ctx).Model(&project.DockerComposeProject).Where("name <> ?", project.Name).Pluck("work_dir", &otherDirs).Error; err != nil {
		return err
	}
	discovered, err := m.discoverComposeProjects(ctx)
	if err != nil {
		return err
	}
	for _, other := range discovered {
		if other.Name != project.Name && other.WorkDir != "" {
			otherDirs = append(otherDirs, other.WorkDir)
		}
	}
	for _, other := range otherDirs {
		if real, err := m.composePath(other, true); err == nil {
			other = real
		}
		if pathsafe.Contains(dir, other) || pathsafe.Contains(other, dir) {
			return errors.New("该工作目录与其他 Compose 项目重叠，无法删除")
		}
	}
	return nil
}
