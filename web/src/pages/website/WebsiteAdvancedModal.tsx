import { lazy, Suspense, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Grid,
  Modal,
  Select,
  Space,
  Spin,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  saveWebsiteDefaultPage,
  saveWebsiteDefaultSite,
  saveWebsiteTLSProtocols,
  websiteAdvanced,
  type DefaultPageKind,
} from '../../api/website';
import { phpCLI, setDefaultPHPCLI } from '../../api/php';

const CodeEditor = lazy(() => import('../../components/CodeEditor'));

const pageTabItems: { key: DefaultPageKind; label: string; desc: string }[] = [
  {
    key: 'index',
    label: '新建网站默认页面',
    desc: '新建静态或 PHP 网站时自动写入根目录的 index.html 默认模板。',
  },
  {
    key: '404',
    label: '404错误页',
    desc: '访问站点不存在的路径返回 404 时展示的默认错误页面。',
  },
  {
    key: 'not_found',
    label: '网站不存在页',
    desc: '未设置默认站点时，访问未绑定域名或服务器 IP（80 端口）展示的页面。',
  },
  {
    key: 'stopped',
    label: '网站停用页',
    desc: '访问已被管理员停用的网站时展示的提示页面（HTTP 503）。',
  },
];

const tlsOptions = [
  { label: 'TLSv1', value: 'TLSv1' },
  { label: 'TLSv1.1', value: 'TLSv1.1' },
  { label: 'TLSv1.2（推荐）', value: 'TLSv1.2' },
  { label: 'TLSv1.3（推荐）', value: 'TLSv1.3' },
];

export default function WebsiteAdvancedModal({
  onClose,
}: {
  onClose: () => void;
}) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const screens = Grid.useBreakpoint();
  const [menu, setMenu] = useState('pages');
  const [pageKind, setPageKind] = useState<DefaultPageKind>('index');
  const [pageDrafts, setPageDrafts] = useState<
    Partial<Record<DefaultPageKind, string>>
  >({});
  const [defaultSite, setDefaultSite] = useState<string>();
  const [tls, setTls] = useState<string[]>();
  const [cliId, setCliId] = useState<string>();
  const [saving, setSaving] = useState(false);

  const advanced = useQuery({
    queryKey: ['website-advanced'],
    queryFn: websiteAdvanced,
  });
  const cli = useQuery({
    queryKey: ['php-cli'],
    queryFn: phpCLI,
  });

  const defaultSiteValue = defaultSite ?? advanced.data?.defaultSiteId ?? '';
  const cliValue = cliId ?? cli.data?.defaultId ?? undefined;
  const currentPageContent =
    pageDrafts[pageKind] ?? advanced.data?.pages[pageKind] ?? '';
  const currentPageMeta =
    pageTabItems.find((item) => item.key === pageKind) ?? pageTabItems[0];

  async function handleSavePage(reset = false) {
    setSaving(true);
    try {
      const res = await saveWebsiteDefaultPage(
        pageKind,
        reset ? '' : currentPageContent,
        reset,
      );
      setPageDrafts((prev) => ({ ...prev, [pageKind]: res.content }));
      await client.invalidateQueries({ queryKey: ['website-advanced'] });
      void message.success(reset ? '已恢复默认页面模板' : '默认页面已保存');
    } finally {
      setSaving(false);
    }
  }

  async function handleSaveDefaultSite() {
    setSaving(true);
    try {
      await saveWebsiteDefaultSite(defaultSiteValue);
      await client.invalidateQueries({ queryKey: ['website-advanced'] });
      void message.success(
        defaultSiteValue
          ? '默认站点已设置并重载 OpenResty'
          : '已取消默认站点（未绑定域名访问 443 将自动拒绝握手防窜站）',
      );
    } finally {
      setSaving(false);
    }
  }

  async function handleSaveCLI() {
    if (!cliValue) return;
    setSaving(true);
    try {
      const next = await setDefaultPHPCLI(cliValue);
      client.setQueryData(['php-cli'], next);
      const picked = next.installed.find((item) => item.id === cliValue);
      void message.success(
        `已将系统默认 PHP CLI 切换为 ${picked ? `PHP-${picked.branch}` : cliValue}`,
      );
    } finally {
      setSaving(false);
    }
  }

  async function handleSaveTLS() {
    const selected = tls ??
      advanced.data?.tlsProtocols ?? ['TLSv1.2', 'TLSv1.3'];
    if (selected.length === 0) {
      void message.warning('请至少选择一个 TLS 协议版本');
      return;
    }
    setSaving(true);
    try {
      await saveWebsiteTLSProtocols(selected);
      await client.invalidateQueries({ queryKey: ['website-advanced'] });
      void message.success('全局 TLS 协议已保存并重新加载全部 SSL 站点');
    } finally {
      setSaving(false);
    }
  }

  const tlsValue = tls ?? advanced.data?.tlsProtocols ?? ['TLSv1.2', 'TLSv1.3'];
  const hasWeakTLS = tlsValue.includes('TLSv1') || tlsValue.includes('TLSv1.1');

  return (
    <Modal
      open
      centered
      title="高级设置"
      width={960}
      styles={{
        body: {
          height: screens.md ? 520 : undefined,
          maxHeight: screens.md ? 520 : '72dvh',
          overflowY: 'auto',
        },
      }}
      footer={null}
      maskClosable={false}
      onCancel={onClose}
      destroyOnHidden
    >
      <Tabs
        tabPosition={screens.md ? 'left' : 'top'}
        activeKey={menu}
        onChange={setMenu}
        items={[
          {
            key: 'pages',
            label: '修改默认页面',
            children: (
              <Space
                direction="vertical"
                size="middle"
                style={{ width: '100%' }}
              >
                <Tabs
                  type="card"
                  size="small"
                  activeKey={pageKind}
                  onChange={(key) => setPageKind(key as DefaultPageKind)}
                  items={pageTabItems.map((item) => ({
                    key: item.key,
                    label: item.label,
                  }))}
                  style={{ marginBottom: 0 }}
                />
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {currentPageMeta.desc}
                </Typography.Text>
                <div style={{ height: 340 }}>
                  {advanced.isPending ? (
                    <Spin />
                  ) : (
                    <Suspense fallback={<Spin />}>
                      <CodeEditor
                        path={`default-page-${pageKind}.html`}
                        value={currentPageContent}
                        onChange={(value) =>
                          setPageDrafts((prev) => ({
                            ...prev,
                            [pageKind]: value,
                          }))
                        }
                      />
                    </Suspense>
                  )}
                </div>
                <Space>
                  <Button
                    type="primary"
                    loading={saving}
                    onClick={() => void handleSavePage(false)}
                  >
                    保存
                  </Button>
                  <Button
                    disabled={saving}
                    onClick={() =>
                      modal.confirm({
                        title: `恢复「${currentPageMeta.label}」为系统默认模板？`,
                        onOk: () => handleSavePage(true),
                      })
                    }
                  >
                    恢复默认
                  </Button>
                </Space>
              </Space>
            ),
          },
          {
            key: 'default-site',
            label: '默认站点',
            children: (
              <Space
                direction="vertical"
                size="middle"
                style={{ width: '100%' }}
              >
                <Alert
                  type="info"
                  showIcon
                  message="设置默认站点后，所有未绑定的域名和服务器 IP 都将定向到该默认站点。"
                  description="不设置默认站点时，80 端口将展示「网站不存在页」，443 端口自动开启 ssl_reject_handshake 防窜站（拒绝未知域名 TLS 握手，防止窜到其他 HTTPS 站点）。"
                />
                <Space align="center" wrap>
                  <Typography.Text>默认站点：</Typography.Text>
                  <Select
                    style={{ width: 320, maxWidth: '100%' }}
                    loading={advanced.isPending}
                    value={defaultSiteValue}
                    onChange={setDefaultSite}
                    options={[
                      { value: '', label: '未设置默认站点（推荐，防窜站）' },
                      ...(advanced.data?.sites ?? []).map((site) => ({
                        value: site.id,
                        disabled: !site.enabled,
                        label: `${site.name}${!site.enabled ? '（已停用）' : site.sslEnabled ? '（含 SSL）' : ''}`,
                      })),
                    ]}
                  />
                </Space>
                <Button
                  type="primary"
                  loading={saving}
                  onClick={() => void handleSaveDefaultSite()}
                >
                  保存
                </Button>
              </Space>
            ),
          },
          {
            key: 'php-cli',
            label: 'PHP命令行版本',
            children: (
              <Space
                direction="vertical"
                size="middle"
                style={{ width: '100%' }}
              >
                <Alert
                  type="info"
                  showIcon
                  message="设置宿主机系统默认 php 命令（/usr/local/bin/php）指向的 PHP 版本。"
                  description="在终端或计划任务中运行 php 命令时，将自动以 www-data 身份在所选 PHP 容器内执行；也可直接使用各版本独立命令（如 php82、php83）。"
                />
                {cli.isPending ? (
                  <Spin />
                ) : !cli.data?.installed.length ? (
                  <Alert
                    type="warning"
                    showIcon
                    message="尚未安装任何 PHP 版本，请先在应用商店安装 PHP。"
                  />
                ) : (
                  <>
                    <Space align="center" wrap>
                      <Typography.Text>默认 CLI 版本：</Typography.Text>
                      <Select
                        style={{ width: 260, maxWidth: '100%' }}
                        value={cliValue}
                        onChange={setCliId}
                        options={cli.data.installed.map((item) => ({
                          value: item.id,
                          label: `PHP-${item.branch}${item.version ? ` (${item.version})` : ''} · ${item.command}`,
                        }))}
                      />
                    </Space>
                    <Space size={[6, 6]} wrap>
                      <Typography.Text type="secondary">
                        已安装独立命令：
                      </Typography.Text>
                      {cli.data.installed.map((item) => (
                        <Tag
                          key={item.id}
                          color={
                            item.id === cli.data.defaultId ? 'blue' : 'default'
                          }
                        >
                          {item.command}
                          {item.id === cli.data.defaultId ? '（默认）' : ''}
                        </Tag>
                      ))}
                    </Space>
                    <Button
                      type="primary"
                      loading={saving}
                      disabled={!cliValue}
                      onClick={() => void handleSaveCLI()}
                    >
                      保存
                    </Button>
                  </>
                )}
              </Space>
            ),
          },
          {
            key: 'tls',
            label: 'TLS设置',
            children: (
              <Space
                direction="vertical"
                size="middle"
                style={{ width: '100%' }}
              >
                <Typography.Text type="secondary">
                  全局控制所有启用 SSL 的网站允许使用的 TLS
                  协议版本，保存后自动重载 OpenResty。
                </Typography.Text>
                <Checkbox.Group
                  options={tlsOptions}
                  value={tlsValue}
                  onChange={(values) => setTls(values as string[])}
                />
                {hasWeakTLS && (
                  <Alert
                    type="warning"
                    showIcon
                    message="TLSv1 与 TLSv1.1 已被主流浏览器废弃且存在已知安全漏洞，仅建议在兼容老旧客户端时临时启用。"
                  />
                )}
                <Button
                  type="primary"
                  loading={saving}
                  disabled={tlsValue.length === 0}
                  onClick={() => void handleSaveTLS()}
                >
                  保存
                </Button>
              </Space>
            ),
          },
        ]}
      />
    </Modal>
  );
}
