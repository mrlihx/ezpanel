import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Input,
  Popconfirm,
  Progress,
  Select,
  Space,
  Statistic,
  Table,
  Tag,
  Typography,
} from "antd";
import type { PanelResourceKind, PanelSDK } from "../panel-sdk";
import { formatAPIError, formatBytes, postJSON, putJSON } from "../api";

interface OverviewResponse {
  user: string;
  hostname: string;
  info: {
    panelVersion: string;
    sdkVersion: number;
    pluginId: string;
    dataDir: string;
  };
  config: {
    greeting: string;
    historyLimit: number;
    autoNotify: boolean;
    inspectMode: string;
    sampleFile: string;
    workDir: string;
    webhookSecret: { configured: boolean; byteLength: number; fingerprint: string };
    demoToken: { configured: boolean; byteLength: number; fingerprint: string };
  };
  counts: Record<string, number>;
  probeHealthy: boolean;
  probeMessage: string;
}

interface SysinfoResponse {
  hostname: string;
  os: string;
  platform: string;
  kernel: string;
  arch: string;
  panelVersion: string;
  cpu: { model: string; cores?: number; usage?: number };
  load?: { l1: number; l5: number; l15: number };
  mem?: { total: number; used: number; usedPercent: number };
  disks?: Array<{ mount: string; fstype: string; total: number; used: number; usedPercent: number }>;
}

interface MetricPoint {
  ts: string;
  cpu?: number;
  memPercent?: number;
  load1?: number;
  netUp?: number;
  netDown?: number;
  diskRead?: number;
  diskWrite?: number;
}

interface KVItem {
  key: string;
  value: string;
}

/**
 * 总览与 SDK 视图：
 * 展示 Host.Info、脱敏环境变量配置、应用商店 5 大设置页签映射、
 * PanelSDK 交互控件、服务器资源与监控历史、以及 Host.KV 完整增删查改。
 */
export default function OverviewView({ sdk }: { sdk: PanelSDK }) {
  const [overview, setOverview] = useState<OverviewResponse>();
  const [sysinfo, setSysinfo] = useState<SysinfoResponse>();
  const [metrics, setMetrics] = useState<MetricPoint[]>([]);
  const [kvList, setKvList] = useState<KVItem[]>([]);
  const [error, setError] = useState("");
  const [pickedResult, setPickedResult] = useState("");
  const [resourceKind, setResourceKind] = useState<PanelResourceKind>("sites");
  const [kvKey, setKvKey] = useState("demo-key");
  const [kvValue, setKvValue] = useState("hello-ezpanel");
  const [kvPrefix, setKvPrefix] = useState("");
  const [kvSingleResult, setKvSingleResult] = useState<KVItem | null>(null);
  const [logLevel, setLogLevel] = useState("info");
  const [logMsg, setLogMsg] = useState("前端手动触发的 Host.Log 演示日志");
  const [refreshSeq, setRefreshSeq] = useState(0);

  // 所有异步请求传入 AbortController.signal，并在组件卸载或重刷时自动中止
  useEffect(() => {
    const controller = new AbortController();
    setError("");
    void Promise.all([
      sdk.api<OverviewResponse>("/overview", { signal: controller.signal }),
      sdk.api<SysinfoResponse>("/sysinfo", { signal: controller.signal }),
      sdk.api<MetricPoint[]>("/monitoring", { signal: controller.signal }).catch(() => []),
      sdk.api<KVItem[]>(`/kv?prefix=${encodeURIComponent(kvPrefix)}`, {
        signal: controller.signal,
      }),
    ])
      .then(([ov, sys, mon, kvs]) => {
        setOverview(ov);
        setSysinfo(sys);
        setMetrics(mon || []);
        setKvList(kvs || []);
      })
      .catch((err) => {
        if (!controller.signal.aborted) {
          setError(formatAPIError(err));
        }
      });
    return () => controller.abort();
  }, [sdk, refreshSeq, kvPrefix]);

  const runPicker = async (mode: "file" | "dir" | "resource") => {
    try {
      let chosen: string | null = null;
      if (mode === "file" && sdk.pickFile) {
        chosen = await sdk.pickFile("/tmp");
      } else if (mode === "dir" && sdk.pickDirectory) {
        chosen = await sdk.pickDirectory("/tmp");
      } else if (mode === "resource" && sdk.pickResource) {
        chosen = await sdk.pickResource(resourceKind);
      }
      setPickedResult(chosen === null ? "用户取消了选择" : chosen);
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  const getSingleKV = async (key: string) => {
    try {
      const res = await sdk.api<KVItem>(`/kv?key=${encodeURIComponent(key)}`);
      setKvSingleResult(res);
      sdk.message.success(`KVGet(${res.key}) 读取成功`);
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  const saveKV = async () => {
    try {
      await putJSON(sdk, "/kv", { key: kvKey, value: kvValue });
      sdk.message.success("KV 已保存");
      setRefreshSeq((n) => n + 1);
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  const deleteKV = async (key: string) => {
    try {
      await sdk.api(`/kv?key=${encodeURIComponent(key)}`, { method: "DELETE" });
      sdk.message.success(`已删除 ${key}`);
      if (kvSingleResult?.key === key) setKvSingleResult(null);
      setRefreshSeq((n) => n + 1);
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  const writeHostLog = async () => {
    try {
      await postJSON(sdk, "/log", { level: logLevel, msg: logMsg });
      sdk.message.success("已写入插件日志（可在应用商店 → 设置 → 日志查看）");
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  const testAbort = () => {
    const controller = new AbortController();
    const promise = sdk.api("/overview", { signal: controller.signal });
    controller.abort();
    void promise.catch(() => {
      sdk.message.info("AbortController 已成功中止请求");
    });
  };

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      {error && <Alert type="error" showIcon message={error} />}

      {overview && (
        <div className="cs-kpi-grid">
          <div className="cs-kpi-card">
            <Statistic title="累计任务执行" value={overview.counts?.jobRuns ?? 0} suffix="次" />
          </div>
          <div className="cs-kpi-card">
            <Statistic title="已收订阅事件" value={overview.counts?.events ?? 0} suffix="条" />
          </div>
          <div className="cs-kpi-card">
            <Statistic title="通知收件箱" value={overview.counts?.notices ?? 0} suffix="条" />
          </div>
          <div className="cs-kpi-card">
            <Statistic
              title="自定义探针状态"
              value={overview.probeHealthy ? "Healthy" : "Unhealthy"}
              valueStyle={{ color: overview.probeHealthy ? "#52c41a" : "#ff4d4f" }}
            />
          </div>
        </div>
      )}

      <div className="cs-grid">
        <Card
          size="small"
          title="Host.Info 与配置摘要（秘密已脱敏）"
          extra={
            <Button size="small" onClick={() => setRefreshSeq((n) => n + 1)}>
              刷新
            </Button>
          }
        >
          {overview && (
            <Descriptions column={1} size="small">
              <Descriptions.Item label="面板用户">{overview.user}</Descriptions.Item>
              <Descriptions.Item label="面板版本">
                {overview.info.panelVersion}（SDK {overview.info.sdkVersion}）
              </Descriptions.Item>
              <Descriptions.Item label="数据目录">{overview.info.dataDir}</Descriptions.Item>
              <Descriptions.Item label="欢迎语（text）">
                {overview.config.greeting}
              </Descriptions.Item>
              <Descriptions.Item label="保留上限（number）">
                {overview.config.historyLimit}
              </Descriptions.Item>
              <Descriptions.Item label="自动通知（boolean）">
                {String(overview.config.autoNotify)}
              </Descriptions.Item>
              <Descriptions.Item label="巡检模式（select）">
                <Tag color="blue">{overview.config.inspectMode}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="文件路径（file）">
                {overview.config.sampleFile || "-"}
              </Descriptions.Item>
              <Descriptions.Item label="工作目录（directory）">
                {overview.config.workDir || "-"}
              </Descriptions.Item>
              <Descriptions.Item label="WEBHOOK_SECRET">
                {overview.config.webhookSecret.configured
                  ? `已配置 (${overview.config.webhookSecret.byteLength} B · SHA256:${overview.config.webhookSecret.fingerprint})`
                  : "未配置"}
              </Descriptions.Item>
              <Descriptions.Item label="DEMO_TOKEN">
                {overview.config.demoToken.configured
                  ? `已配置 (${overview.config.demoToken.byteLength} B · SHA256:${overview.config.demoToken.fingerprint})`
                  : "未配置"}
              </Descriptions.Item>
            </Descriptions>
          )}
        </Card>

        <Card size="small" title="PanelSDK 前端交互控件演示">
          <Space direction="vertical" size="small" style={{ width: "100%" }}>
            <Space wrap>
              <Button onClick={() => void runPicker("file")}>sdk.pickFile</Button>
              <Button onClick={() => void runPicker("dir")}>sdk.pickDirectory</Button>
            </Space>
            <Space wrap>
              <Select<PanelResourceKind>
                value={resourceKind}
                onChange={setResourceKind}
                style={{ width: 160 }}
                options={[
                  { value: "sites", label: "sites 网站" },
                  { value: "containers", label: "containers 容器" },
                  { value: "databases", label: "databases 数据库" },
                  { value: "certificates", label: "certificates 证书" },
                  { value: "channels", label: "channels 通知渠道" },
                ]}
              />
              <Button type="primary" onClick={() => void runPicker("resource")}>
                sdk.pickResource
              </Button>
            </Space>
            {pickedResult && (
              <Alert type="success" showIcon message={`选择结果：${pickedResult}`} />
            )}
            <Space wrap>
              <Button onClick={() => sdk.message.success("来自 sdk.message 的提示")}>
                sdk.message
              </Button>
              <Button
                onClick={() =>
                  sdk.modal.info({
                    title: "PanelSDK Modal 示例",
                    content: "此弹窗通过隔离 iframe 中的 App.useApp() modal 实例渲染。",
                  })
                }
              >
                sdk.modal
              </Button>
              <Button onClick={testAbort}>AbortSignal 中止测试</Button>
            </Space>
            <Space.Compact style={{ width: "100%" }}>
              <Select
                value={logLevel}
                onChange={setLogLevel}
                style={{ width: 96 }}
                options={[
                  { value: "debug", label: "debug" },
                  { value: "info", label: "info" },
                  { value: "warn", label: "warn" },
                  { value: "error", label: "error" },
                ]}
              />
              <Input
                value={logMsg}
                onChange={(e) => setLogMsg(e.target.value)}
                placeholder="写入 Host.Log 的摘要消息"
              />
              <Button onClick={() => void writeHostLog()}>写 Host.Log</Button>
            </Space.Compact>

            <Alert
              type="info"
              showIcon
              message="应用商店「插件设置」5 大标签页说明"
              description="本插件的设置窗口由 EzPanel 根据 plugin.yaml 自动生成：① 服务（启停/卸载/作者主页/前端隔离开关）；② 配置（全部 7 种 env 控件 + JSON 导入导出）；③ 授权（资源三级范围、通知渠道、凭据槽、Cron 与事件订阅）；④ 能力（声明权限与风险核验）；⑤ 日志（实时进程日志流）。"
            />
          </Space>
        </Card>
      </div>

      <Card size="small" title="服务器概览（Host.Sysinfo）与监控历史（Host.MonitoringHistory）">
        {sysinfo && (
          <Space direction="vertical" size="middle" style={{ width: "100%" }}>
            <div className="cs-kpi-grid">
              <Statistic title="主机名" value={sysinfo.hostname} />
              <Statistic
                title="系统 / 架构"
                value={`${sysinfo.platform || sysinfo.os} / ${sysinfo.arch}`}
              />
              <Statistic
                title="CPU 使用率"
                value={sysinfo.cpu?.usage != null ? sysinfo.cpu.usage.toFixed(1) : "-"}
                suffix="%"
              />
              <Statistic
                title="系统负载 (1/5/15m)"
                value={
                  sysinfo.load
                    ? `${sysinfo.load.l1.toFixed(2)} / ${sysinfo.load.l5.toFixed(2)} / ${sysinfo.load.l15.toFixed(2)}`
                    : "-"
                }
              />
              <Statistic
                title="内存使用"
                value={
                  sysinfo.mem
                    ? `${formatBytes(sysinfo.mem.used)} / ${formatBytes(sysinfo.mem.total)}`
                    : "-"
                }
              />
            </div>

            {sysinfo.disks?.length ? (
              <Table
                size="small"
                rowKey="mount"
                pagination={false}
                scroll={{ x: "max-content" }}
                dataSource={sysinfo.disks}
                columns={[
                  { title: "挂载点", dataIndex: "mount" },
                  { title: "文件系统", dataIndex: "fstype", width: 110 },
                  {
                    title: "已用 / 总量",
                    width: 200,
                    render: (_, d) => `${formatBytes(d.used)} / ${formatBytes(d.total)}`,
                  },
                  {
                    title: "使用率",
                    dataIndex: "usedPercent",
                    width: 180,
                    render: (pct: number) => (
                      <Progress percent={Number(pct.toFixed(1))} size="small" />
                    ),
                  },
                ]}
              />
            ) : null}

            <Typography.Text className="cs-muted">
              近 1 小时监控采样点（共 {metrics.length} 条，展示最近 8 条）：
            </Typography.Text>
            <Table
              size="small"
              rowKey="ts"
              pagination={false}
              scroll={{ x: "max-content" }}
              dataSource={[...metrics].slice(-8).reverse()}
              columns={[
                { title: "采样时间", dataIndex: "ts", width: 200 },
                {
                  title: "CPU %",
                  dataIndex: "cpu",
                  width: 90,
                  render: (v?: number) => (v != null ? `${v.toFixed(1)}%` : "-"),
                },
                {
                  title: "内存 %",
                  dataIndex: "memPercent",
                  width: 90,
                  render: (v?: number) => (v != null ? `${v.toFixed(1)}%` : "-"),
                },
                {
                  title: "Load1",
                  dataIndex: "load1",
                  width: 90,
                  render: (v?: number) => (v != null ? v.toFixed(2) : "-"),
                },
                {
                  title: "网络上/下行",
                  width: 180,
                  render: (_, m) =>
                    `${formatBytes(m.netUp)}/s ↑ · ${formatBytes(m.netDown)}/s ↓`,
                },
                {
                  title: "磁盘读/写",
                  width: 180,
                  render: (_, m) =>
                    `${formatBytes(m.diskRead)}/s R · ${formatBytes(m.diskWrite)}/s W`,
                },
              ]}
            />
          </Space>
        )}
      </Card>

      <Card size="small" title="插件 KV 存储演示（Host.KVList / KVGet / KVSet / KVDelete）">
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Space wrap>
            <Input
              style={{ width: 180 }}
              value={kvKey}
              onChange={(e) => setKvKey(e.target.value)}
              placeholder="键名（1-128 字节）"
            />
            <Input
              style={{ width: 240 }}
              value={kvValue}
              onChange={(e) => setKvValue(e.target.value)}
              placeholder="键值（最大 64KiB）"
            />
            <Button type="primary" onClick={() => void saveKV()}>
              写入 (KVSet)
            </Button>
            <Button onClick={() => void getSingleKV(kvKey)}>单键读取 (KVGet)</Button>
            <Input
              style={{ width: 180 }}
              value={kvPrefix}
              onChange={(e) => setKvPrefix(e.target.value)}
              placeholder="按前缀过滤列表..."
            />
          </Space>

          {kvSingleResult && (
            <Alert
              type="success"
              showIcon
              closable
              onClose={() => setKvSingleResult(null)}
              message={`Host.KVGet("${kvSingleResult.key}") = ${kvSingleResult.value}`}
            />
          )}

          <Table
            size="small"
            rowKey="key"
            pagination={{ pageSize: 5 }}
            scroll={{ x: "max-content" }}
            dataSource={kvList}
            columns={[
              { title: "Key", dataIndex: "key", width: 220 },
              { title: "Value", dataIndex: "value", ellipsis: true },
              {
                title: "操作",
                width: 170,
                render: (_, row) => (
                  <Space size={4}>
                    <Button size="small" onClick={() => void getSingleKV(row.key)}>
                      KVGet
                    </Button>
                    <Popconfirm title={`删除 ${row.key}？`} onConfirm={() => void deleteKV(row.key)}>
                      <Button size="small" danger>
                        删除
                      </Button>
                    </Popconfirm>
                  </Space>
                ),
              },
            ]}
          />
        </Space>
      </Card>
    </Space>
  );
}
