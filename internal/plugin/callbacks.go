package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"ezpanel/internal/store"
	"ezpanel/internal/task"
)

const jobCallback = "_panel/job"
const eventCallback = "_panel/event"

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

func (m *Module) readyPlugin(id string) (*running, Manifest, error) {
	var p store.Plugin
	var manifest Manifest
	if err := m.db.Select("manifest").Take(&p, "id = ? AND enabled = ? AND operation IS NULL", id, true).Error; err != nil {
		return nil, manifest, errors.New("插件未启用")
	}
	if err := json.Unmarshal([]byte(p.Manifest), &manifest); err != nil {
		return nil, manifest, errors.New("插件清单无效")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.runs[id]
	if m.statuses[id] != "running" || run == nil {
		return nil, manifest, errors.New("插件未运行")
	}
	return run, manifest, nil
}

type callbackReply struct {
	Message string
	Data    json.RawMessage
}

func invokePlugin(ctx context.Context, run *running, id, route string, input any) (callbackReply, error) {
	var result callbackReply
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(run.ctx, cancel)
	defer stop()
	body, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/%s", run.port, route), bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Panel-Plugin-Id", id)
	req.Header.Set("X-Panel-Proxy-Token", run.proxy)
	req.Header.Set("X-Panel-User", "system")
	client := &http.Client{Transport: sharedPluginProxyTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return result, errors.New("插件回调中断或超时")
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("插件回调返回 HTTP %d", resp.StatusCode)
	}
	return result, nil
}

func (m *Module) submitJob(id, jobID, user, ip, expression, trigger string, params map[string]string) (string, error) {
	run, manifest, err := m.readyPlugin(id)
	if err != nil {
		return "", err
	}
	idx := slices.IndexFunc(manifest.Jobs, func(job Job) bool { return job.ID == jobID })
	if idx < 0 {
		return "", errors.New("插件未声明此任务")
	}
	if err := m.checkPermission(id, manifest, "jobs.run"); err != nil {
		return "", err
	}
	if trigger == "webhook" {
		if err := m.checkPermission(id, manifest, "jobs.webhook"); err != nil {
			return "", err
		}
		if !manifest.Jobs[idx].Webhook {
			return "", errors.New("任务未启用 Webhook")
		}
	}
	values := make(map[string]string, len(manifest.Jobs[idx].Params))
	for _, field := range manifest.Jobs[idx].Params {
		value, hasValue := params[field.Key]
		if !hasValue {
			value = field.Default
		}
		if len(value) > 512 || strings.ContainsRune(value, 0) || field.Required && value == "" || validateEnvValue(field, value) != nil {
			return "", errors.New("任务参数无效：" + field.Label)
		}
		values[field.Key] = value
	}
	for key := range params {
		if _, ok := values[key]; !ok {
			return "", errors.New("包含未声明的任务参数")
		}
	}
	if expression != "" {
		grant, err := m.grant(id)
		if err != nil || grant.Schedules[jobID] != expression {
			return "", errors.New("计划已更改")
		}
	}
	return m.tasks.Submit(task.Meta{Module: "plugin:" + id, Action: "job", Target: manifest.Jobs[idx].Title, ResourceID: jobID, Username: user, IP: ip, LockKey: "plugin-job:" + id + ":" + jobID}, func(ctx context.Context, log *task.Log) (any, error) {
		steps := manifest.Jobs[idx].Steps
		timeout := 10 * time.Minute
		if len(steps) == 0 {
			timeout = 2 * time.Minute
			steps = []string{""}
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		log.Secrets(run.log.secretValues()...)
		log.Line("执行插件任务 " + jobID)
		m.mu.Lock()
		if run.jobLogs == nil {
			run.jobLogs = map[string]*task.Log{}
		}
		run.jobLogs[log.TaskID()] = log
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			delete(run.jobLogs, log.TaskID())
			m.mu.Unlock()
		}()
		for _, step := range steps {
			if step != "" {
				log.Line("执行步骤 " + step)
			}
			stepCtx, done := context.WithTimeout(ctx, 2*time.Minute)
			reply, err := invokePlugin(stepCtx, run, id, jobCallback, map[string]any{"jobId": jobID, "taskId": log.TaskID(), "trigger": trigger, "params": values, "step": step})
			done()
			if strings.TrimSpace(reply.Message) != "" {
				log.Line(redact(reply.Message, run.log.secretValues()))
			}
			if err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

func (m *Module) restoreSchedules(p store.Plugin) {
	var manifest Manifest
	grant, err := m.grant(p.ID)
	if err == nil && json.Unmarshal([]byte(p.Manifest), &manifest) == nil {
		m.replaceSchedules(p.ID, manifest, grant)
	}
}

func (m *Module) replaceSchedules(id string, manifest Manifest, grant capabilityGrant) {
	m.scheduleMu.Lock()
	defer m.scheduleMu.Unlock()
	for key, stop := range m.schedules {
		if strings.HasPrefix(key, id+":") {
			stop()
			delete(m.schedules, key)
		}
	}
	select {
	case <-m.closed:
		return
	default:
	}
	if grant.permissionDisabled("jobs.run") {
		return
	}
	for _, job := range manifest.Jobs {
		expression := grant.Schedules[job.ID]
		if expression == "" {
			continue
		}
		stop, err := m.services.Cron.AddPluginSchedule(expression, func() { _, _ = m.submitJob(id, job.ID, "system", "", expression, "schedule", nil) })
		if err != nil {
			slog.Warn("无法加载插件计划", "plugin", id, "job", job.ID, "error", err)
			continue
		}
		m.schedules[id+":"+job.ID] = stop
	}
}
