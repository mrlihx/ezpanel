package docker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"ezpanel/internal/pathsafe"
	"ezpanel/internal/store"
	"gorm.io/gorm"
)

const composeTextMax = 1 << 20

var errComposeConfigMissing = errors.New("Compose 配置文件不存在")

var composeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}
var composeOverrideNames = []string{"compose.override.yml", "compose.override.yaml", "docker-compose.override.yml", "docker-compose.override.yaml"}

func composeAbsolute(workDir, file string) string {
	if filepath.IsAbs(file) {
		return filepath.Clean(file)
	}
	return filepath.Join(workDir, file)
}

func defaultComposeFiles(workDir string) []string {
	files := []string{}
	for _, candidates := range [][]string{composeFileNames, composeOverrideNames} {
		for _, name := range candidates {
			file := filepath.Join(workDir, name)
			if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
				files = append(files, file)
				break
			}
		}
		if len(files) == 0 {
			break
		}
	}
	return files
}

func validateComposeText(yaml, env *string) error {
	if yaml != nil && (strings.TrimSpace(*yaml) == "" || len(*yaml) > composeTextMax || strings.ContainsRune(*yaml, 0)) {
		return errors.New("请填写 1 MiB 以内的 Compose YAML")
	}
	if env != nil && (len(*env) > composeTextMax || strings.ContainsRune(*env, 0)) {
		return errors.New(".env 文件须为 1 MiB 以内的有效文本")
	}
	return nil
}

type composeFileText struct {
	path string
	text string
	info os.FileInfo
}

func (m *Module) readComposeFile(file string) (composeFileText, error) {
	path, err := m.composePath(file, true)
	if err != nil {
		return composeFileText{}, err
	}
	out := composeFileText{path: path}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() || info.Size() > composeTextMax {
		return out, errors.New("编排文件必须为 1 MiB 以内的普通文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, composeTextMax+1))
	if err != nil {
		return out, err
	}
	if len(b) > composeTextMax || strings.ContainsRune(string(b), 0) {
		return out, errors.New("编排文件必须为 1 MiB 以内的有效文本")
	}
	out.text, out.info = string(b), info
	return out, nil
}

func (m *Module) composeFileState(project composeProjectView) (composeFileText, composeFileText, error) {
	if project.ComposeFile == "" || project.EnvFile == "" {
		return composeFileText{}, composeFileText{}, errors.New("未找到项目的 Compose 文件，请确认工作目录")
	}
	yaml, err := m.readComposeFile(project.ComposeFile)
	if err != nil {
		return yaml, composeFileText{}, err
	}
	env, err := m.readComposeFile(project.EnvFile)
	if err == nil && yaml.path == env.path {
		err = errors.New("Compose 文件与 .env 不能指向同一文件")
	}
	return yaml, env, err
}

func (m *Module) readComposeProjectFiles(project composeProjectView) (string, string, error) {
	yaml, env, err := m.composeFileState(project)
	if err == nil && yaml.info == nil {
		err = errors.New("Compose 文件不存在")
	}
	return yaml.text, env.text, err
}

func maskComposeEnv(env string) string {
	var masked []string
	for _, entry := range composeEnvDeclaration.FindAllStringSubmatch(strings.TrimPrefix(env, "\ufeff"), -1) {
		masked = append(masked, entry[1]+"=••••••")
	}
	return strings.Join(masked, "\n")
}

var composeEnvEscapes = strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\\`, `\`, `\"`, `"`, `\'`, `'`)

func composeEnvValues(env string) []string {
	var values []string
	for _, entry := range composeEnvDeclaration.FindAllStringSubmatch(strings.TrimPrefix(env, "\ufeff"), -1) {
		value := strings.TrimSpace(entry[2])
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[0] == value[len(value)-1] {
			value = value[1 : len(value)-1]
		} else {
			value, _, _ = strings.Cut(value, " #")
			value, _, _ = strings.Cut(value, "\t#")
		}
		values = append(values, strings.Split(strings.TrimSpace(value), "\n")...)
		values = append(values, strings.Split(composeEnvEscapes.Replace(value), "\n")...)
	}
	return values
}

func stageComposeFile(dir, suffix, content string, info os.FileInfo) (string, error) {
	f, err := os.CreateTemp(dir, ".compose-*"+suffix)
	if err != nil {
		return "", err
	}
	var saveErr error
	if info != nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			saveErr = f.Chown(int(stat.Uid), int(stat.Gid))
		}
		if saveErr == nil {
			saveErr = f.Chmod(info.Mode().Perm())
		}
	}
	if saveErr == nil {
		_, saveErr = f.WriteString(content)
	}
	if err = errors.Join(saveErr, f.Close()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (file composeFileText) restore() error {
	if file.info == nil {
		return os.Remove(file.path)
	}
	staged, err := stageComposeFile(filepath.Dir(file.path), ".restore", file.text, file.info)
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	return os.Rename(staged, file.path)
}

func (m *Module) checkComposeFiles(project composeProjectView, requireMain bool) (composeProjectView, error) {
	if len(project.ComposeFiles) == 0 && requireMain {
		return project, errComposeConfigMissing
	}
	work, err := m.composePath(project.WorkDir, true)
	if err != nil || len(project.ComposeFiles) == 0 {
		return project, errors.New("Compose 工作目录或文件路径无效")
	}
	project.WorkDir = work
	files := make([]string, len(project.ComposeFiles))
	for i, file := range project.ComposeFiles {
		files[i], err = m.composePath(file, true)
		if err != nil {
			return project, err
		}
		if i > 0 || requireMain {
			info, err := os.Stat(files[i])
			if errors.Is(err, os.ErrNotExist) {
				return project, errComposeConfigMissing
			}
			if err != nil {
				return project, err
			}
			if !info.Mode().IsRegular() {
				return project, errors.New("Compose 配置文件不是普通文件")
			}
		}
	}
	project.ComposeFiles, project.ComposeFile = files, files[0]
	project.EnvFile, err = m.composePath(project.EnvFile, true)
	if err != nil {
		return project, err
	}
	if project.EnvFile == project.ComposeFile {
		return project, errors.New("Compose 文件与 .env 不能指向同一文件")
	}
	if info, err := os.Stat(project.EnvFile); err == nil {
		if !info.Mode().IsRegular() || info.Size() > composeTextMax {
			return project, errors.New("环境变量文件必须为 1 MiB 以内的普通文件")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return project, err
	}
	return project, nil
}

type composeFileChange struct {
	YAML      *string `json:"yaml"`
	Env       *string `json:"env"`
	Apply     bool    `json:"apply"`
	removeEnv bool
}

// 暂存文件只替换读取路径，保留工作目录和覆盖文件顺序。
func stagedComposeFiles(project composeProjectView, change composeFileChange, yaml, env composeFileText) (composeProjectView, func(), error) {
	staged := project
	staged.ComposeFiles = append([]string(nil), project.ComposeFiles...)
	var paths []string
	cleanup := func() {
		for _, path := range paths {
			os.Remove(path)
		}
	}
	if change.YAML != nil {
		path, err := stageComposeFile(filepath.Dir(yaml.path), ".yaml", *change.YAML, yaml.info)
		if err != nil {
			return staged, cleanup, err
		}
		paths = append(paths, path)
		staged.ComposeFiles[0] = path
	}
	if change.Env != nil || change.removeEnv {
		content := ""
		if change.Env != nil {
			content = *change.Env
		}
		path, err := stageComposeFile(filepath.Dir(env.path), ".env", content, env.info)
		if err != nil {
			return staged, cleanup, err
		}
		paths = append(paths, path)
		staged.EnvFile = path
	}
	return staged, cleanup, nil
}

func prepareComposePreview(project composeProjectView, change composeFileChange, dir string) (composeProjectView, bool, error) {
	info, err := os.Stat(project.WorkDir)
	previewOnly := errors.Is(err, os.ErrNotExist)
	switch {
	case previewOnly:
		project.WorkDir = dir
	case err != nil:
		return project, false, err
	case !info.IsDir():
		return project, false, errors.New("Compose 工作目录不是目录")
	}
	staged, _, err := stagedComposeFiles(project, change, composeFileText{path: filepath.Join(dir, "compose.yaml")}, composeFileText{path: filepath.Join(dir, ".env")})
	if err == nil && previewOnly && change.Env != nil {
		// 新目录可直接提供 .env，让服务的相对 env_file 引用也能参与校验。
		env := filepath.Join(dir, ".env")
		err = os.Rename(staged.EnvFile, env)
		staged.EnvFile = env
	}
	return staged, previewOnly, err
}

func (m *Module) persistComposeFiles(ctx context.Context, cli composeCLI, project composeProjectView, change composeFileChange, username string) error {
	project, err := m.checkComposeFiles(project, change.YAML == nil)
	if err != nil {
		return err
	}
	yaml, env, err := m.composeFileState(project)
	if err != nil {
		return err
	}
	if (change.YAML != nil && !pathsafe.Contains(project.WorkDir, yaml.path)) || ((change.Env != nil || change.removeEnv) && !pathsafe.Contains(project.WorkDir, env.path)) {
		return errors.New("只能修改项目工作目录内的编排文件")
	}
	// 临时文件与各自目标同目录，两个文件都通过校验后才原子替换。
	staged, cleanup, err := stagedComposeFiles(project, change, yaml, env)
	defer cleanup()
	if err != nil {
		return err
	}
	newEnv := ""
	if change.Env != nil && env.info == nil {
		newEnv = env.path
	}
	if err := cli.check(ctx, staged, project.WorkDir, newEnv); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var changed []composeFileText
	if change.YAML != nil {
		if err := os.Rename(staged.ComposeFiles[0], yaml.path); err != nil {
			return err
		}
		changed = append(changed, yaml)
	}
	if change.Env != nil {
		err = os.Rename(staged.EnvFile, env.path)
	} else if change.removeEnv && env.info != nil {
		err = os.Remove(env.path)
	}
	if err == nil {
		if change.Env != nil || (change.removeEnv && env.info != nil) {
			changed = append(changed, env)
		}
		err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if project.Registered {
				if err := tx.Model(&project.DockerComposeProject).Updates(map[string]any{"updated_at": m.db.NowFunc(), "template_id": project.TemplateID}).Error; err != nil {
					return err
				}
			} else {
				project.ID = store.ID()
				if err := tx.Create(&project.DockerComposeProject).Error; err != nil {
					return err
				}
			}
			if yaml.info == nil || len(changed) == 0 {
				return nil
			}
			return m.saveComposeRevision(tx, project.ID, username, yaml, env)
		})
	}
	if err != nil {
		for _, file := range changed {
			err = errors.Join(err, file.restore())
		}
	}
	return err
}

func (m *Module) saveComposeRevision(tx *gorm.DB, id, username string, yaml, env composeFileText) error {
	encYAML, err := store.Encrypt(m.cfg.AESKey, yaml.text)
	if err != nil {
		return err
	}
	encEnv, err := store.Encrypt(m.cfg.AESKey, env.text)
	if err != nil {
		return err
	}
	row := store.DockerComposeRevision{ID: store.ID(), ProjectID: id, ComposeYAML: encYAML, EnvContent: encEnv, HadEnv: env.info != nil, CreatedBy: username}
	if err := tx.Create(&row).Error; err != nil {
		return err
	}
	var ids []string
	if err := tx.Model(&row).Where("project_id = ?", id).Order("created_at DESC, rowid DESC").Offset(10).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) != 0 {
		return tx.Delete(&store.DockerComposeRevision{}, "id IN ?", ids).Error
	}
	return nil
}
