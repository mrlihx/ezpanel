package store

import "time"

type MongoMetadata struct {
	ConnectionID string `gorm:"primaryKey"`
	Name         string `gorm:"primaryKey"`
	Username     string
	PasswordEnc  string `json:"-"`
	Remark       string
}

func (MongoMetadata) TableName() string { return "mongo_metadata" }

type MongoBackup struct {
	BackupFile
	ID             string `gorm:"primaryKey"`
	ConnectionID   string
	ConnectionName string
	Database       string
	FilePath       string `json:"-"`
	Size           int64
	CreatedAt      time.Time
}

func (MongoBackup) TableName() string { return "mongo_backups" }
