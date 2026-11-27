import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Input,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from "antd";
import type { PanelSDK } from "../panel-sdk";
import { formatAPIError, formatBytes, postJSON } from "../api";

interface StateResponse {
  state: {
    notices: Array<{
      channelId: string;
      target: string;
      title: string;
      message: string;
      time: string;
    }>;
    dnsRecords: Record<string, string[]>;
    dnsOps: Array<{ action: string; name: string; value: string; time: string }>;
    deployHooks: Array<{
      project: string;
      phase: string;
      status: string;
      taskId: string;
      blocked: boolean;
      time: string;
    }>;
    blockNextBeforeHook: boolean;
    storageOps: Array<{
      operation: string;
      folder: string;
      key: string;
      size: number;
      sha256: string;
      success: boolean;
      error?: string;
      time: string;
    }>;
    serviceCalls: Array<{ caller: string; name: string; input: string; time: string }>;
    probeHealthy: boolean;
    probeMessage: string;
  };
  storageObjects: Array<{ path: string; size: number; modifiedAt: string }>;
}

/**
 * 扩展渠道与钩子视图：
 * 集中展示通知渠道收件箱（OnNotification）、自定义监控探针（OnMonitorCheck）、
 * 备份存储渠道与已存对象（OnBackupStorage）、ACME DNS TXT 记录与变更历史（OnDNSChange）、
 * Compose 部署前后置钩子（OnDeployment）、插件间服务（OnService）以及 OIDC SSO 说明。
 */
export default function ProvidersView({ sdk }: { sdk: PanelSDK }) {
  const [data, setData] = useState<StateResponse>();
  const [probeMsg, setProbeMsg] = useState("自定义探针状态更新");
  const [serviceOutput, setServiceOutput] = useState("");
  const [error, setError] = useState("");
  const [refreshSeq, setRefreshSeq] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setError("");
    void sdk
      .api<StateResponse>("/extensions/state", { signal: controller.signal })
      .then((res) => setData(res))
      .catch((err) => {
        if (!controller.signal.aborted) setError(formatAPIError(err));
      });
    return () => controller.abort();
  }, [sdk, refreshSeq]);

  const dnsRows = Object.entries(data?.state.dnsRecords || {}).map(([name, values]) => ({
    name,
    values,
  }));

  const toggleProbe = async (healthy: boolean) => {
    try {
      await postJSON(sdk, "/extensions/probe", { healthy, message: probeMsg });
      sdk.message.success(`探针已设为${healthy ? "健康" : "异常"}`);
      setRefreshSeq((n) => n + 1);
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      {error && <Alert type="error" showIcon message={error} />}

      <Space>
        <Button size="small" onClick={() => setRefreshSeq((n) => n + 1)}>
          刷新扩展状态
        </Button>
      </Space>

      <div className="cs-grid">
        <Card
          size="small"
          title="通知渠道收件箱（OnNotification）"
          extra={
            <Button
              size="small"
              onClick={() =>
                void postJSON(sdk, "/extensions/inbox/clear", {})
                  .then(() => setRefreshSeq((n) => n + 1))
                  .catch((e) => sdk.message.error(formatAPIError(e)))
              }
            >
              清空
            </Button>
          }
        >
          <Table
            size="small"
            rowKey={(r, i) => `${r.channelId}-${i}`}
            pagination={{ pageSize: 4 }}
            scroll={{ x: "max-content" }}
            dataSource={[...(data?.state.notices || [])].reverse()}
            columns={[
              { title: "目标", dataIndex: "target", width: 100 },
              { title: "标题", dataIndex: "title", width: 130 },
              { title: "正文", dataIndex: "message", ellipsis: true },
            ]}
          />
        </Card>

        <Card size="small" title="自定义监控检测探针（OnMonitorCheck）">
          <Space direction="vertical" style={{ width: "100%" }}>
            <Space wrap>
              <Tag color={data?.state.probeHealthy ? "green" : "red"}>
                custom-probe: {data?.state.probeHealthy ? "Healthy" : "Unhealthy"}
              </Tag>
              <Typography.Text className="cs-muted">{data?.state.probeMessage}</Typography.Text>
            </Space>
            <Input
              value={probeMsg}
              onChange={(e) => setProbeMsg(e.target.value)}
              placeholder="探针返回文案"
            />
            <Space wrap>
              <Button type="primary" onClick={() => void toggleProbe(true)}>
                设为健康
              </Button>
              <Button danger onClick={() => void toggleProbe(false)}>
                模拟异常（触发告警规则）
              </Button>
            </Space>
          </Space>
        </Card>
      </div>

      <Card size="small" title="备份存储渠道（OnBackupStorage：Demo 私有存储）">
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Typography.Text className="cs-muted">
            在「面板设置 → 备份设置」可添加类型为「Demo 私有存储」的备份渠道。已存储对象：
            {data?.storageObjects.length || 0} 个
          </Typography.Text>
          <div className="cs-grid">
            <Table
              size="small"
              rowKey={(r, i) => `${r.operation}-${r.key}-${i}`}
              pagination={{ pageSize: 4 }}
              scroll={{ x: "max-content" }}
              dataSource={[...(data?.state.storageOps || [])].reverse()}
              columns={[
                { title: "操作", dataIndex: "operation", width: 80 },
                { title: "目录", dataIndex: "folder", width: 110 },
                { title: "Key", dataIndex: "key", ellipsis: true },
                {
                  title: "大小",
                  dataIndex: "size",
                  width: 90,
                  render: (v: number) => (v ? formatBytes(v) : "-"),
                },
                {
                  title: "结果",
                  dataIndex: "success",
                  width: 80,
                  render: (ok: boolean) => (
                    <Tag color={ok ? "green" : "red"}>{ok ? "成功" : "失败"}</Tag>
                  ),
                },
              ]}
            />
            <Table
              size="small"
              rowKey="path"
              pagination={{ pageSize: 4 }}
              scroll={{ x: "max-content" }}
              dataSource={data?.storageObjects || []}
              columns={[
                { title: "已存对象路径", dataIndex: "path", ellipsis: true },
                {
                  title: "大小",
                  dataIndex: "size",
                  width: 90,
                  render: (v: number) => formatBytes(v),
                },
                { title: "修改时间", dataIndex: "modifiedAt", width: 180 },
              ]}
            />
          </div>
        </Space>
      </Card>

      <div className="cs-grid">
        <Card size="small" title="ACME DNS 验证渠道（OnDNSChange）">
          <Space direction="vertical" size="middle" style={{ width: "100%" }}>
            <Table
              size="small"
              rowKey="name"
              pagination={{ pageSize: 4 }}
              scroll={{ x: "max-content" }}
              dataSource={dnsRows}
              columns={[
                { title: "TXT 记录名", dataIndex: "name", ellipsis: true },
                {
                  title: "当前值列表",
                  dataIndex: "values",
                  render: (vals: string[]) => vals.map((v) => <Tag key={v}>{v}</Tag>),
                },
              ]}
            />
            <Table
              size="small"
              rowKey={(r, i) => `${r.name}-${r.time}-${i}`}
              pagination={{ pageSize: 4 }}
              scroll={{ x: "max-content" }}
              dataSource={[...(data?.state.dnsOps || [])].reverse()}
              columns={[
                {
                  title: "操作",
                  dataIndex: "action",
                  width: 90,
                  render: (a: string) => (
                    <Tag color={a === "present" ? "green" : "orange"}>{a}</Tag>
                  ),
                },
                { title: "记录名", dataIndex: "name", ellipsis: true },
                { title: "值", dataIndex: "value", ellipsis: true },
              ]}
            />
          </Space>
        </Card>

        <Card
          size="small"
          title="Compose 部署钩子（OnDeployment）"
          extra={
            <Space>
              <Typography.Text className="cs-muted">拦截下次前置钩子</Typography.Text>
              <Switch
                checked={Boolean(data?.state.blockNextBeforeHook)}
                onChange={(checked) =>
                  void postJSON(sdk, "/extensions/deploy-hook-policy", {
                    blockNextBefore: checked,
                  })
                    .then(() => setRefreshSeq((n) => n + 1))
                    .catch((e) => sdk.message.error(formatAPIError(e)))
                }
              />
            </Space>
          }
        >
          <Table
            size="small"
            rowKey={(r, i) => `${r.taskId}-${r.phase}-${i}`}
            pagination={{ pageSize: 5 }}
            scroll={{ x: "max-content" }}
            dataSource={[...(data?.state.deployHooks || [])].reverse()}
            columns={[
              { title: "项目", dataIndex: "project" },
              { title: "阶段", dataIndex: "phase", width: 80 },
              { title: "状态", dataIndex: "status", width: 90 },
              {
                title: "拦截",
                dataIndex: "blocked",
                width: 70,
                render: (b: boolean) => (b ? <Tag color="red">是</Tag> : "否"),
              },
            ]}
          />
        </Card>
      </div>

      <div className="cs-grid">
        <Card size="small" title="插件间服务提供方（OnService：echo-service / summary-service）">
          <Space direction="vertical" size="small" style={{ width: "100%" }}>
            <Space wrap>
              <Button
                size="small"
                onClick={() =>
                  void postJSON(sdk, "/extensions/service-selftest", {
                    name: "echo-service",
                    payload: JSON.stringify({ hello: "ezpanel" }),
                  })
                    .then((res) => {
                      setServiceOutput(JSON.stringify(res, null, 2));
                      setRefreshSeq((n) => n + 1);
                    })
                    .catch((e) => sdk.message.error(formatAPIError(e)))
                }
              >
                本地自测 echo-service
              </Button>
              <Button
                size="small"
                onClick={() =>
                  void postJSON(sdk, "/extensions/service-selftest", {
                    name: "summary-service",
                    payload: "{}",
                  })
                    .then((res) => {
                      setServiceOutput(JSON.stringify(res, null, 2));
                      setRefreshSeq((n) => n + 1);
                    })
                    .catch((e) => sdk.message.error(formatAPIError(e)))
                }
              >
                本地自测 summary-service
              </Button>
            </Space>
            {serviceOutput && <pre className="cs-pre">{serviceOutput}</pre>}
            <Table
              size="small"
              rowKey={(r, i) => `${r.caller}-${r.time}-${i}`}
              pagination={{ pageSize: 4 }}
              scroll={{ x: "max-content" }}
              dataSource={[...(data?.state.serviceCalls || [])].reverse()}
              columns={[
                { title: "调用方", dataIndex: "caller", width: 130 },
                { title: "服务名", dataIndex: "name", width: 140 },
                { title: "入参摘要", dataIndex: "input", ellipsis: true },
              ]}
            />
            <Typography.Text className="cs-muted">
              其他插件可在清单声明 dependencies: &#123; {sdk.pluginId}: "0.2.1" &#125; 与
              services.call，通过 Host.CallService 调用本插件服务。
            </Typography.Text>
          </Space>
        </Card>

        <Card size="small" title="企业 OIDC 单点登录与兼容模式命令说明">
          <Space direction="vertical" size="small" style={{ width: "100%" }}>
            <Typography.Text>
              1. <strong>ssoProvider</strong>：本插件已注册「Demo 企业 SSO」，启用后可在面板「系统设置 → 单点登录」配置
              OIDC 签发者、客户端 ID、密钥与账号 sub 绑定（PKCE + 一次性 State）。
            </Typography.Text>
            <Typography.Text className="cs-muted">
              2. <strong>exec.run / execAllow</strong>：Linux 新装插件默认启用 systemd 沙箱，与
              exec.run 互斥（沙箱插件直接运行自带程序）；如需演示兼容模式特权桥接，须移除 manifest 中的
              sandbox 字段并声明 execAllow。
            </Typography.Text>
          </Space>
        </Card>
      </div>
    </Space>
  );
}
