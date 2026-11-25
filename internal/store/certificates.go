package store

import "time"

type Certificate struct {
	ID               string `gorm:"primaryKey"`
	Domains          string
	Certificate      string
	KeyEnc           string
	Fingerprint      string
	Issuer           string
	NotBefore        time.Time
	NotAfter         time.Time
	Source           string
	Algorithm        string
	ValidationMethod string
	Email            string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (Certificate) TableName() string { return "certificates" }

type CertificateOrder struct {
	ID            string `gorm:"primaryKey"`
	Domains       string
	Email         string
	Method        string
	Algorithm     string
	AutoDeploy    bool
	SiteID        string
	RenewalID     string
	Status        string
	Error         string
	AccountURI    string
	OrderURI      string
	KeyEnc        string
	Challenges    string
	CertificateID string
	TaskID        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (CertificateOrder) TableName() string { return "certificate_orders" }

type ACMEAccount struct {
	Email  string `gorm:"primaryKey"`
	KeyEnc string
	URI    string
}

func (ACMEAccount) TableName() string { return "acme_accounts" }
