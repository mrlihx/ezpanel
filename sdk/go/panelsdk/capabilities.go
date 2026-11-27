package panelsdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type ResourceSummary struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind,omitempty"`
	Status    string     `json:"status,omitempty"`
	Version   string     `json:"version,omitempty"`
	Domains   []string   `json:"domains,omitempty"`
	SiteID    string     `json:"siteId,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type TaskSummary struct {
	TaskID     string          `json:"taskId"`
	Action     string          `json:"action"`
	Target     string          `json:"target"`
	ResourceID string          `json:"resourceId"`
	Status     string          `json:"status"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt"`
	ErrorMsg   string          `json:"errorMsg"`
	Result     json.RawMessage `json:"result,omitempty"`
}

type JobInvocation struct {
	JobID   string            `json:"jobId"`
	TaskID  string            `json:"taskId"`
	Trigger string            `json:"trigger"`
	Params  map[string]string `json:"params"`
	Step    string            `json:"step"`
}

type Event struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	SiteID     string    `json:"siteId,omitempty"`
	ResourceID string    `json:"resourceId,omitempty"`
	Action     string    `json:"action,omitempty"`
	TaskID     string    `json:"taskId"`
	Time       time.Time `json:"time"`
}

type ProviderNotice struct {
	ChannelID string `json:"channelId"`
	Target    string `json:"target"`
	Title     string `json:"title"`
	Message   string `json:"message"`
}

func (p *Plugin) OnNotification(send func(context.Context, ProviderNotice) error) error {
	return p.HandleFunc("/api/_panel/notification", callbackHandler(64<<10, func(ctx context.Context, notice ProviderNotice) (any, error) {
		return nil, send(ctx, notice)
	}))
}

type DNSChange struct {
	Action string `json:"action"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}

func (p *Plugin) OnDNSChange(change func(context.Context, DNSChange) error) error {
	return p.HandleFunc("/api/_panel/dns", callbackHandler(4096, func(ctx context.Context, request DNSChange) (any, error) {
		return nil, change(ctx, request)
	}))
}

type DeploymentHook struct {
	Project string `json:"project"`
	Phase   string `json:"phase"`
	Status  string `json:"status"`
	TaskID  string `json:"taskId"`
}

func (p *Plugin) OnDeployment(run func(context.Context, DeploymentHook) error) error {
	return p.HandleFunc("/api/_panel/deployment", callbackHandler(4096, func(ctx context.Context, hook DeploymentHook) (any, error) {
		return nil, run(ctx, hook)
	}))
}

type ServiceCall struct {
	Caller string          `json:"caller"`
	Name   string          `json:"name"`
	Data   json.RawMessage `json:"data"`
}

func (p *Plugin) OnService(run func(context.Context, ServiceCall) (any, error)) error {
	return p.HandleFunc("/api/_panel/service", callbackHandler(100<<10, func(ctx context.Context, call ServiceCall) (any, error) {
		data, err := run(ctx, call)
		return map[string]any{"data": data}, err
	}))
}

type CheckResult struct {
	Healthy bool   `json:"healthy"`
	Message string `json:"message,omitempty"`
}

func (p *Plugin) OnMonitorCheck(check func(context.Context, string) (CheckResult, error)) error {
	type request struct {
		CheckID string `json:"checkId"`
	}
	return p.HandleFunc("/api/_panel/check", callbackHandler(4096, func(ctx context.Context, input request) (any, error) {
		data, err := check(ctx, input.CheckID)
		return map[string]CheckResult{"data": data}, err
	}))
}

func (h *HostClient) CallService(ctx context.Context, pluginID, name string, input, output any) error {
	if input == nil {
		input = json.RawMessage("null")
	}
	return h.call(ctx, http.MethodPost, "/services/"+url.PathEscape(pluginID)+"/"+url.PathEscape(name), input, output)
}

func (h *HostClient) resources(ctx context.Context, kind string) ([]ResourceSummary, error) {
	var rows []ResourceSummary
	err := h.call(ctx, http.MethodGet, "/resources/"+kind, nil, &rows)
	return rows, err
}

func (h *HostClient) Sites(ctx context.Context) ([]ResourceSummary, error) {
	return h.resources(ctx, "sites")
}

func (h *HostClient) Containers(ctx context.Context) ([]ResourceSummary, error) {
	return h.resources(ctx, "containers")
}

func (h *HostClient) Databases(ctx context.Context) ([]ResourceSummary, error) {
	return h.resources(ctx, "databases")
}

func (h *HostClient) Certificates(ctx context.Context) ([]ResourceSummary, error) {
	return h.resources(ctx, "certificates")
}

func (h *HostClient) Backups(ctx context.Context) ([]ResourceSummary, error) {
	return h.resources(ctx, "backups")
}

func (h *HostClient) ComposeProjects(ctx context.Context) ([]ResourceSummary, error) {
	return h.resources(ctx, "compose")
}

func (h *HostClient) DeployCompose(ctx context.Context, name string) (string, error) {
	return h.submitTask(ctx, "/compose/"+url.PathEscape(name)+"/deploy", nil)
}

type ApplicationStatus struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Installed bool    `json:"installed"`
	Running   *bool   `json:"running"`
	Version   *string `json:"version"`
}

func (h *HostClient) Applications(ctx context.Context) ([]ResourceSummary, error) {
	return h.resources(ctx, "apps")
}

func (h *HostClient) AppStatus(ctx context.Context, id string) (ApplicationStatus, error) {
	var out ApplicationStatus
	err := h.call(ctx, http.MethodGet, "/apps/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (h *HostClient) ControlApp(ctx context.Context, id, action string) (string, error) {
	return h.submitTask(ctx, "/apps/"+url.PathEscape(id)+"/control", map[string]string{"action": action})
}

type SecurityStatus struct {
	FirewallEnabled    bool       `json:"firewallEnabled"`
	FirewallRules      int        `json:"firewallRules"`
	HardeningEnabled   bool       `json:"hardeningEnabled"`
	HardeningRules     int        `json:"hardeningRules"`
	HardeningCheckedAt *time.Time `json:"hardeningCheckedAt,omitempty"`
	WAFEnabled         bool       `json:"wafEnabled"`
}

func (h *HostClient) SecurityStatus(ctx context.Context) (SecurityStatus, error) {
	var out SecurityStatus
	err := h.call(ctx, http.MethodGet, "/security", nil, &out)
	return out, err
}

func (h *HostClient) ScanHardening(ctx context.Context) (string, error) {
	return h.submitTask(ctx, "/security/scan", nil)
}

type AuditRecord struct {
	ID        uint      `json:"id"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	TaskID    *string   `json:"taskId"`
	Phase     string    `json:"phase"`
	Success   bool      `json:"success"`
	ErrorCode int       `json:"errorCode"`
	CreatedAt time.Time `json:"createdAt"`
}

type AuditPage struct {
	List  []AuditRecord `json:"list"`
	Total int64         `json:"total"`
}

func (h *HostClient) AuditHistory(ctx context.Context, page, size int) (AuditPage, error) {
	query := url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(size)}}
	var out AuditPage
	err := h.call(ctx, http.MethodGet, "/audit?"+query.Encode(), nil, &out)
	return out, err
}

type MetricPoint struct {
	TS         time.Time `json:"ts"`
	CPU        *float64  `json:"cpu"`
	MemPercent *float64  `json:"memPercent"`
	Load1      *float64  `json:"load1"`
	NetUp      *float64  `json:"netUp"`
	NetDown    *float64  `json:"netDown"`
	DiskRead   *float64  `json:"diskRead"`
	DiskWrite  *float64  `json:"diskWrite"`
}

func (h *HostClient) MonitoringHistory(ctx context.Context, start, end time.Time) ([]MetricPoint, error) {
	query := url.Values{"start": {strconv.FormatInt(start.UnixMilli(), 10)}, "end": {strconv.FormatInt(end.UnixMilli(), 10)}}
	var out []MetricPoint
	err := h.call(ctx, http.MethodGet, "/monitoring/history?"+query.Encode(), nil, &out)
	return out, err
}

func (h *HostClient) Notify(ctx context.Context, title, message string) error {
	return h.call(ctx, http.MethodPost, "/notify", map[string]string{"title": title, "message": message}, nil)
}

func (h *HostClient) Credential(ctx context.Context, slot string) (string, error) {
	var out struct{ Value string }
	err := h.call(ctx, http.MethodGet, "/credentials/"+url.PathEscape(slot), nil, &out)
	return out.Value, err
}

func (h *HostClient) CreateSiteBackup(ctx context.Context, siteID string) (string, error) {
	var out struct {
		TaskID string `json:"taskId"`
	}
	err := h.call(ctx, http.MethodPost, "/sites/"+url.PathEscape(siteID)+"/backup", nil, &out)
	return out.TaskID, err
}

type SiteBackupPage struct {
	List []struct {
		ID          string    `json:"id"`
		FileName    string    `json:"fileName"`
		StorageType string    `json:"storageType"`
		Size        int64     `json:"size"`
		CreatedAt   time.Time `json:"createdAt"`
	} `json:"list"`
	Total int64 `json:"total"`
}

func (h *HostClient) SiteBackupHistory(ctx context.Context, siteID string, page, size int) (SiteBackupPage, error) {
	query := url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(size)}}
	var out SiteBackupPage
	err := h.call(ctx, http.MethodGet, "/sites/"+url.PathEscape(siteID)+"/backups?"+query.Encode(), nil, &out)
	return out, err
}

func (h *HostClient) VerifySiteBackup(ctx context.Context, siteID, backupID string) (string, error) {
	return h.submitTask(ctx, "/sites/"+url.PathEscape(siteID)+"/backups/"+url.PathEscape(backupID)+"/verify", nil)
}

func (h *HostClient) RestoreSiteBackup(ctx context.Context, siteID, backupID, confirmName string) (string, error) {
	return h.submitTask(ctx, "/sites/"+url.PathEscape(siteID)+"/backups/"+url.PathEscape(backupID)+"/restore", map[string]string{"confirmName": confirmName})
}

func (h *HostClient) BindSiteCertificate(ctx context.Context, siteID, certificateID string, forceHTTPS bool) (string, error) {
	return h.submitTask(ctx, "/sites/"+url.PathEscape(siteID)+"/certificate", map[string]any{"certificateId": certificateID, "forceHttps": forceHTTPS})
}

func (h *HostClient) RenewCertificate(ctx context.Context, id string) (string, error) {
	return h.submitTask(ctx, "/certificates/"+url.PathEscape(id)+"/renew", nil)
}

func (h *HostClient) Task(ctx context.Context, id string) (TaskSummary, error) {
	var out TaskSummary
	err := h.call(ctx, http.MethodGet, "/tasks/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (h *HostClient) JobProgress(ctx context.Context, taskID string, percent int, message string) error {
	return h.call(ctx, http.MethodPost, "/tasks/"+url.PathEscape(taskID)+"/progress", map[string]any{"percent": percent, "message": message}, nil)
}

type LogTail struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

func (h *HostClient) SiteLogs(ctx context.Context, id string, options url.Values) (LogTail, error) {
	var out LogTail
	err := h.call(ctx, http.MethodGet, "/sites/"+url.PathEscape(id)+"/logs?"+options.Encode(), nil, &out)
	return out, err
}

func (h *HostClient) ContainerLogs(ctx context.Context, id string, options url.Values) (LogTail, error) {
	var out LogTail
	err := h.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs?"+options.Encode(), nil, &out)
	return out, err
}

func (h *HostClient) ContainerStats(ctx context.Context, id string, out any) error {
	return h.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/stats", nil, out)
}

func (h *HostClient) RestartSite(ctx context.Context, id string) (string, error) {
	return h.submitTask(ctx, "/sites/"+url.PathEscape(id)+"/restart", nil)
}

type SiteDetail struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ProjectType    string `json:"projectType"`
	Enabled        bool   `json:"enabled"`
	RuntimeVersion string `json:"runtimeVersion"`
	RuntimePort    int    `json:"runtimePort"`
	RuntimePending bool   `json:"runtimePending"`
	ProxyRoutes    []struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Enabled bool   `json:"enabled"`
	} `json:"proxyRoutes"`
}

func (h *HostClient) SiteDetail(ctx context.Context, id string) (SiteDetail, error) {
	var out SiteDetail
	err := h.call(ctx, http.MethodGet, "/sites/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (h *HostClient) ControlSite(ctx context.Context, id, action string) (string, error) {
	return h.submitTask(ctx, "/sites/"+url.PathEscape(id)+"/control", map[string]string{"action": action})
}

func (h *HostClient) ControlContainer(ctx context.Context, id, action string) (string, error) {
	return h.submitTask(ctx, "/containers/"+url.PathEscape(id)+"/control", map[string]string{"action": action})
}

func (h *HostClient) DatabaseNames(ctx context.Context, serverID string) ([]string, error) {
	var out []string
	err := h.call(ctx, http.MethodGet, "/databases/"+url.PathEscape(serverID)+"/names", nil, &out)
	return out, err
}

func (h *HostClient) CreateDatabaseBackup(ctx context.Context, serverID, name string) (string, error) {
	return h.submitTask(ctx, "/databases/"+url.PathEscape(serverID)+"/backup", map[string]string{"name": name})
}

type DatabaseBackupPage struct {
	List []struct {
		ID        string    `json:"id"`
		Database  string    `json:"database"`
		FileName  string    `json:"fileName"`
		Size      int64     `json:"size"`
		CreatedAt time.Time `json:"createdAt"`
	} `json:"list"`
	Total int64 `json:"total"`
}

func (h *HostClient) DatabaseBackups(ctx context.Context, serverID string, page, size int) (DatabaseBackupPage, error) {
	query := url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(size)}}
	var out DatabaseBackupPage
	err := h.call(ctx, http.MethodGet, "/databases/"+url.PathEscape(serverID)+"/backups?"+query.Encode(), nil, &out)
	return out, err
}

func (h *HostClient) RestoreDatabaseBackup(ctx context.Context, serverID, backupID, confirmDatabase string) (string, error) {
	return h.submitTask(ctx, "/databases/"+url.PathEscape(serverID)+"/restore", map[string]string{"backupId": backupID, "confirmDatabase": confirmDatabase})
}

func (h *HostClient) submitTask(ctx context.Context, path string, data any) (string, error) {
	var out struct {
		TaskID string `json:"taskId"`
	}
	err := h.call(ctx, http.MethodPost, path, data, &out)
	return out.TaskID, err
}

// OnJob 注册一个处理器，通过 JobID 分发清单中的任务。返回的消息会写入任务日志。
func (p *Plugin) OnJob(run func(context.Context, JobInvocation) (string, error)) error {
	return p.HandleFunc("/api/_panel/job", callbackHandler(32<<10, func(ctx context.Context, job JobInvocation) (any, error) {
		message, err := run(ctx, job)
		return map[string]string{"message": message}, err
	}))
}

func (p *Plugin) OnEvent(run func(context.Context, Event) error) error {
	return p.HandleFunc("/api/_panel/event", callbackHandler(8192, func(ctx context.Context, event Event) (any, error) {
		return nil, run(ctx, event)
	}))
}

func callbackHandler[T any](limit int64, run func(context.Context, T) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Panel-User") != "system" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var input T
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(&input); err != nil {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		result, err := run(r.Context(), input)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			result = map[string]string{"message": err.Error()}
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(result)
	}
}
