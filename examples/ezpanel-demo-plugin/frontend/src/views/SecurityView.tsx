import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Input,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import type { PanelSDK } from "../panel-sdk";
import { formatAPIError, postJSON } from "../api";

interface SecurityStatus {
  firewallEnabled: boolean;
  firewallRules: number;
  hardeningEnabled: boolean;
  hardeningRules: number;
  hardeningCheckedAt?: string;
  wafEnabled: boolean;
}

interface AuditRecord {
  id: number;
  action: string;
  target: string;
  taskId?: string;
  phase: string;
  success: boolean;
  errorCode: number;
  createdAt: string;
}

interface CredentialCheck {
  slot: string;
  summary: { configured: boolean; byteLength: number; fingerprint: string };
}

/**
 * 凭据与审计视图：
 * 展示系统安全概要与加固扫描（Host.SecurityStatus / ScanHardening）、
 * 外部凭据槽脱敏指纹（Host.Credential）、插件任务归属查询（Host.Task & sdk.showTask）
 * 以及本插件的 Host 审计历史分页（Host.AuditHistory）。
 */
export default function SecurityView({ sdk }: { sdk: PanelSDK }) {
  const [sec, setSec] = useState<SecurityStatus>();
  const [secError, setSecError] = useState("");
  const [audits, setAudits] = useState<AuditRecord[]>([]);
  const [auditTotal, setAuditTotal] = useState(0);
  const [auditError, setAuditError] = useState("");
  const [page, setPage] = useState(1);
  const [cred, setCred] = useState<CredentialCheck>();
  const [taskIdInput, setTaskIdInput] = useState("");
  const [taskSummary, setTaskSummary] = useState("");
  const [refreshSeq, setRefreshSeq] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setSecError("");
    setAuditError("");
    void Promise.all([
      sdk.api<SecurityStatus>("/extensions/security", { signal: controller.signal }).then(
        (s) => {
          if (!controller.signal.aborted) setSec(s);
        },
        (err) => {
          if (!controller.signal.aborted) setSecError(formatAPIError(err));
        },
      ),
      sdk
        .api<{ list: AuditRecord[]; total: number }>(
          `/extensions/audit?page=${page}&size=10`,
          { signal: controller.signal },
        )
        .then(
          (a) => {
            if (!controller.signal.aborted) {
              setAudits(a?.list || []);
              setAuditTotal(a?.total || 0);
            }
          },
          (err) => {
            if (!controller.signal.aborted) setAuditError(formatAPIError(err));
          },
        ),
    ]);
    return () => controller.abort();
  }, [sdk, page, refreshSeq]);

  const checkCredential = async () => {
    try {
      const res = await sdk.api<CredentialCheck>("/extensions/credential?slot=demo-api-key");
      setCred(res);
      sdk.message.success("已读取绑定凭据的脱敏指纹");
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  const queryTask = async () => {
    try {
      const res = await sdk.api(`/extensions/task?id=${encodeURIComponent(taskIdInput)}`);
      setTaskSummary(JSON.stringify(res, null, 2));
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <div className="cs-grid">
        <Card
          size="small"
          title="系统安全概要与加固扫描（Host.SecurityStatus / ScanHardening）"
          extra={
            <Button
              size="small"
              type="primary"
              onClick={() =>
                void postJSON<{ taskId: string }>(sdk, "/extensions/security/scan", {})
                  .then((r) => {
                    sdk.message.success(`已提交加固扫描：${r.taskId}`);
                    sdk.showTask?.(r.taskId);
                  })
                  .catch((e) => sdk.message.error(formatAPIError(e)))
              }
            >
              提交加固检查
            </Button>
          }
        >
          {secError ? (
            <Alert type="error" showIcon message={secError} />
          ) : sec ? (
            <Descriptions column={2} size="small">
              <Descriptions.Item label="防火墙">
                <Tag color={sec.firewallEnabled ? "green" : "default"}>
                  {sec.firewallEnabled ? `启用 (${sec.firewallRules} 规则)` : "关闭"}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="WAF">
                <Tag color={sec.wafEnabled ? "green" : "default"}>
                  {sec.wafEnabled ? "启用" : "关闭"}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="系统加固">
                <Tag color={sec.hardeningEnabled ? "green" : "default"}>
                  {sec.hardeningEnabled ? `启用 (${sec.hardeningRules} 项)` : "关闭"}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="最近检查">
                {sec.hardeningCheckedAt || "-"}
              </Descriptions.Item>
            </Descriptions>
          ) : (
            <Typography.Text className="cs-muted">正在加载安全状态...</Typography.Text>
          )}
        </Card>

        <Card size="small" title="外部凭据槽（Host.Credential：demo-api-key）">
          <Space direction="vertical" style={{ width: "100%" }}>
            <Typography.Text className="cs-muted">
              在「面板设置 → 外部凭据」创建密钥并在插件「应用商店 → 插件设置 → 授权」绑定到 demo-api-key 槽位。
            </Typography.Text>
            <Button onClick={() => void checkCredential()}>读取凭据脱敏指纹</Button>
            {cred && (
              <Alert
                type="success"
                showIcon
                message={`槽位 ${cred.slot}：已配置 (${cred.summary.byteLength} 字节 · SHA256:${cred.summary.fingerprint})`}
              />
            )}
          </Space>
        </Card>
      </div>

      <Card size="small" title="插件任务归属查询（Host.Task & sdk.showTask）">
        <Space direction="vertical" style={{ width: "100%" }}>
          <Space wrap>
            <Input
              style={{ width: 280 }}
              value={taskIdInput}
              onChange={(e) => setTaskIdInput(e.target.value)}
              placeholder="输入本插件提交或执行的 taskId"
            />
            <Button type="primary" disabled={!taskIdInput} onClick={() => void queryTask()}>
              Host.Task 查询
            </Button>
            <Button disabled={!taskIdInput} onClick={() => sdk.showTask?.(taskIdInput)}>
              打开任务抽屉
            </Button>
          </Space>
          {taskSummary && <pre className="cs-pre">{taskSummary}</pre>}
        </Space>
      </Card>

      <Card
        size="small"
        title={`插件审计日志（Host.AuditHistory · 共 ${auditTotal} 条）`}
        extra={
          <Button size="small" onClick={() => setRefreshSeq((n) => n + 1)}>
            刷新
          </Button>
        }
      >
        {auditError && (
          <Alert type="error" showIcon message={auditError} style={{ marginBottom: 12 }} />
        )}
        <Table
          size="small"
          rowKey="id"
          scroll={{ x: "max-content" }}
          dataSource={audits}
          pagination={{
            current: page,
            pageSize: 10,
            total: auditTotal,
            onChange: (next) => setPage(next),
          }}
          columns={[
            { title: "动作", dataIndex: "action", width: 160 },
            { title: "目标", dataIndex: "target", ellipsis: true },
            {
              title: "结果",
              dataIndex: "success",
              width: 90,
              render: (v: boolean) => <Tag color={v ? "green" : "red"}>{v ? "成功" : "失败"}</Tag>,
            },
            { title: "错误码", dataIndex: "errorCode", width: 80 },
            { title: "时间", dataIndex: "createdAt", width: 190 },
          ]}
        />
      </Card>
    </Space>
  );
}
