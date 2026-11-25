package store

import "time"

type User struct {
	ID           uint      `json:"id" gorm:"column:id;primaryKey"`
	Username     string    `json:"username" gorm:"column:username"`
	PasswordHash string    `json:"-" gorm:"column:password_hash"`
	AuthVersion  uint      `json:"authVersion" gorm:"column:auth_version;default:1"`
	CreatedAt    time.Time `json:"createdAt" gorm:"column:created_at"`
}

func (User) TableName() string { return "users" }

type LoginAttempt struct {
	IP        string `gorm:"primaryKey"`
	Count     int
	ExpiresAt int64
}

type Setting struct {
	Key   string `json:"key" gorm:"column:key;primaryKey"`
	Value string `json:"value" gorm:"column:value"`
}

func (Setting) TableName() string { return "settings" }

type ProxyRoute struct {
	Name      string          `json:"name"`
	Path      string          `json:"path"`
	Target    string          `json:"target"`
	Host      string          `json:"host"`
	Enabled   bool            `json:"enabled"`
	WebSocket *bool           `json:"webSocket,omitempty"`
	CacheTTL  int             `json:"cacheTTL,omitempty"`
	Headers   []ProxyHeader   `json:"headers,omitempty"`
	RateLimit *ProxyRateLimit `json:"rateLimit,omitempty"`
}

type ProxyHeader struct {
	Direction string `json:"direction"`
	Name      string `json:"name"`
	Value     string `json:"value"`
}

type ProxyRateLimit struct {
	RequestsPerSecond int `json:"requestsPerSecond"`
	Burst             int `json:"burst"`
}

type Website struct {
	RuntimeEnvironment    string
	RuntimeVersion        string
	RuntimeLaunch         string
	RuntimeEntry          string
	RuntimeCommand        string
	RuntimePort           int
	RuntimeWorkers        int
	RuntimeThreads        int
	RuntimeTimeout        int
	RuntimeFile           string
	RuntimeAutoStart      bool
	RuntimePackageManager string
	RuntimeScript         string
	RuntimeRequirements   string
	RuntimeRegistry       string
	RuntimePending        bool
	RuntimeEnv            string `json:"-"`

	ProjectType        string       `json:"projectType" gorm:"column:project_type"`
	CertificateID      string       `json:"-" gorm:"column:certificate_id"`
	SSLAllowMismatch   bool         `json:"-" gorm:"column:ssl_allow_mismatch"`
	LogDirectory       string       `json:"-" gorm:"column:log_directory"`
	RewriteRule        string       `json:"rewriteRule" gorm:"column:rewrite_rule"`
	RedirectEnabled    bool         `json:"redirectEnabled" gorm:"column:redirect_enabled"`
	RedirectTarget     string       `json:"redirectTarget" gorm:"column:redirect_target"`
	RedirectCode       int          `json:"redirectCode" gorm:"column:redirect_code;default:301"`
	RedirectKeepURI    bool         `json:"redirectKeepUri" gorm:"column:redirect_keep_uri;default:true"`
	PHPVersion         string       `json:"phpVersion" gorm:"column:php_version"`
	ID                 string       `json:"id" gorm:"column:id;primaryKey"`
	Name               string       `json:"name" gorm:"column:name"`
	Domains            string       `json:"domains" gorm:"column:domains"`
	Type               string       `json:"type" gorm:"column:type"`
	Root               string       `json:"root" gorm:"column:root"`
	RootCreatedByPanel bool         `json:"rootCreatedByPanel" gorm:"column:root_created_by_panel"`
	ProxyTarget        string       `json:"-" gorm:"column:proxy_target"`
	ProxyRoutes        []ProxyRoute `json:"proxyRoutes" gorm:"column:proxy_routes;serializer:json;default:'[]'"`
	SSLEnabled         bool         `json:"sslEnabled" gorm:"column:ssl_enabled"`
	ForceHTTPS         bool         `json:"forceHttps" gorm:"column:force_https"`
	Enabled            bool         `json:"enabled" gorm:"column:enabled"`
	Remark             string       `json:"remark" gorm:"column:remark"`
	CreatedAt          time.Time    `json:"createdAt" gorm:"column:created_at"`
	UpdatedAt          time.Time    `json:"updatedAt" gorm:"column:updated_at"`
}

func (Website) TableName() string { return "websites" }

type DBServer struct {
	InstanceID    string    `json:"instanceId,omitempty" gorm:"column:instance_id"`
	IsLocal       bool      `json:"local" gorm:"column:is_local"`
	ID            string    `json:"id" gorm:"column:id;primaryKey"`
	Name          string    `json:"name" gorm:"column:name"`
	Type          string    `json:"type" gorm:"column:type"`
	Network       string    `json:"network" gorm:"column:network"`
	Host          string    `json:"host" gorm:"column:host"`
	Port          int       `json:"port" gorm:"column:port"`
	SocketPath    string    `json:"socketPath" gorm:"column:socket_path"`
	Username      string    `json:"username" gorm:"column:username"`
	PasswordEnc   string    `json:"-" gorm:"column:password_enc"`
	TLSMode       string    `json:"tlsMode" gorm:"column:tls_mode"`
	TLSServerName string    `json:"tlsServerName" gorm:"column:tls_server_name"`
	TLSCAPath     string    `json:"-" gorm:"column:tls_ca_path"`
	CreatedAt     time.Time `json:"createdAt" gorm:"column:created_at"`
	UpdatedAt     time.Time `json:"updatedAt" gorm:"column:updated_at"`
}

func (DBServer) TableName() string { return "db_servers" }

// 备份方式独立于计划任务外键保存，删除计划后仍可识别来源。
type BackupFile struct {
	FileName    string `json:"fileName" gorm:"column:file_name"`
	StorageType string `json:"storageType" gorm:"column:storage_type;default:local"`
	StorageID   string `json:"-" gorm:"column:storage_id"`
	RemoteKey   string `json:"-" gorm:"column:remote_key"`
	Method      string `json:"method" gorm:"column:method;default:manual"`
}
type DBBackup struct {
	BackupFile
	ID         string    `json:"id" gorm:"column:id;primaryKey"`
	ServerID   *string   `json:"serverId" gorm:"column:server_id"`
	ServerName string    `json:"serverName" gorm:"column:server_name"`
	Database   string    `json:"database" gorm:"column:database"`
	FilePath   string    `json:"-" gorm:"column:file_path"`
	Size       int64     `json:"size" gorm:"column:size"`
	CronJobID  *string   `json:"cronJobId" gorm:"column:cron_job_id"`
	CreatedAt  time.Time `json:"createdAt" gorm:"column:created_at"`
}

func (DBBackup) TableName() string { return "db_backups" }

type SiteBackup struct {
	BackupFile
	ID        string    `json:"id" gorm:"column:id;primaryKey"`
	WebsiteID *string   `json:"websiteId" gorm:"column:website_id"`
	SiteName  string    `json:"siteName" gorm:"column:site_name"`
	FilePath  string    `json:"-" gorm:"column:file_path"`
	Size      int64     `json:"size" gorm:"column:size"`
	CronJobID *string   `json:"cronJobId" gorm:"column:cron_job_id"`
	CreatedAt time.Time `json:"createdAt" gorm:"column:created_at"`
}

func (SiteBackup) TableName() string { return "site_backups" }

type TrashItem struct {
	ID           string    `json:"id" gorm:"column:id;primaryKey"`
	Name         string    `json:"name" gorm:"column:name"`
	OriginalPath string    `json:"originalPath" gorm:"column:original_path"`
	StoragePath  string    `json:"-" gorm:"column:storage_path"`
	IsDir        bool      `json:"isDir" gorm:"column:is_dir"`
	FileType     string    `json:"fileType" gorm:"column:file_type"`
	Size         int64     `json:"size" gorm:"column:size"`
	DeletedAt    time.Time `json:"deletedAt" gorm:"column:deleted_at"`
}

func (TrashItem) TableName() string { return "trash_items" }

type CronJob struct {
	ID             string     `json:"id" gorm:"column:id;primaryKey"`
	Name           string     `json:"name" gorm:"column:name"`
	Type           string     `json:"type" gorm:"column:type"`
	Schedule       string     `json:"schedule" gorm:"column:schedule"`
	Config         string     `json:"config" gorm:"column:config"`
	KeepCount      int        `json:"keepCount" gorm:"column:keep_count"`
	Enabled        bool       `json:"enabled" gorm:"column:enabled"`
	DisabledReason string     `json:"disabledReason" gorm:"column:disabled_reason"`
	LastRunAt      *time.Time `json:"lastRunAt" gorm:"column:last_run_at"`
	LastStatus     *string    `json:"lastStatus" gorm:"column:last_status"`
	CreatedAt      time.Time  `json:"createdAt" gorm:"column:created_at"`
	UpdatedAt      time.Time  `json:"updatedAt" gorm:"column:updated_at"`
}

func (CronJob) TableName() string { return "cron_jobs" }

type Task struct {
	ID           string     `json:"id" gorm:"column:id;primaryKey"`
	Module       string     `json:"module" gorm:"column:module"`
	Action       string     `json:"action" gorm:"column:action"`
	Target       string     `json:"target" gorm:"column:target"`
	ResourceID   string     `json:"resourceId" gorm:"column:resource_id"`
	CronJobID    *string    `json:"cronJobId" gorm:"column:cron_job_id"`
	Status       string     `json:"status" gorm:"column:status"`
	ExitCode     *int       `json:"exitCode" gorm:"column:exit_code"`
	ErrorCode    int        `json:"errorCode" gorm:"column:error_code"`
	ErrorMsg     string     `json:"errorMsg" gorm:"column:error_msg"`
	Result       *string    `json:"result" gorm:"column:result"`
	LogPath      string     `json:"-" gorm:"column:log_path"`
	LogStart     int64      `json:"-" gorm:"column:log_start"`
	LogSize      *int64     `json:"-" gorm:"column:log_size"`
	LogTruncated bool       `json:"logTruncated" gorm:"column:log_truncated"`
	StartedAt    time.Time  `json:"startedAt" gorm:"column:started_at"`
	FinishedAt   *time.Time `json:"finishedAt" gorm:"column:finished_at"`
}

func (Task) TableName() string { return "tasks" }

type OpLog struct {
	UserAgent  string    `json:"userAgent" gorm:"column:user_agent"`
	ResourceID string    `json:"resourceId" gorm:"column:resource_id"`
	ID         uint      `json:"id" gorm:"column:id;primaryKey"`
	Username   *string   `json:"username" gorm:"column:username"`
	IP         string    `json:"ip" gorm:"column:ip"`
	Location   string    `json:"location" gorm:"-"`
	Module     string    `json:"module" gorm:"column:module"`
	Action     string    `json:"action" gorm:"column:action"`
	Target     string    `json:"target" gorm:"column:target"`
	TaskID     *string   `json:"taskId" gorm:"column:task_id"`
	Phase      string    `json:"phase" gorm:"column:phase"`
	Success    bool      `json:"success" gorm:"column:success"`
	ErrorCode  int       `json:"errorCode" gorm:"column:error_code"`
	ErrorMsg   string    `json:"errorMsg" gorm:"column:error_msg"`
	CreatedAt  time.Time `json:"createdAt" gorm:"column:created_at"`
}

func (OpLog) TableName() string { return "op_logs" }

// Plugin 仅持久化期望状态，运行状态与 token 留在内存。
type Plugin struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Type        string    `json:"type"`
	Enabled     bool      `json:"enabled"`
	Manifest    string    `json:"-"`
	Config      string    `json:"-"`
	Operation   *string   `json:"-"`
	InstalledAt time.Time `json:"installedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (Plugin) TableName() string { return "plugins" }

type PluginKV struct {
	PluginID  string `gorm:"primaryKey"`
	Key       string `gorm:"primaryKey"`
	Value     string
	UpdatedAt time.Time
}

func (PluginKV) TableName() string { return "plugin_kv" }

// AuthSession 仅保存令牌摘要和活动时间，不落盘原始Cookie。
type AuthSession struct {
	TokenHash  string `gorm:"primaryKey"`
	UserID     uint
	LastActive time.Time
	ExpiresAt  time.Time
}

func (AuthSession) TableName() string { return "auth_sessions" }

type DatabaseMetadata struct {
	ServerID    string `gorm:"column:server_id;primaryKey"`
	Name        string `gorm:"column:name;primaryKey"`
	Username    string `gorm:"column:username"`
	Host        string `gorm:"column:host"`
	PasswordEnc string `json:"-" gorm:"column:password_enc"`
	Remark      string `gorm:"column:remark"`
}

func (DatabaseMetadata) TableName() string { return "db_metadata" }

type DatabaseTrash struct {
	ID           string `gorm:"primaryKey"`
	ServerID     *string
	ServerName   string
	DatabaseName string
	BackupID     string
	Charset      string
	Collation    string
	Metadata     string `json:"-"`
	State        string
	DeletedAt    time.Time
}

func (DatabaseTrash) TableName() string { return "database_trash" }
