import { useEffect, useState } from "react";
import { Alert, Button, Card, Input, Space, Steps, Table, Tag, Typography } from "antd";
import type { PanelSDK } from "../panel-sdk";
import { formatAPIError, postJSON } from "../api";

interface JobRun {
  jobId: string;
  taskId: string;
  trigger: string;
  step?: string;
  summary: string;
  time: string;
}

interface EventRow {
  id: string;
  name: string;
  status: string;
  siteId?: string;
  resourceId?: string;
  taskId: string;
  receivedAt: string;
}

interface ExtensionStateResponse {
  state: {
    jobRuns: JobRun[];
    events: EventRow[];
  };
}

/**
 * 任务与事件视图：
 * 展示 OnJob（带参数任务 + 多步骤工作流）、Webhook 触发签名模板、
 * OnEvent（5 类面板事件订阅与幂等去重）、以及 Host.Notify 通知发送。
 */
export default function AutomationView({ sdk }: { sdk: PanelSDK }) {
  const [jobs, setJobs] = useState<JobRun[]>([]);
  const [events, setEvents] = useState<EventRow[]>([]);
  const [title, setTitle] = useState("能力 Demo 告警测试");
  const [message, setMessage] = useState("这是一条通过 Host.Notify 发送的插件测试通知。");
  const [error, setError] = useState("");
  const [refreshSeq, setRefreshSeq] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setError("");
    void sdk
      .api<ExtensionStateResponse>("/extensions/state", { signal: controller.signal })
      .then((res) => {
        setJobs(res.state?.jobRuns || []);
        setEvents(res.state?.events || []);
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(formatAPIError(err));
      });
    return () => controller.abort();
  }, [sdk, refreshSeq]);

  const webhookCurl = `TS=$(date +%s)
PATH_STR="/plugin-webhooks/${sdk.pluginId}/staged-workflow"
BODY='{"params":{"NOTE":"ci-deploy"}}'
SIG=$(printf '%s\\n%s\\n%s' "$PATH_STR" "$TS" "$BODY" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" -hex | awk '{print $NF}')
curl -X POST "https://YOUR_PANEL$PATH_STR" \\
  -H "Content-Type: application/json" \\
  -H "X-Panel-Request: 1" \\
  -H "X-Ezpanel-Timestamp: $TS" \\
  -H "X-Ezpanel-Signature: $SIG" \\
  -d "$BODY"`;

  const copyCurl = async () => {
    try {
      await navigator.clipboard.writeText(webhookCurl);
      sdk.message.success("已复制 Webhook 签名请求示例");
    } catch {
      sdk.message.info("当前隔离上下文无法访问剪贴板，请直接手动复制下方代码块");
    }
  };

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      {error && <Alert type="error" showIcon message={error} />}

      <Card size="small" title="任务类型与多步骤顺序工作流说明（OnJob）">
        <Space direction="vertical" size="small" style={{ width: "100%" }}>
          <Typography.Text>
            1. <Tag color="blue">quick-inspect</Tag> 参数化巡检任务：声明 4 个非秘密字段（text / number /
            boolean / select），可在「应用商店 → 插件设置 → 授权」手动传参运行或配置 Cron 定时执行。
          </Typography.Text>
          <Typography.Text>
            2. <Tag color="purple">staged-workflow</Tag> 多步骤工作流（支持 Webhook）：按顺序执行以下 3
            个步骤（单步超时 2 分钟，全流程 10 分钟，共用同一个 taskId）：
          </Typography.Text>
          <Steps
            size="small"
            current={2}
            items={[
              { title: "check-env", description: "校验配置与 Host 连通性" },
              { title: "collect-metrics", description: "采集 Host.Sysinfo" },
              { title: "write-report", description: "写入 KV 并归档记录" },
            ]}
          />
        </Space>
      </Card>

      <div className="cs-grid">
        <Card size="small" title="发送面板通知（Host.Notify）">
          <Space direction="vertical" style={{ width: "100%" }}>
            <Input
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              addonBefore="标题"
              maxLength={120}
            />
            <Input.TextArea
              rows={3}
              value={message}
              onChange={(e) => setMessage(e.target.value)}
            />
            <Space wrap>
              <Button
                type="primary"
                onClick={() =>
                  void postJSON(sdk, "/extensions/notify", { title, message })
                    .then(() => sdk.message.success("已发送面板通知"))
                    .catch((e) => sdk.message.error(formatAPIError(e)))
                }
              >
                发送通知
              </Button>
              <Typography.Text className="cs-muted">
                限流：每插件每分钟 1 次；需在「应用商店 → 插件设置 → 授权」选择启用通知渠道。
              </Typography.Text>
            </Space>
          </Space>
        </Card>

        <Card
          size="small"
          title="Webhook 触发签名示例（jobs.webhook）"
          extra={
            <Button size="small" onClick={() => void copyCurl()}>
              复制命令
            </Button>
          }
        >
          <Space direction="vertical" style={{ width: "100%" }}>
            <Typography.Text className="cs-muted">
              路径：<Typography.Text code>/plugin-webhooks/{sdk.pluginId}/staged-workflow</Typography.Text>
              （时间误差 ≤ 5 分钟，重复签名自动拒绝）
            </Typography.Text>
            <pre className="cs-pre">{webhookCurl}</pre>
          </Space>
        </Card>
      </div>

      <Card
        size="small"
        title={`任务执行记录（OnJob · 单步与多步骤工作流：${jobs.length} 条）`}
        extra={
          <Button size="small" onClick={() => setRefreshSeq((n) => n + 1)}>
            刷新
          </Button>
        }
      >
        <Table
          size="small"
          rowKey={(r, i) => `${r.taskId}-${r.step || ""}-${i}`}
          pagination={{ pageSize: 6 }}
          scroll={{ x: "max-content" }}
          dataSource={[...jobs].reverse()}
          columns={[
            { title: "任务 ID", dataIndex: "jobId", width: 140 },
            {
              title: "触发方式",
              dataIndex: "trigger",
              width: 100,
              render: (v: string) => <Tag color="blue">{v}</Tag>,
            },
            { title: "步骤", dataIndex: "step", width: 130, render: (v?: string) => v || "-" },
            { title: "执行摘要", dataIndex: "summary", ellipsis: true },
            {
              title: "面板日志",
              width: 110,
              render: (_, row) => (
                <Button size="small" onClick={() => sdk.showTask?.(row.taskId)}>
                  查看任务
                </Button>
              ),
            },
          ]}
        />
      </Card>

      <Card size="small" title={`已订阅事件投递历史（OnEvent · 按事件 ID 幂等去重：${events.length} 条）`}>
        <Table
          size="small"
          rowKey="id"
          pagination={{ pageSize: 6 }}
          scroll={{ x: "max-content" }}
          dataSource={[...events].reverse()}
          columns={[
            { title: "事件 ID", dataIndex: "id", width: 150, ellipsis: true },
            { title: "事件名", dataIndex: "name", width: 210 },
            {
              title: "状态",
              dataIndex: "status",
              width: 100,
              render: (s: string) => (
                <Tag color={s === "success" ? "green" : "red"}>{s}</Tag>
              ),
            },
            { title: "资源 ID", dataIndex: "resourceId", render: (v?: string) => v || "-" },
            { title: "接收时间", dataIndex: "receivedAt", width: 190 },
          ]}
        />
      </Card>
    </Space>
  );
}
