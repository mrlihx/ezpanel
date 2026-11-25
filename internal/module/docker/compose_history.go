package docker

import (
	"context"
	"errors"
	"regexp"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var composeRevisionID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (m *Module) listComposeRevisions(c *gin.Context) {
	project, ok := m.composeProjectFromRequest(c)
	if !ok {
		return
	}
	rows := []store.DockerComposeRevision{}
	if project.Registered {
		err := m.db.WithContext(c.Request.Context()).Select("id", "created_at", "created_by").Where("project_id = ?", project.ID).Order("created_at DESC, rowid DESC").Limit(10).Find(&rows).Error
		if err != nil {
			fail(c, err)
			return
		}
	}
	response.OK(c, rows)
}

func (m *Module) readComposeRevision(ctx context.Context, project composeProjectView, id string) (store.DockerComposeRevision, error) {
	var row store.DockerComposeRevision
	if err := m.db.WithContext(ctx).First(&row, "project_id = ? AND id = ?", project.ID, id).Error; err != nil {
		return row, err
	}
	yaml, err := store.Decrypt(m.cfg.AESKey, row.ComposeYAML)
	if err != nil {
		return row, err
	}
	env, err := store.Decrypt(m.cfg.AESKey, row.EnvContent)
	if err != nil {
		return row, err
	}
	row.ComposeYAML, row.EnvContent = yaml, env
	return row, nil
}

func (m *Module) composeRevisionDetail(c *gin.Context) {
	id := c.Param("revision")
	if !composeRevisionID.MatchString(id) {
		response.Fail(c, response.Invalid, "版本标识无效")
		return
	}
	project, ok := m.composeProjectFromRequest(c)
	if !ok {
		return
	}
	row, err := m.readComposeRevision(c.Request.Context(), project, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.Fail(c, response.NotFound, "配置版本不存在")
		return
	} else if err != nil {
		fail(c, err)
		return
	}
	env := row.EnvContent
	if c.Query("revealEnv") != "true" {
		env = maskComposeEnv(env)
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"id": row.ID, "createdAt": row.CreatedAt, "createdBy": row.CreatedBy, "yaml": row.ComposeYAML, "env": env})
}

func (m *Module) rollbackComposeRevision(c *gin.Context) {
	name, ok := composeNameParam(c)
	if !ok {
		return
	}
	id := c.Param("revision")
	if !composeRevisionID.MatchString(id) {
		response.Fail(c, response.Invalid, "版本标识无效")
		return
	}
	var in struct {
		Apply bool `json:"apply"`
	}
	if !response.Bind(c, &in) {
		return
	}
	username := auth.Current(c).User.Username
	m.submitComposeTaskWithResult(c, name, "rollback", func(ctx context.Context, log *task.Log) (any, error) {
		project, err := m.getComposeProject(ctx, name)
		if err != nil {
			return nil, err
		}
		row, err := m.readComposeRevision(ctx, project, id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("配置版本不存在")
		} else if err != nil {
			return nil, err
		}
		change := composeFileChange{YAML: &row.ComposeYAML, Apply: in.Apply, removeEnv: !row.HadEnv}
		if row.HadEnv {
			change.Env = &row.EnvContent
		}
		saved, err := m.applyComposeChange(ctx, log, project, change, username)
		return gin.H{"project": name, "saved": saved, "applied": in.Apply && err == nil}, err
	})
}
