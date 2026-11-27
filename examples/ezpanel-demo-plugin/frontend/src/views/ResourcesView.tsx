import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import type { PanelSDK } from "../panel-sdk";
import { formatAPIError, formatBytes, postJSON } from "../api";

interface ResourceRow {
  id: string;
  name: string;
  kind?: string;
  status?: string;
  version?: string;
  domains?: string[];
  expiresAt?: string;
  latestBackup?: { createdAt?: string; kind?: string };
}

interface SiteDetail {
  id: string;
  name: string;
  projectType: string;
  enabled: boolean;
  runtimeVersion: string;
  runtimePort: number;
  proxyRoutes?: Array<{ name: string; path: string; enabled: boolean }>;
}

interface SiteBackupRow {
  id: string;
  fileName: string;
  storageType: string;
  size: number;
  createdAt: string;
}

interface DBBackupRow {
  id: string;
  database: string;
  fileName: string;
  size: number;
  createdAt: string;
}

interface LogModalQuery {
  kind: "site" | "container";
  id: string;
  name: string;
  logType: "access" | "error";
  lines: number;
  keyword: string;
}

const RUNTIME_PROJECT_TYPES = new Set(["nodejs", "python", "go"]);

/**
 * 资源与运维视图：
 * 完整展示网站、Docker 容器、数据库、SSL 证书、Compose 编排与系统应用六大资源接口；
 * 自动响应 sdk.context.siteId（网站设置内嵌页）与 sdk.context.resource（资源列表操作菜单），
 * 并按面板真实业务能力约束操作按钮（例如仅 Node.js/Python/Go 项目支持重启，MySQL/MariaDB 支持库备份）。
 */
export default function ResourcesView({ sdk }: { sdk: PanelSDK }) {
  const ctxKind = sdk.context?.resource?.kind;
  const ctxResourceId = sdk.context?.resource?.id;
  const [subTab, setSubTab] = useState<string>(ctxKind || "sites");
  const [sites, setSites] = useState<ResourceRow[]>([]);
  const [containers, setContainers] = useState<ResourceRow[]>([]);
  const [databases, setDatabases] = useState<ResourceRow[]>([]);
  const [certificates, setCertificates] = useState<ResourceRow[]>([]);
  const [composeList, setComposeList] = useState<ResourceRow[]>([]);
  const [apps, setApps] = useState<ResourceRow[]>([]);
  const [tabErrors, setTabErrors] = useState<Record<string, string>>({});
  const [refreshSeq, setRefreshSeq] = useState(0);

  // 日志与 JSON 采样弹窗状态
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputContent, setOutputContent] = useState("");
  const [logQuery, setLogQuery] = useState<LogModalQuery | null>(null);
  const [wrapLog, setWrapLog] = useState(true);
  const [logLoading, setLogLoading] = useState(false);

  // 站点详情与备份
  const [selectedSite, setSelectedSite] = useState<SiteDetail>();
  const [siteBackups, setSiteBackups] = useState<SiteBackupRow[]>([]);
  const [activeSiteId, setActiveSiteId] = useState<string>(sdk.context?.siteId || "");

  // 数据库操作状态
  const [selectedDBId, setSelectedDBId] = useState("");
  const [dbNames, setDbNames] = useState<string[]>([]);
  const [dbBackupName, setDbBackupName] = useState("");
  const [dbBackups, setDbBackups] = useState<DBBackupRow[]>([]);
  const [dbDetailError, setDbDetailError] = useState("");

  // 证书绑定
  const [bindSiteId, setBindSiteId] = useState("");
  const [bindCertId, setBindCertId] = useState("");
  const [forceHTTPS, setForceHTTPS] = useState(false);

  useEffect(() => {
    if (ctxKind) setSubTab(ctxKind);
  }, [ctxKind]);

  useEffect(() => {
    if (sdk.context?.siteId) setActiveSiteId(sdk.context.siteId);
  }, [sdk.context?.siteId]);

  useEffect(() => {
    const controller = new AbortController();
    const nextErrors: Record<string, string> = {};
    const loadKind = async (key: string, path: string): Promise<ResourceRow[]> => {
      try {
        return await sdk.api<ResourceRow[]>(path, { signal: controller.signal });
      } catch (err) {
        if (!controller.signal.aborted) {
          nextErrors[key] = formatAPIError(err);
        }
        return [];
      }
    };

    void Promise.all([
      loadKind("sites", "/resources/sites"),
      loadKind("containers", "/resources/containers"),
      loadKind("databases", "/resources/databases"),
      loadKind("certificates", "/resources/certificates"),
      loadKind("compose", "/resources/compose"),
      loadKind("apps", "/resources/apps"),
    ]).then(([s, c, d, cert, comp, appRows]) => {
      if (controller.signal.aborted) return;
      setSites(s);
      setContainers(c);
      setDatabases(d);
      setCertificates(cert);
      setComposeList(comp);
      setApps(appRows);
      setTabErrors(nextErrors);
    });
    return () => controller.abort();
  }, [sdk, refreshSeq]);

  useEffect(() => {
    if (!activeSiteId) return;
    const controller = new AbortController();
    void Promise.all([
      sdk.api<SiteDetail>(`/resources/site-detail?id=${encodeURIComponent(activeSiteId)}`, {
        signal: controller.signal,
      }),
      sdk.api<{ list: SiteBackupRow[] }>(
        `/resources/site-backups?id=${encodeURIComponent(activeSiteId)}&page=1&size=10`,
        { signal: controller.signal },
      ),
    ])
      .then(([detail, page]) => {
        setSelectedSite(detail);
        setSiteBackups(page.list || []);
      })
      .catch((err) => {
        if (!controller.signal.aborted) sdk.message.warning(formatAPIError(err));
      });
    return () => controller.abort();
  }, [sdk, activeSiteId, refreshSeq]);

  const handleTask = (res: { taskId: string }, label: string) => {
    sdk.message.success(`${label}已提交任务：${res.taskId}`);
    sdk.showTask?.(res.taskId);
  };

  const fetchLogs = async (query: LogModalQuery) => {
    setLogLoading(true);
    try {
      const path =
        query.kind === "site"
          ? `/resources/site-logs?id=${encodeURIComponent(query.id)}&type=${encodeURIComponent(query.logType)}&lines=${query.lines}&keyword=${encodeURIComponent(query.keyword)}`
          : `/resources/container-logs?id=${encodeURIComponent(query.id)}&lines=${query.lines}`;
      const res = await sdk.api<{ content: string; truncated?: boolean }>(path);
      setLogQuery(query);
      setOutputTitle(
        query.kind === "site" ? `网站日志 · ${query.name}` : `容器日志 · ${query.name}`,
      );
      setOutputContent(res.content || "（暂无日志输出）");
      setOutputOpen(true);
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    } finally {
      setLogLoading(false);
    }
  };

  const openOutput = (title: string, content: string) => {
    setLogQuery(null);
    setOutputTitle(title);
    setOutputContent(content);
    setOutputOpen(true);
  };

  const confirmSiteRestore = (siteId: string, backupId: string, siteName: string) => {
    let typed = "";
    sdk.modal.confirm({
      title: "确认恢复网站备份",
      content: (
        <Space direction="vertical" style={{ width: "100%" }}>
          <Typography.Text>
            请输入站点名称 <Typography.Text code>{siteName}</Typography.Text> 确认恢复（目标站点需已停止）：
          </Typography.Text>
          <Input onChange={(e) => (typed = e.target.value)} placeholder={siteName} />
        </Space>
      ),
      onOk: async () => {
        try {
          const res = await postJSON<{ taskId: string }>(sdk, "/resources/site-backup-restore", {
            siteId,
            backupId,
            confirmName: typed,
          });
          handleTask(res, "站点恢复");
        } catch (err) {
          sdk.message.error(formatAPIError(err));
        }
      },
    });
  };

  const loadDatabaseDetails = async (serverId: string) => {
    setSelectedDBId(serverId);
    setDbDetailError("");
    const errors: string[] = [];
    const [names, page] = await Promise.all([
      sdk
        .api<string[]>(`/resources/database-names?id=${encodeURIComponent(serverId)}`)
        .catch((err) => {
          errors.push(`可备份库列表：${formatAPIError(err)}`);
          return [] as string[];
        }),
      sdk
        .api<{ list: DBBackupRow[] }>(
          `/resources/database-backups?id=${encodeURIComponent(serverId)}&page=1&size=10`,
        )
        .catch((err) => {
          errors.push(`备份历史：${formatAPIError(err)}`);
          return { list: [] as DBBackupRow[] };
        }),
    ]);
    setDbNames(names || []);
    setDbBackupName(names?.[0] || "");
    setDbBackups(page.list || []);
    if (errors.length) setDbDetailError(errors.join("；"));
  };

  const confirmDBRestore = (serverId: string, backupId: string, dbName: string) => {
    let typed = "";
    sdk.modal.confirm({
      title: "确认恢复数据库备份",
      content: (
        <Space direction="vertical" style={{ width: "100%" }}>
          <Typography.Text>
            请输入原库名 <Typography.Text code>{dbName}</Typography.Text> 进行精确确认：
          </Typography.Text>
          <Input onChange={(e) => (typed = e.target.value)} placeholder={dbName} />
        </Space>
      ),
      onOk: async () => {
        try {
          const res = await postJSON<{ taskId: string }>(sdk, "/resources/database-restore", {
            serverId,
            backupId,
            confirmDatabase: typed,
          });
          handleTask(res, "数据库恢复");
        } catch (err) {
          sdk.message.error(formatAPIError(err));
        }
      },
    });
  };

  const controlContainer = (id: string, action: "start" | "stop" | "restart") =>
    void postJSON<{ taskId: string }>(sdk, "/resources/container-control", { id, action })
      .then((r) => handleTask(r, `容器 ${action}`))
      .catch((e) => sdk.message.error(formatAPIError(e)));

  const controlApp = (id: string, action: "start" | "stop" | "restart" | "reload") =>
    void postJSON<{ taskId: string }>(sdk, "/resources/app-control", { id, action })
      .then((r) => handleTask(r, `应用 ${action}`))
      .catch((e) => sdk.message.error(formatAPIError(e)));

  const highlightClass = (kind: string, id: string) =>
    (ctxKind === kind && ctxResourceId === id) || (kind === "sites" && activeSiteId === id)
      ? "cs-row-highlight"
      : "";

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      {tabErrors[subTab] && <Alert type="warning" showIcon message={tabErrors[subTab]} />}

      {sdk.context?.resource && (
        <Alert
          type="info"
          showIcon
          message={`来自资源菜单上下文：${sdk.context.resource.kind} · ${sdk.context.resource.name || sdk.context.resource.id}${sdk.context.resource.engine ? ` (${sdk.context.resource.engine})` : ""}`}
          action={
            <Space>
              {sdk.context.resource.kind === "sites" && (
                <Button size="small" onClick={() => setActiveSiteId(sdk.context!.resource!.id)}>
                  加载站点详情
                </Button>
              )}
              {sdk.context.resource.kind === "containers" && (
                <Button
                  size="small"
                  onClick={() =>
                    void fetchLogs({
                      kind: "container",
                      id: sdk.context!.resource!.id,
                      name: sdk.context!.resource!.name || sdk.context!.resource!.id,
                      logType: "access",
                      lines: 100,
                      keyword: "",
                    })
                  }
                >
                  查看容器日志
                </Button>
              )}
              {sdk.context.resource.kind === "databases" && (
                <Button
                  size="small"
                  onClick={() => void loadDatabaseDetails(sdk.context!.resource!.id)}
                >
                  加载库与备份
                </Button>
              )}
            </Space>
          }
        />
      )}

      <Tabs
        activeKey={subTab}
        onChange={setSubTab}
        tabBarExtraContent={
          <Button size="small" onClick={() => setRefreshSeq((n) => n + 1)}>
            刷新全部资源
          </Button>
        }
        items={[
          {
            key: "sites",
            label: `网站 (${sites.length})`,
            children: (
              <Space direction="vertical" size="middle" style={{ width: "100%" }}>
                <Table
                  size="small"
                  rowKey="id"
                  pagination={{ pageSize: 6 }}
                  scroll={{ x: "max-content" }}
                  rowClassName={(row) => highlightClass("sites", row.id)}
                  dataSource={sites}
                  columns={[
                    { title: "名称", dataIndex: "name" },
                    { title: "类型", dataIndex: "kind", width: 100 },
                    {
                      title: "状态",
                      dataIndex: "status",
                      width: 90,
                      render: (v: string) => (
                        <Tag color={v === "enabled" ? "green" : "default"}>{v}</Tag>
                      ),
                    },
                    {
                      title: "域名",
                      dataIndex: "domains",
                      render: (d?: string[]) => d?.join(", ") || "-",
                    },
                    {
                      title: "操作",
                      width: 340,
                      render: (_, row) => {
                        const canRestart =
                          RUNTIME_PROJECT_TYPES.has(row.kind || "") && row.status === "enabled";
                        const canBackup = row.kind !== "proxy";
                        return (
                          <Space wrap size={4}>
                            <Button size="small" onClick={() => setActiveSiteId(row.id)}>
                              详情/备份
                            </Button>
                            <Button
                              size="small"
                              onClick={() =>
                                void fetchLogs({
                                  kind: "site",
                                  id: row.id,
                                  name: row.name,
                                  logType: "access",
                                  lines: 100,
                                  keyword: "",
                                })
                              }
                            >
                              日志
                            </Button>
                            {canBackup ? (
                              <Button
                                size="small"
                                onClick={() =>
                                  void postJSON<{ taskId: string }>(sdk, "/resources/site-backup", {
                                    id: row.id,
                                  })
                                    .then((r) => handleTask(r, "网站备份"))
                                    .catch((e) => sdk.message.error(formatAPIError(e)))
                                }
                              >
                                备份
                              </Button>
                            ) : (
                              <Tooltip title="纯反代站点无本地项目目录，不支持站点文件备份">
                                <Button size="small" disabled>
                                  备份
                                </Button>
                              </Tooltip>
                            )}
                            {canRestart ? (
                              <Popconfirm
                                title={`确认重启 ${row.name}？`}
                                onConfirm={() =>
                                  void postJSON<{ taskId: string }>(
                                    sdk,
                                    "/resources/site-restart",
                                    { id: row.id },
                                  )
                                    .then((r) => handleTask(r, "网站重启"))
                                    .catch((e) => sdk.message.error(formatAPIError(e)))
                                }
                              >
                                <Button size="small">重启</Button>
                              </Popconfirm>
                            ) : (
                              <Tooltip title="仅已启用的 Node.js / Python / Go 项目支持重启；PHP / 静态 / 反代站点请使用启停">
                                <Button size="small" disabled>
                                  重启
                                </Button>
                              </Tooltip>
                            )}
                            <Popconfirm
                              title={`确认${row.status === "enabled" ? "停用" : "启用"} ${row.name}？`}
                              onConfirm={() =>
                                void postJSON<{ taskId: string }>(sdk, "/resources/site-control", {
                                  id: row.id,
                                  action: row.status === "enabled" ? "stop" : "start",
                                })
                                  .then((r) => handleTask(r, "网站状态切换"))
                                  .catch((e) => sdk.message.error(formatAPIError(e)))
                              }
                            >
                              <Button size="small">
                                {row.status === "enabled" ? "停用" : "启用"}
                              </Button>
                            </Popconfirm>
                          </Space>
                        );
                      },
                    },
                  ]}
                />

                {selectedSite && (
                  <Card size="small" title={`站点详情与备份历史 · ${selectedSite.name}`}>
                    <Descriptions size="small" column={{ xs: 1, sm: 3 }}>
                      <Descriptions.Item label="项目类型">{selectedSite.projectType}</Descriptions.Item>
                      <Descriptions.Item label="运行时">
                        {selectedSite.runtimeVersion || "-"} (端口 {selectedSite.runtimePort || "-"})
                      </Descriptions.Item>
                      <Descriptions.Item label="反代路由">
                        {selectedSite.proxyRoutes?.length || 0} 条
                      </Descriptions.Item>
                    </Descriptions>
                    <Table
                      size="small"
                      rowKey="id"
                      style={{ marginTop: 12 }}
                      pagination={false}
                      scroll={{ x: "max-content" }}
                      dataSource={siteBackups}
                      columns={[
                        { title: "归档文件", dataIndex: "fileName" },
                        { title: "存储", dataIndex: "storageType", width: 90 },
                        {
                          title: "大小",
                          dataIndex: "size",
                          width: 100,
                          render: (v: number) => formatBytes(v),
                        },
                        {
                          title: "操作",
                          width: 180,
                          render: (_, b) => (
                            <Space size={4}>
                              <Button
                                size="small"
                                onClick={() =>
                                  void postJSON<{ taskId: string }>(
                                    sdk,
                                    "/resources/site-backup-verify",
                                    { siteId: selectedSite.id, backupId: b.id },
                                  )
                                    .then((r) => handleTask(r, "归档校验"))
                                    .catch((e) => sdk.message.error(formatAPIError(e)))
                                }
                              >
                                校验
                              </Button>
                              <Button
                                size="small"
                                danger
                                onClick={() => confirmSiteRestore(selectedSite.id, b.id, selectedSite.name)}
                              >
                                恢复
                              </Button>
                            </Space>
                          ),
                        },
                      ]}
                    />
                  </Card>
                )}
              </Space>
            ),
          },
          {
            key: "containers",
            label: `容器 (${containers.length})`,
            children: (
              <Table
                size="small"
                rowKey="id"
                pagination={{ pageSize: 8 }}
                scroll={{ x: "max-content" }}
                rowClassName={(row) => highlightClass("containers", row.id)}
                dataSource={containers}
                columns={[
                  { title: "名称", dataIndex: "name" },
                  { title: "镜像", dataIndex: "kind", ellipsis: true },
                  { title: "状态", dataIndex: "status", width: 120 },
                  {
                    title: "操作",
                    width: 360,
                    render: (_, row) => (
                      <Space wrap size={4}>
                        <Button
                          size="small"
                          onClick={() =>
                            void fetchLogs({
                              kind: "container",
                              id: row.id,
                              name: row.name,
                              logType: "access",
                              lines: 100,
                              keyword: "",
                            })
                          }
                        >
                          日志
                        </Button>
                        <Button
                          size="small"
                          onClick={() =>
                            void sdk
                              .api(`/resources/container-stats?id=${encodeURIComponent(row.id)}`)
                              .then((stats) =>
                                openOutput(`容器资源采样 · ${row.name}`, JSON.stringify(stats, null, 2)),
                              )
                              .catch((e) => sdk.message.error(formatAPIError(e)))
                          }
                        >
                          采样
                        </Button>
                        <Popconfirm
                          title={`启动容器 ${row.name}？`}
                          onConfirm={() => controlContainer(row.id, "start")}
                        >
                          <Button size="small">启动</Button>
                        </Popconfirm>
                        <Popconfirm
                          title={`停止容器 ${row.name}？`}
                          onConfirm={() => controlContainer(row.id, "stop")}
                        >
                          <Button size="small" danger>
                            停止
                          </Button>
                        </Popconfirm>
                        <Popconfirm
                          title={`重启容器 ${row.name}？`}
                          onConfirm={() => controlContainer(row.id, "restart")}
                        >
                          <Button size="small">重启</Button>
                        </Popconfirm>
                      </Space>
                    ),
                  },
                ]}
              />
            ),
          },
          {
            key: "databases",
            label: `数据库 (${databases.length})`,
            children: (
              <Space direction="vertical" size="middle" style={{ width: "100%" }}>
                <Table
                  size="small"
                  rowKey="id"
                  pagination={{ pageSize: 6 }}
                  scroll={{ x: "max-content" }}
                  rowClassName={(row) => highlightClass("databases", row.id)}
                  dataSource={databases}
                  columns={[
                    { title: "名称", dataIndex: "name" },
                    { title: "引擎", dataIndex: "kind", width: 110 },
                    { title: "状态", dataIndex: "status", width: 110 },
                    {
                      title: "操作",
                      width: 140,
                      render: (_, row) => {
                        const supportsBackup =
                          row.kind === "mysql" || row.kind === "mariadb";
                        return supportsBackup ? (
                          <Button size="small" onClick={() => void loadDatabaseDetails(row.id)}>
                            库与备份
                          </Button>
                        ) : (
                          <Tooltip title="Host 数据库备份与恢复仅支持 MySQL / MariaDB 实例">
                            <Button size="small" disabled>
                              库与备份
                            </Button>
                          </Tooltip>
                        );
                      },
                    },
                  ]}
                />
                {selectedDBId && (
                  <Card size="small" title={`实例管理 · ${selectedDBId}`}>
                    <Space direction="vertical" style={{ width: "100%" }}>
                      {dbDetailError && (
                        <Alert type="warning" showIcon message={dbDetailError} />
                      )}
                      <Space wrap>
                        <Select
                          style={{ width: 220 }}
                          value={dbBackupName || undefined}
                          onChange={setDbBackupName}
                          placeholder="选择数据库名"
                          options={dbNames.map((n) => ({ value: n, label: n }))}
                        />
                        <Button
                          type="primary"
                          disabled={!dbBackupName}
                          onClick={() =>
                            void postJSON<{ taskId: string }>(sdk, "/resources/database-backup", {
                              serverId: selectedDBId,
                              name: dbBackupName,
                            })
                              .then((r) => handleTask(r, "数据库备份"))
                              .catch((e) => sdk.message.error(formatAPIError(e)))
                          }
                        >
                          备份选中库
                        </Button>
                      </Space>
                      <Table
                        size="small"
                        rowKey="id"
                        pagination={false}
                        scroll={{ x: "max-content" }}
                        dataSource={dbBackups}
                        columns={[
                          { title: "数据库", dataIndex: "database" },
                          { title: "文件名", dataIndex: "fileName" },
                          {
                            title: "大小",
                            dataIndex: "size",
                            render: (v: number) => formatBytes(v),
                          },
                          {
                            title: "恢复",
                            render: (_, row) => (
                              <Button
                                size="small"
                                danger
                                onClick={() => confirmDBRestore(selectedDBId, row.id, row.database)}
                              >
                                恢复
                              </Button>
                            ),
                          },
                        ]}
                      />
                    </Space>
                  </Card>
                )}
              </Space>
            ),
          },
          {
            key: "certificates",
            label: `证书 (${certificates.length})`,
            children: (
              <Space direction="vertical" size="middle" style={{ width: "100%" }}>
                <Space wrap>
                  <Select
                    style={{ width: 200 }}
                    placeholder="选择已授权站点"
                    value={bindSiteId || undefined}
                    onChange={setBindSiteId}
                    options={sites.map((s) => ({ value: s.id, label: s.name }))}
                  />
                  <Select
                    style={{ width: 240 }}
                    placeholder="选择证书"
                    value={bindCertId || undefined}
                    onChange={setBindCertId}
                    options={certificates.map((c) => ({ value: c.id, label: c.name || c.id }))}
                  />
                  <Space size={6}>
                    <Typography.Text className="cs-muted">强制 HTTPS</Typography.Text>
                    <Switch checked={forceHTTPS} onChange={setForceHTTPS} />
                  </Space>
                  <Button
                    type="primary"
                    disabled={!bindSiteId || !bindCertId}
                    onClick={() =>
                      void postJSON<{ taskId: string }>(sdk, "/resources/certificate-bind", {
                        siteId: bindSiteId,
                        certificateId: bindCertId,
                        forceHttps: forceHTTPS,
                      })
                        .then((r) => handleTask(r, "证书绑定"))
                        .catch((e) => sdk.message.error(formatAPIError(e)))
                    }
                  >
                    绑定证书到站点
                  </Button>
                </Space>
                <Table
                  size="small"
                  rowKey="id"
                  pagination={{ pageSize: 6 }}
                  scroll={{ x: "max-content" }}
                  rowClassName={(row) => highlightClass("certificates", row.id)}
                  dataSource={certificates}
                  columns={[
                    { title: "域名", dataIndex: "name", ellipsis: true },
                    { title: "来源", dataIndex: "kind", width: 100 },
                    { title: "到期时间", dataIndex: "expiresAt", width: 200 },
                    {
                      title: "续签",
                      width: 100,
                      render: (_, row) => (
                        <Button
                          size="small"
                          onClick={() =>
                            void postJSON<{ taskId: string }>(sdk, "/resources/certificate-renew", {
                              id: row.id,
                            })
                              .then((r) => handleTask(r, "证书续签"))
                              .catch((e) => sdk.message.error(formatAPIError(e)))
                          }
                        >
                          续签
                        </Button>
                      ),
                    },
                  ]}
                />
              </Space>
            ),
          },
          {
            key: "compose",
            label: `Compose (${composeList.length})`,
            children: (
              <Table
                size="small"
                rowKey="id"
                pagination={{ pageSize: 6 }}
                scroll={{ x: "max-content" }}
                dataSource={composeList}
                columns={[
                  { title: "项目名称", dataIndex: "name" },
                  { title: "来源", dataIndex: "kind", width: 120 },
                  {
                    title: "部署",
                    width: 120,
                    render: (_, row) => (
                      <Popconfirm
                        title={`部署 Compose 项目 ${row.name}？`}
                        onConfirm={() =>
                          void postJSON<{ taskId: string }>(sdk, "/resources/compose-deploy", {
                            name: row.name,
                          })
                            .then((r) => handleTask(r, "Compose 部署"))
                            .catch((e) => sdk.message.error(formatAPIError(e)))
                        }
                      >
                        <Button size="small" type="primary">
                          立即部署
                        </Button>
                      </Popconfirm>
                    ),
                  },
                ]}
              />
            ),
          },
          {
            key: "apps",
            label: `系统应用 (${apps.length})`,
            children: (
              <Table
                size="small"
                rowKey="id"
                pagination={{ pageSize: 8 }}
                scroll={{ x: "max-content" }}
                dataSource={apps}
                columns={[
                  { title: "ID", dataIndex: "id", width: 140 },
                  { title: "应用名", dataIndex: "name" },
                  {
                    title: "操作",
                    width: 340,
                    render: (_, row) => {
                      const isRuntime = RUNTIME_PROJECT_TYPES.has(row.id);
                      return (
                        <Space wrap size={4}>
                          <Button
                            size="small"
                            onClick={() =>
                              void sdk
                                .api(`/resources/app-status?id=${encodeURIComponent(row.id)}`)
                                .then((st) =>
                                  openOutput(`应用状态 · ${row.name}`, JSON.stringify(st, null, 2)),
                                )
                                .catch((e) => sdk.message.error(formatAPIError(e)))
                            }
                          >
                            状态
                          </Button>
                          {isRuntime ? (
                            <Tooltip title="运行时环境不支持系统服务启停，请使用「状态」查看安装情况">
                              <Space size={4}>
                                <Button size="small" disabled>
                                  启动
                                </Button>
                                <Button size="small" disabled>
                                  停止
                                </Button>
                                <Button size="small" disabled>
                                  重启
                                </Button>
                                <Button size="small" disabled>
                                  重载
                                </Button>
                              </Space>
                            </Tooltip>
                          ) : (
                            <>
                              <Popconfirm
                                title={`启动应用 ${row.name}？`}
                                onConfirm={() => controlApp(row.id, "start")}
                              >
                                <Button size="small">启动</Button>
                              </Popconfirm>
                              <Popconfirm
                                title={`停止应用 ${row.name}？`}
                                onConfirm={() => controlApp(row.id, "stop")}
                              >
                                <Button size="small" danger>
                                  停止
                                </Button>
                              </Popconfirm>
                              <Popconfirm
                                title={`重启应用 ${row.name}？`}
                                onConfirm={() => controlApp(row.id, "restart")}
                              >
                                <Button size="small">重启</Button>
                              </Popconfirm>
                              <Popconfirm
                                title={`重载应用 ${row.name}？`}
                                onConfirm={() => controlApp(row.id, "reload")}
                              >
                                <Button size="small">重载</Button>
                              </Popconfirm>
                            </>
                          )}
                        </Space>
                      );
                    },
                  },
                ]}
              />
            ),
          },
        ]}
      />

      {/* AntD Modal 默认通过 Portal 挂载到 body，这里必须显式包一层 data-panel-plugin，
          同时附加内联换行样式，确保作用域 CSS 与换行策略在任意模式下均生效。 */}
      <Modal
        open={outputOpen}
        title={outputTitle}
        onCancel={() => setOutputOpen(false)}
        footer={
          <Button type="primary" onClick={() => setOutputOpen(false)}>
            关闭
          </Button>
        }
        width={780}
      >
        <div data-panel-plugin="ezpanel-demo-plugin">
          <Space direction="vertical" size="small" style={{ width: "100%" }}>
            <Space wrap style={{ width: "100%", justifyContent: "space-between" }}>
              {logQuery ? (
                <Space wrap size="small">
                  {logQuery.kind === "site" && (
                    <Select<"access" | "error">
                      size="small"
                      style={{ width: 110 }}
                      value={logQuery.logType}
                      onChange={(logType) => {
                        const next = { ...logQuery, logType };
                        setLogQuery(next);
                        void fetchLogs(next);
                      }}
                      options={[
                        { value: "access", label: "访问日志" },
                        { value: "error", label: "错误日志" },
                      ]}
                    />
                  )}
                  <Select<number>
                    size="small"
                    style={{ width: 100 }}
                    value={logQuery.lines}
                    onChange={(lines) => {
                      const next = { ...logQuery, lines };
                      setLogQuery(next);
                      void fetchLogs(next);
                    }}
                    options={[
                      { value: 100, label: "100 行" },
                      { value: 200, label: "200 行" },
                      { value: 500, label: "500 行" },
                    ]}
                  />
                  {logQuery.kind === "site" && (
                    <Input
                      size="small"
                      style={{ width: 180 }}
                      value={logQuery.keyword}
                      onChange={(e) =>
                        setLogQuery({ ...logQuery, keyword: e.target.value })
                      }
                      onPressEnter={() => void fetchLogs(logQuery)}
                      placeholder="关键字过滤，回车查询"
                      allowClear
                    />
                  )}
                  <Button
                    size="small"
                    loading={logLoading}
                    onClick={() => void fetchLogs(logQuery)}
                  >
                    刷新日志
                  </Button>
                </Space>
              ) : (
                <span />
              )}
              <Space size="small">
                <Typography.Text className="cs-muted">自动换行</Typography.Text>
                <Switch size="small" checked={wrapLog} onChange={setWrapLog} />
              </Space>
            </Space>
            <pre
              className={`cs-pre cs-modal-pre${wrapLog ? "" : " cs-pre--nowrap"}`}
              style={{
                whiteSpace: wrapLog ? "pre-wrap" : "pre",
                overflowWrap: wrapLog ? "anywhere" : "normal",
                wordBreak: wrapLog ? "break-word" : "normal",
                overflow: "auto",
                maxHeight: "60vh",
                maxWidth: "100%",
                boxSizing: "border-box",
              }}
            >
              {outputContent}
            </pre>
          </Space>
        </div>
      </Modal>
    </Space>
  );
}
