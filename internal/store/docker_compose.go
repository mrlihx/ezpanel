package store

import "time"

type DockerComposeProject struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	Name         string    `json:"name"`
	Source       string    `json:"source"`
	OwnedDir     bool      `json:"canRemoveFiles"`
	WorkDir      string    `json:"workDir"`
	ComposeFiles []string  `json:"composeFiles" gorm:"serializer:json"`
	EnvFile      string    `json:"envFile"`
	Remark       string    `json:"remark"`
	TemplateID   string    `json:"templateId"`
	CreatedAt    time.Time `json:"createdAt,omitzero"`
	UpdatedAt    time.Time `json:"updatedAt,omitzero"`
}

type DockerComposeRevision struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	ProjectID   string    `json:"-"`
	ComposeYAML string    `json:"-"`
	EnvContent  string    `json:"-"`
	HadEnv      bool      `json:"-"`
	CreatedBy   string    `json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
}
