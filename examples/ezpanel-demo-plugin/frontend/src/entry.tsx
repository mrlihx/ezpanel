import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import { Alert, Button, Card, Space, Spin, Tabs, Tag, Typography } from "antd";
import {
  AppstoreOutlined,
  CloudServerOutlined,
  FileOutlined,
  GithubOutlined,
  SafetyOutlined,
  SettingOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import type { PanelSDK } from "./panel-sdk";
import "./style.css";

// 使用 React.lazy 按功能域拆分为独立 ESM 分块，Vite 输出带内容哈希的相对路径资源，
// 在隔离 iframe 内按需加载，避免首屏阻塞。
const OverviewView = lazy(() => import("./views/OverviewView"));
const ResourcesView = lazy(() => import("./views/ResourcesView"));
const FilesView = lazy(() => import("./views/FilesView"));
const AutomationView = lazy(() => import("./views/AutomationView"));
const ProvidersView = lazy(() => import("./views/ProvidersView"));
const SecurityView = lazy(() => import("./views/SecurityView"));

type TabKey =
  | "overview"
  | "resources"
  | "files"
  | "automation"
  | "providers"
  | "security";

const VALID_TABS: TabKey[] = [
  "overview",
  "resources",
  "files",
  "automation",
  "providers",
  "security",
];

function resolveTab(route?: string): TabKey {
  const cleaned = (route || "").replace(/^\/+/, "").split("/")[0] as TabKey;
  return VALID_TABS.includes(cleaned) ? cleaned : "overview";
}

/**
 * EzPanel 插件前端入口组件。
 * 接收面板注入的 PanelSDK（含 theme、route、context、api、navigate、选择器及通知控件）。
 */
export default function CapabilityShowcase({ sdk }: { sdk: PanelSDK }) {
  // 根据二级菜单子路由（sdk.route）或宿主跳转上下文（siteId / filePaths / resource）自动定位初始视图
  const initialTab = useMemo<TabKey>(() => {
    if (sdk.route && sdk.route !== "/") {
      return resolveTab(sdk.route);
    }
    if (sdk.context?.filePaths?.length) return "files";
    if (sdk.context?.resource || sdk.context?.siteId) return "resources";
    return "overview";
  }, [sdk.route, sdk.context]);

  const [activeTab, setActiveTab] = useState<TabKey>(initialTab);

  useEffect(() => {
    setActiveTab(initialTab);
  }, [initialTab]);

  // 使用 sdk.navigate 同步面板地址栏子路径，不自行创建第二个 BrowserRouter
  const switchTab = (key: string) => {
    const next = VALID_TABS.includes(key as TabKey) ? (key as TabKey) : "overview";
    setActiveTab(next);
    sdk.navigate(`/${next}`);
  };

  const tabItems = [
    {
      key: "overview",
      label: (
        <Space size={6}>
          <AppstoreOutlined />
          总览与 SDK
        </Space>
      ),
    },
    {
      key: "resources",
      label: (
        <Space size={6}>
          <CloudServerOutlined />
          资源与运维
        </Space>
      ),
    },
    {
      key: "files",
      label: (
        <Space size={6}>
          <FileOutlined />
          文件与存储
        </Space>
      ),
    },
    {
      key: "automation",
      label: (
        <Space size={6}>
          <SettingOutlined />
          任务与事件
        </Space>
      ),
    },
    {
      key: "providers",
      label: (
        <Space size={6}>
          <SafetyOutlined />
          扩展渠道与钩子
        </Space>
      ),
    },
    {
      key: "security",
      label: (
        <Space size={6}>
          <ToolOutlined />
          凭据与审计
        </Space>
      ),
    },
  ];

  return (
    <div className="cs-root">
      <div className="cs-header">
        <div>
          <div className="cs-header-title">
            <Typography.Title level={4} style={{ margin: 0 }}>
              EzPanel演示插件
            </Typography.Title>
            <Tag color="blue">v0.2.1</Tag>
            <Tag>by EzPanel</Tag>
            <Button
              type="link"
              size="small"
              icon={<GithubOutlined />}
              href="https://github.com/mrlihx/ezpanel"
              target="_blank"
              rel="noopener noreferrer"
              style={{ paddingInline: 4 }}
            >
              GitHub
            </Button>
          </div>
          <Typography.Text className="cs-muted">
            SDK v{sdk.sdkVersion} · 隔离前端（uiMode: isolated · openMode: embed）· Linux systemd 沙箱 · 覆盖面板全量能力
          </Typography.Text>
        </div>

        <div className="cs-header-meta">
          <Tag color="blue">{sdk.pluginId}</Tag>
          <Tag color={sdk.dark ? "geekblue" : "gold"}>
            {sdk.dark ? "深色主题" : "浅色主题"}
          </Tag>
          <Tag>route: {sdk.route || "/"}</Tag>
          {sdk.context?.siteId && <Tag color="green">siteId: {sdk.context.siteId}</Tag>}
          {sdk.context?.resource && (
            <Tag color="purple">
              resource: {sdk.context.resource.kind}/{sdk.context.resource.name || sdk.context.resource.id}
            </Tag>
          )}
          {sdk.context?.filePaths?.length ? (
            <Tag color="cyan">filePaths: {sdk.context.filePaths.length} 项</Tag>
          ) : null}
        </div>
      </div>

      {sdk.context?.siteId && activeTab !== "resources" && (
        <Alert
          type="info"
          showIcon
          message={`当前携带站点上下文（siteId: ${sdk.context.siteId}）`}
          action={
            <Button size="small" type="primary" onClick={() => switchTab("resources")}>
              跳转到站点运维
            </Button>
          }
        />
      )}

      <Card size="small">
        <Tabs
          activeKey={activeTab}
          onChange={switchTab}
          items={tabItems}
          destroyInactiveTabPane={false}
        />
        <Suspense
          fallback={
            <div className="cs-loading">
              <Spin tip="正在按需加载功能分块..." />
            </div>
          }
        >
          {activeTab === "overview" && <OverviewView sdk={sdk} />}
          {activeTab === "resources" && <ResourcesView sdk={sdk} />}
          {activeTab === "files" && <FilesView sdk={sdk} />}
          {activeTab === "automation" && <AutomationView sdk={sdk} />}
          {activeTab === "providers" && <ProvidersView sdk={sdk} />}
          {activeTab === "security" && <SecurityView sdk={sdk} />}
        </Suspense>
      </Card>
    </div>
  );
}

/**
 * 可选导出 dispose()：供宿主页面卸载或隔离容器销毁时清理模块级常驻资源。
 * 必须保证可重复安全调用；本插件所有异步请求与订阅均已在组件 effect 中通过 AbortController 清理。
 */
export function dispose() {}
