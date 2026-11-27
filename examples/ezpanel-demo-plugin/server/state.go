package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/mrlihx/ezpanel/sdk/go/panelsdk"
)

type JobRunRecord struct {
	JobID   string            `json:"jobId"`
	TaskID  string            `json:"taskId"`
	Trigger string            `json:"trigger"`
	Step    string            `json:"step,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
	Summary string            `json:"summary"`
	Time    time.Time         `json:"time"`
}

type EventRecord struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	SiteID     string    `json:"siteId,omitempty"`
	ResourceID string    `json:"resourceId,omitempty"`
	Action     string    `json:"action,omitempty"`
	TaskID     string    `json:"taskId"`
	Time       time.Time `json:"time"`
	ReceivedAt time.Time `json:"receivedAt"`
}

type NoticeRecord struct {
	ChannelID string    `json:"channelId"`
	Target    string    `json:"target"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Time      time.Time `json:"time"`
}

type DNSOpRecord struct {
	Action string    `json:"action"`
	Name   string    `json:"name"`
	Value  string    `json:"value"`
	Time   time.Time `json:"time"`
}

type DeployHookRecord struct {
	Project string    `json:"project"`
	Phase   string    `json:"phase"`
	Status  string    `json:"status"`
	TaskID  string    `json:"taskId"`
	Blocked bool      `json:"blocked"`
	Time    time.Time `json:"time"`
}

type StorageOpRecord struct {
	Operation string    `json:"operation"`
	ChannelID string    `json:"channelId"`
	Folder    string    `json:"folder"`
	Key       string    `json:"key,omitempty"`
	Size      int64     `json:"size,omitempty"`
	SHA256    string    `json:"sha256,omitempty"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
	Time      time.Time `json:"time"`
}

type ServiceCallRecord struct {
	Caller string    `json:"caller"`
	Name   string    `json:"name"`
	Input  string    `json:"input"`
	Time   time.Time `json:"time"`
}

type StateSnapshot struct {
	JobRuns             []JobRunRecord      `json:"jobRuns"`
	Events              []EventRecord       `json:"events"`
	EventDedup          map[string]bool     `json:"eventDedup,omitempty"`
	Notices             []NoticeRecord      `json:"notices"`
	DNSRecords          map[string][]string `json:"dnsRecords"`
	DNSOps              []DNSOpRecord       `json:"dnsOps"`
	DeployHooks         []DeployHookRecord  `json:"deployHooks"`
	BlockNextBeforeHook bool                `json:"blockNextBeforeHook"`
	StorageOps          []StorageOpRecord   `json:"storageOps"`
	ServiceCalls        []ServiceCallRecord `json:"serviceCalls"`
	ProbeHealthy        bool                `json:"probeHealthy"`
	ProbeMessage        string              `json:"probeMessage"`
}

// StateStore 在插件私有目录（PANEL_PLUGIN_DATA/state.json）中持久化演示运行状态。
// 采用读写锁 + 临时文件原子 Rename 写入，确保版本快照、升级与异常重启时状态完整。
type StateStore struct {
	mu       sync.RWMutex
	filePath string
	limit    int
	data     StateSnapshot
}

func NewStateStore(dataDir string, limit int) (*StateStore, error) {
	if limit < 5 || limit > 200 {
		limit = 50
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	s := &StateStore{
		filePath: filepath.Join(dataDir, "state.json"),
		limit:    limit,
		data: StateSnapshot{
			DNSRecords:   map[string][]string{},
			EventDedup:   map[string]bool{},
			ProbeHealthy: true,
			ProbeMessage: "模拟探针运行正常",
		},
	}
	raw, err := os.ReadFile(s.filePath)
	if err == nil {
		_ = json.Unmarshal(raw, &s.data)
		if s.data.DNSRecords == nil {
			s.data.DNSRecords = map[string][]string{}
		}
		if s.data.EventDedup == nil {
			s.data.EventDedup = map[string]bool{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

func (s *StateStore) saveLocked() error {
	payload, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, payload, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

func trimHead[T any](items []T, limit int) []T {
	if len(items) <= limit {
		return items
	}
	return append([]T(nil), items[len(items)-limit:]...)
}

func (s *StateStore) Snapshot() StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := StateSnapshot{
		JobRuns:             slices.Clone(s.data.JobRuns),
		Events:              slices.Clone(s.data.Events),
		Notices:             slices.Clone(s.data.Notices),
		DNSRecords:          make(map[string][]string, len(s.data.DNSRecords)),
		DNSOps:              slices.Clone(s.data.DNSOps),
		DeployHooks:         slices.Clone(s.data.DeployHooks),
		BlockNextBeforeHook: s.data.BlockNextBeforeHook,
		StorageOps:          slices.Clone(s.data.StorageOps),
		ServiceCalls:        slices.Clone(s.data.ServiceCalls),
		ProbeHealthy:        s.data.ProbeHealthy,
		ProbeMessage:        s.data.ProbeMessage,
	}
	for k, v := range s.data.DNSRecords {
		out.DNSRecords[k] = slices.Clone(v)
	}
	return out
}

func (s *StateStore) AddJobRun(rec JobRunRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.JobRuns = trimHead(append(s.data.JobRuns, rec), s.limit)
	_ = s.saveLocked()
}

// AddEvent records an event once by event.ID and returns true if it was newly recorded.
func (s *StateStore) AddEvent(ev panelsdk.Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.ID != "" && s.data.EventDedup[ev.ID] {
		return false
	}
	if ev.ID != "" {
		s.data.EventDedup[ev.ID] = true
		if len(s.data.EventDedup) > s.limit*4 {
			// 仅保留最近事件集合防止映射无限增长
			keep := make(map[string]bool, len(s.data.Events)+1)
			for _, item := range s.data.Events {
				if item.ID != "" {
					keep[item.ID] = true
				}
			}
			keep[ev.ID] = true
			s.data.EventDedup = keep
		}
	}
	s.data.Events = trimHead(append(s.data.Events, EventRecord{
		ID:         ev.ID,
		Name:       ev.Name,
		Status:     ev.Status,
		SiteID:     ev.SiteID,
		ResourceID: ev.ResourceID,
		Action:     ev.Action,
		TaskID:     ev.TaskID,
		Time:       ev.Time,
		ReceivedAt: time.Now().UTC(),
	}), s.limit)
	_ = s.saveLocked()
	return true
}

func (s *StateStore) AddNotice(notice panelsdk.ProviderNotice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Notices = trimHead(append(s.data.Notices, NoticeRecord{
		ChannelID: notice.ChannelID,
		Target:    notice.Target,
		Title:     notice.Title,
		Message:   notice.Message,
		Time:      time.Now().UTC(),
	}), s.limit)
	_ = s.saveLocked()
}

func (s *StateStore) ClearNotices() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Notices = nil
	_ = s.saveLocked()
}

func (s *StateStore) ApplyDNS(change panelsdk.DNSChange) error {
	if change.Name == "" || change.Value == "" {
		return errors.New("DNS 记录名称与值不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values := s.data.DNSRecords[change.Name]
	switch change.Action {
	case "present":
		if !slices.Contains(values, change.Value) {
			s.data.DNSRecords[change.Name] = append(values, change.Value)
		}
	case "remove":
		filtered := make([]string, 0, len(values))
		for _, v := range values {
			if v != change.Value {
				filtered = append(filtered, v)
			}
		}
		if len(filtered) == 0 {
			delete(s.data.DNSRecords, change.Name)
		} else {
			s.data.DNSRecords[change.Name] = filtered
		}
	default:
		return errors.New("不支持的 DNS 操作：" + change.Action)
	}
	s.data.DNSOps = trimHead(append(s.data.DNSOps, DNSOpRecord{
		Action: change.Action,
		Name:   change.Name,
		Value:  change.Value,
		Time:   time.Now().UTC(),
	}), s.limit)
	return s.saveLocked()
}

func (s *StateStore) RecordDeploymentHook(hook panelsdk.DeploymentHook) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	blocked := false
	if hook.Phase == "before" && s.data.BlockNextBeforeHook {
		blocked = true
		s.data.BlockNextBeforeHook = false
	}
	s.data.DeployHooks = trimHead(append(s.data.DeployHooks, DeployHookRecord{
		Project: hook.Project,
		Phase:   hook.Phase,
		Status:  hook.Status,
		TaskID:  hook.TaskID,
		Blocked: blocked,
		Time:    time.Now().UTC(),
	}), s.limit)
	_ = s.saveLocked()
	return blocked
}

func (s *StateStore) SetBlockNextBeforeHook(block bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.BlockNextBeforeHook = block
	_ = s.saveLocked()
}

func (s *StateStore) AddStorageOp(rec StorageOpRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.StorageOps = trimHead(append(s.data.StorageOps, rec), s.limit)
	_ = s.saveLocked()
}

func (s *StateStore) AddServiceCall(caller, name, input string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(input) > 512 {
		input = input[:512] + "..."
	}
	s.data.ServiceCalls = trimHead(append(s.data.ServiceCalls, ServiceCallRecord{
		Caller: caller,
		Name:   name,
		Input:  input,
		Time:   time.Now().UTC(),
	}), s.limit)
	_ = s.saveLocked()
}

func (s *StateStore) SetProbe(healthy bool, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.ProbeHealthy = healthy
	if message == "" {
		if healthy {
			message = "模拟探针运行正常"
		} else {
			message = "模拟探针处于异常告警状态"
		}
	}
	s.data.ProbeMessage = message
	_ = s.saveLocked()
}

func (s *StateStore) Probe() (bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.ProbeHealthy, s.data.ProbeMessage
}

type SecretSummary struct {
	Configured  bool   `json:"configured"`
	ByteLength  int    `json:"byteLength"`
	Fingerprint string `json:"fingerprint"`
}

// SummarizeSecret 计算秘密配置项的脱敏摘要（仅返回是否已配置、字节长度与 SHA-256 前 12 位指纹）。
// 插件不得在日志、状态文件或前端接口中输出任何密钥、Token 或外部凭据的明文。
func SummarizeSecret(value string) SecretSummary {
	if value == "" {
		return SecretSummary{}
	}
	sum := sha256.Sum256([]byte(value))
	return SecretSummary{
		Configured:  true,
		ByteLength:  len(value),
		Fingerprint: hex.EncodeToString(sum[:])[:12],
	}
}
