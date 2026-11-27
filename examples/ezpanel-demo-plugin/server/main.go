// Package main 实现 EzPanel演示插件 的服务端。
//
// 本服务基于官方 github.com/mrlihx/ezpanel/sdk/go/panelsdk 构建，完整演示：
// 1. 环境变量与敏感配置解析（严禁向日志或接口回显秘密明文）；
// 2. 全部 8 个面板回调协议（OnJob / OnEvent / OnBackupStorage / OnNotification /
//    OnDNSChange / OnDeployment / OnService / OnMonitorCheck）；
// 3. 全部 Host v1 代理接口封装（核心概览、KV、系统与监控、六大资源、受控文件系统、安全与审计）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mrlihx/ezpanel/sdk/go/panelsdk"
)

// loadPluginConfig 从面板注入的环境变量加载配置。
// 注意：password/secret 类型字段仅通过 SummarizeSecret 生成脱敏摘要，绝不打印或返回明文。
func loadPluginConfig() (pluginConfig, error) {
	limitStr := os.Getenv("HISTORY_LIMIT")
	if limitStr == "" {
		limitStr = "50"
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 5 || limit > 200 {
		return pluginConfig{}, errors.New("HISTORY_LIMIT 须为 5 至 200 的整数")
	}
	mode := os.Getenv("INSPECT_MODE")
	if mode == "" {
		mode = "standard"
	}
	greeting := os.Getenv("GREETING")
	if greeting == "" {
		greeting = "你好，欢迎体验 EzPanel演示插件"
	}
	return pluginConfig{
		Greeting:      greeting,
		HistoryLimit:  limit,
		AutoNotify:    os.Getenv("AUTO_NOTIFY") == "true",
		InspectMode:   mode,
		SampleFile:    os.Getenv("SAMPLE_FILE"),
		WorkDir:       os.Getenv("WORK_DIR"),
		WebhookSecret: SummarizeSecret(os.Getenv("WEBHOOK_SECRET")),
		DemoToken:     SummarizeSecret(os.Getenv("DEMO_TOKEN")),
	}, nil
}

func executeService(caller, name string, raw []byte, state *StateStore) (any, error) {
	state.AddServiceCall(caller, name, string(raw))
	switch name {
	case "echo-service":
		var payload any
		if len(raw) > 0 && json.Unmarshal(raw, &payload) != nil {
			payload = string(raw)
		}
		return map[string]any{
			"service":   "echo-service",
			"caller":    caller,
			"echo":      payload,
			"timestamp": time.Now().UTC(),
		}, nil
	case "summary-service":
		snap := state.Snapshot()
		return map[string]any{
			"service":      "summary-service",
			"caller":       caller,
			"jobRuns":      len(snap.JobRuns),
			"events":       len(snap.Events),
			"notices":      len(snap.Notices),
			"dnsKeys":      len(snap.DNSRecords),
			"probeHealthy": snap.ProbeHealthy,
		}, nil
	default:
		return nil, errors.New("未知插件服务：" + name)
	}
}

// registerCallbacks 注册面板反向调用插件的 8 类扩展回调（由 panelsdk 自动校验 X-Panel-User: system 身份）。
func registerCallbacks(p *panelsdk.Plugin, cfg pluginConfig, state *StateStore, storage *DemoBackupStorage) error {
	// 1. 定时任务 / 手动任务 / Webhook / 多步骤顺序工作流回调（单步上限 2 分钟，全流程上限 10 分钟）
	if err := p.OnJob(func(ctx context.Context, job panelsdk.JobInvocation) (string, error) {
		switch job.JobID {
		case "quick-inspect":
			label := job.Params["TARGET_LABEL"]
			count, _ := strconv.Atoi(job.Params["SAMPLE_COUNT"])
			if count < 1 {
				count = 1
			}
			_ = p.Host.JobProgress(ctx, job.TaskID, 25, fmt.Sprintf("开始快速巡检：%s（模式 %s）", label, cfg.InspectMode))
			sites, _ := p.Host.Sites(ctx)
			_ = p.Host.JobProgress(ctx, job.TaskID, 70, fmt.Sprintf("已扫描授权站点 %d 个，采样次数 %d", len(sites), count))
			summary := fmt.Sprintf("快速巡检完成：label=%s, sites=%d, level=%s, trigger=%s", label, len(sites), job.Params["LEVEL"], job.Trigger)
			if job.Params["SEND_NOTICE"] == "true" || cfg.AutoNotify {
				if nErr := p.Host.Notify(ctx, "快速巡检完成", summary); nErr != nil {
					_ = p.Host.JobProgress(ctx, job.TaskID, 90, "通知未发送："+nErr.Error())
				} else {
					_ = p.Host.JobProgress(ctx, job.TaskID, 90, "已通过面板渠道发送汇总通知")
				}
			}
			state.AddJobRun(JobRunRecord{
				JobID:   job.JobID,
				TaskID:  job.TaskID,
				Trigger: job.Trigger,
				Params:  job.Params,
				Summary: summary,
				Time:    time.Now().UTC(),
			})
			_ = p.Host.JobProgress(ctx, job.TaskID, 100, summary)
			return summary, nil

		case "staged-workflow":
			note := job.Params["NOTE"]
			var msg string
			switch job.Step {
			case "check-env":
				_ = p.Host.JobProgress(ctx, job.TaskID, 30, "步骤 check-env：检查插件配置与 Host 连通性")
				if _, err := p.Host.Info(ctx); err != nil {
					return "", err
				}
				msg = "环境校验通过，模式=" + cfg.InspectMode
			case "collect-metrics":
				_ = p.Host.JobProgress(ctx, job.TaskID, 65, "步骤 collect-metrics：读取服务器系统概览")
				overview, err := p.Host.Sysinfo(ctx)
				if err != nil {
					return "", err
				}
				msg = fmt.Sprintf("主机 %s（%s）负载采样完成", overview.Hostname, overview.OS)
			case "write-report":
				_ = p.Host.JobProgress(ctx, job.TaskID, 95, "步骤 write-report：写入 KV 摘要并保存执行记录")
				msg = fmt.Sprintf("工作流完成（trigger=%s, note=%s）", job.Trigger, note)
				_ = p.Host.KVSet(ctx, "last-workflow-report", msg)
				_ = p.Host.JobProgress(ctx, job.TaskID, 100, msg)
			default:
				return "", errors.New("未知工作流步骤：" + job.Step)
			}
			state.AddJobRun(JobRunRecord{
				JobID:   job.JobID,
				TaskID:  job.TaskID,
				Trigger: job.Trigger,
				Step:    job.Step,
				Params:  job.Params,
				Summary: msg,
				Time:    time.Now().UTC(),
			})
			return msg, nil

		default:
			return "", errors.New("未声明的定时任务：" + job.JobID)
		}
	}); err != nil {
		return err
	}

	// 2. 面板事件订阅回调（退避重试期间后续事件可能先行到达，务必按 event.ID 幂等去重）
	if err := p.OnEvent(func(ctx context.Context, event panelsdk.Event) error {
		added := state.AddEvent(event)
		if !added {
			return nil
		}
		return p.Host.Log(ctx, "info", fmt.Sprintf("收到订阅事件 %s（status=%s, resource=%s, task=%s）", event.Name, event.Status, event.ResourceID, event.TaskID))
	}); err != nil {
		return err
	}

	// 3. 备份存储渠道回调（实现 Test / Upload / Open / Delete 流式接口）
	if err := p.OnBackupStorage(storage); err != nil {
		return err
	}

	// 4. 告警通知渠道回调（最长等待 15 秒，发送结果记入面板通知记录）
	if err := p.OnNotification(func(ctx context.Context, notice panelsdk.ProviderNotice) error {
		state.AddNotice(notice)
		return p.Host.Log(ctx, "info", fmt.Sprintf("通知收件箱收到来自渠道 %s 发往 %s 的消息：%s", notice.ChannelID, notice.Target, notice.Title))
	}); err != nil {
		return err
	}

	// 5. ACME DNS TXT 记录管理回调（present 追加且保持幂等，remove 仅移除对应值并保留同名其他 TXT 值）
	if err := p.OnDNSChange(func(ctx context.Context, change panelsdk.DNSChange) error {
		if err := state.ApplyDNS(change); err != nil {
			return err
		}
		return p.Host.Log(ctx, "info", fmt.Sprintf("演示 DNS 记录变更：%s %s", change.Action, change.Name))
	}); err != nil {
		return err
	}

	// 6. Compose 部署前置/后置钩子回调（before 返回错误将中止实际部署；每个回调上限 10 秒）
	if err := p.OnDeployment(func(ctx context.Context, hook panelsdk.DeploymentHook) error {
		blocked := state.RecordDeploymentHook(hook)
		if blocked {
			_ = p.Host.Log(ctx, "warn", fmt.Sprintf("Compose %s 前置钩子触发模拟拦截（task=%s）", hook.Project, hook.TaskID))
			return errors.New("EzPanel演示插件模拟前置部署钩子拦截")
		}
		_ = p.Host.Log(ctx, "info", fmt.Sprintf("Compose %s 部署钩子：%s（status=%s）", hook.Project, hook.Phase, hook.Status))
		return nil
	}); err != nil {
		return err
	}

	// 7. 插件间服务提供方回调（可按 call.Caller 校验调用方插件 ID，超时 20 秒）
	if err := p.OnService(func(_ context.Context, call panelsdk.ServiceCall) (any, error) {
		if strings.TrimSpace(call.Caller) == "" {
			return nil, errors.New("缺少调用方插件标识")
		}
		return executeService(call.Caller, call.Name, call.Data, state)
	}); err != nil {
		return err
	}

	// 8. 自定义监控检测探针回调（需在 10 秒内返回 CheckResult；Healthy=false 时触发面板告警规则）
	if err := p.OnMonitorCheck(func(_ context.Context, checkID string) (panelsdk.CheckResult, error) {
		switch checkID {
		case "runtime-health":
			return panelsdk.CheckResult{
				Healthy: true,
				Message: "ezpanel-demo-plugin 运行正常",
			}, nil
		case "custom-probe":
			healthy, msg := state.Probe()
			return panelsdk.CheckResult{
				Healthy: healthy,
				Message: msg,
			}, nil
		default:
			return panelsdk.CheckResult{}, errors.New("未知检测项：" + checkID)
		}
	}); err != nil {
		return err
	}

	return nil
}

func main() {
	// 校验 PANEL_PLUGIN_ID / PORT / DATA / HOST_URL / PLUGIN_TOKEN / PROXY_TOKEN 等启动环境
	p, err := panelsdk.New()
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := loadPluginConfig()
	if err != nil {
		log.Fatal(err)
	}
	// 注意：插件启动完成（/health 返回 200 进入 running）前仅 Host.Info 可用，
	// 因此这里仅初始化本地私有目录状态，不提前调用其他 Host API。
	state, err := NewStateStore(p.DataDir, cfg.HistoryLimit)
	if err != nil {
		log.Fatal(err)
	}
	storage := NewDemoBackupStorage(p.DataDir, state)

	if err := registerCallbacks(p, cfg, state, storage); err != nil {
		log.Fatal(err)
	}
	if err := registerCoreRoutes(p, cfg, state); err != nil {
		log.Fatal(err)
	}
	if err := registerResourceRoutes(p); err != nil {
		log.Fatal(err)
	}
	if err := registerFileRoutes(p); err != nil {
		log.Fatal(err)
	}
	if err := registerExtensionRoutes(p, state, storage); err != nil {
		log.Fatal(err)
	}

	// 启动回环监听、代理 Token 鉴权、健康检查与优雅退出循环
	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
