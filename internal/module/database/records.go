package database

import "time"

type databaseRecord struct {
	Engine    string `gorm:"primaryKey"`
	ServerID  string `gorm:"primaryKey"`
	Name      string `gorm:"primaryKey"`
	CreatedAt time.Time
}

func (databaseRecord) TableName() string { return "database_records" }
