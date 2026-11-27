import { useParams, useSearchParams } from 'react-router-dom';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Flex,
  Form,
  Input,
  InputNumber,
  Select,
  Skeleton,
  Space,
  Switch,
  Typography,
} from 'antd';
import { me, saveSSOConfig, ssoConfig, totpStatus } from '../../api/auth';
import { usePluginList } from '../../api/plugin';
import {
  panelSettings,
  saveBasic,
  saveNetwork,
  savePanelMenus,
  type PanelSettings,
  type NetworkSettings,
} from '../../api/settings';
import { pages } from '../../layouts/menuItems';
import { useAuth } from '../../stores/auth';
import { useTheme } from '../../stores/theme';
import { LAYOUT } from '../../styles/tokens';
import BackupChannels from './BackupChannels';
import PluginCredentialSettings from './PluginCredentialSettings';
import { SSOModal } from './SSOSettings';
import SectionTabs from '../../components/SectionTabs';
import AccountModal from './AccountModal';
import AlertNotifications from './AlertNotifications';
import MenuVisibilityModal from './MenuVisibilityModal';
import PanelSSLModal from './PanelSSLModal';
import {
  formatUnauthSummary,
  parseUserAgentList,
  UnauthResponseModal,
  UserAgentModal,
} from './SecurityModals';
import TOTPModal from './TOTPModal';
export default function SettingsPage() {
  const [params] = useSearchParams();
  const { section, alertTab } = useParams();
  const legacyAlert = params.get('alertTab');
  const { tab, loading, invalid, selectTab } = useRoutedTab(
    'settings',
    '/settings',
    section,
    ['basic', 'network', 'backup', 'credentials', 'alerts'],
    {
      keys: section === 'alerts' ? ['tab'] : ['tab', 'alertTab'],
      tab: params.get('tab'),
      suffix:
        params.get('tab') === 'alerts' &&
        legacyAlert &&
        ['rules', 'channels', 'logs'].includes(legacyAlert)
          ? `/${legacyAlert}`
          : undefined,
    },
  );
  const query = useQuery({
    queryKey: ['panel-settings'],
    queryFn: panelSettings,
  });
  const totpQuery = useQuery({
    queryKey: ['panel-totp'],
    queryFn: totpStatus,
  });
  const ssoQuery = useQuery({
    queryKey: ['panel-sso'],
    queryFn: ssoConfig,
  });
  const client = useQueryClient();
  const { message, modal } = App.useApp();
  const [business, setBusiness] = useState(false);
  const [accountKind, setAccountKind] = useState<
    'username' | 'password' | null
  >(null);
  const [menuModalOpen, setMenuModalOpen] = useState(false);
  const [menuSaving, setMenuSaving] = useState(false);
  const [sslOpen, setSSLOpen] = useState(false);
  const [totpModalMode, setTotpModalMode] = useState<
    'enable' | 'disable' | null
  >(null);
  const [ssoModal, setSsoModal] = useState<{
    open: boolean;
    initialEnabled?: boolean;
  }>({ open: false });
  const [ssoSaving, setSsoSaving] = useState(false);
  const [uaModalOpen, setUaModalOpen] = useState(false);
  const [unauthModalOpen, setUnauthModalOpen] = useState(false);
  const [networkForm] = Form.useForm();
  const setUser = useAuth((s) => s.setUser);
  const { mode, setMode } = useTheme();
  const pluginsQuery = usePluginList();
  async function save(job: () => Promise<PanelSettings>) {
    setBusiness(true);
    try {
      const data = await job();
      client.setQueryData(['panel-settings'], data);
      setUser(await me());
      void client.invalidateQueries({ queryKey: ['app-versions'] });
      void client.invalidateQueries({ queryKey: ['software-catalog'] });
      message.success('设置已保存并生效');
    } finally {
      setBusiness(false);
    }
  }
  async function applyNetwork(values: NetworkSettings) {
    setBusiness(true);
    try {
      const result = await saveNetwork(values);
      if (result.loginRequired) {
        window.location.assign(result.loginUrl);
        return;
      }
      await query.refetch();
      setSSLOpen(false);
      void message.success('安全设置已保存');
    } finally {
      setBusiness(false);
    }
  }
  if (invalid || (alertTab && section !== 'alerts'))
    return <RouteNotFound back="/settings" />;
  if (query.isPending || loading) return <Skeleton active />;
  if (query.error)
    return (
      <Alert
        type="error"
        message={query.error.message}
        action={<Button onClick={() => void query.refetch()}>重试</Button>}
      />
    );
  const data = query.data!;
  const menuLabelMap = new Map<string, string>([
    ...pages.map((p) => [p.key, p.label] as [string, string]),
    ...(pluginsQuery.data ?? [])
      .filter((p) => p.menu)
      .map((p) => [`/p/${p.id}`, p.menu!.title] as [string, string]),
  ]);
  const hiddenMenuLabels = (data.hiddenMenus ?? [])
    .map((key) => menuLabelMap.get(key) ?? key)
    .join('、');
  const menuSummaryText = [
    data.menuGroups?.length ? `已设 ${data.menuGroups.length} 个分组` : '',
    hiddenMenuLabels ? `已隐藏：${hiddenMenuLabels}` : '',
  ]
    .filter(Boolean)
    .join('；');
  async function updatePanelMenus(payload: {
    hiddenMenus: string[];
    menuOrder: string[];
    menuGroups: PanelSettings['menuGroups'];
  }) {
    setMenuSaving(true);
    try {
      const updated = await savePanelMenus(payload);
      client.setQueryData(['panel-settings'], updated);
      message.success('菜单设置已保存');
      setMenuModalOpen(false);
    } finally {
      setMenuSaving(false);
    }
  }
  const formProps = {
    layout: 'horizontal' as const,
    labelCol: { flex: LAYOUT.formLabelWidth },
    wrapperCol: { flex: 1 },
    colon: false,
    style: { maxWidth: LAYOUT.formMaxWidth },
  };
  return (
    <>
      <SectionTabs
        activeKey={tab}
        onChange={selectTab}
        cardContent
        items={[
          {
            key: 'basic',
            label: '基本设置',
            children: (
              <Form
                {...formProps}
                key={query.dataUpdatedAt}
                initialValues={data}
                onFinish={(v) => void save(() => saveBasic(v))}
              >
                <Form.Item
                  label="面板别名"
                  name="alias"
                  rules={[{ required: true, max: 32 }]}
                >
                  <Input maxLength={32} />
                </Form.Item>
                <Form.Item label="面板主题">
                  <Select
                    aria-label="面板主题"
                    value={mode}
                    onChange={setMode}
                    options={[
                      { value: 'system', label: '跟随系统' },
                      { value: 'light', label: '浅色' },
                      { value: 'dark', label: '深色' },
                    ]}
                  />
                </Form.Item>
                <Form.Item
                  label="超时时间"
                  name="idleMinutes"
                  rules={[{ required: true }]}
                  getValueProps={(value: number) => ({ value: value / 60 })}
                  normalize={(value: number | null) =>
                    value == null ? value : Math.round(value * 60)
                  }
                  extra="超过指定时间没有操作面板，自动退出登录；监控刷新不计入操作。支持1–168小时，按整小时设置；单次登录最长30天，到期需重新登录。"
                >
                  <InputNumber
                    min={1}
                    max={168}
                    precision={0}
                    step={1}
                    addonAfter="小时"
                  />
                </Form.Item>
                <Form.Item
                  label="默认建站目录"
                  name="siteRoot"
                  rules={[{ required: true }]}
                  extra="用于新建网站，已有站点目录保持原位置。"
                >
                  <Input placeholder="/www/wwwroot" />
                </Form.Item>
                <Form.Item
                  label="默认备份目录"
                  name="backupRoot"
                  rules={[{ required: true }]}
                  extra="新备份分别保存到 database、site、software 子目录；已有备份保持原位置。"
                >
                  <Input placeholder="/www/backup" />
                </Form.Item>
                <Form.Item label="修改面板菜单">
                  <Space.Compact style={{ width: '100%' }}>
                    <Input
                      value={menuSummaryText}
                      placeholder="默认布局，未隐藏任何菜单"
                      readOnly
                      aria-label="修改面板菜单"
                    />
                    <Button onClick={() => setMenuModalOpen(true)}>设置</Button>
                  </Space.Compact>
                </Form.Item>
                <Form.Item label=" ">
                  <Button type="primary" htmlType="submit" loading={business}>
                    保存
                  </Button>
                </Form.Item>
              </Form>
            ),
          },
          {
            key: 'network',
            label: '安全设置',
            children: (
              <Form
                {...formProps}
                key={`network-${query.dataUpdatedAt}`}
                form={networkForm}
                initialValues={data}
              >
                <Form.Item label="当前访问地址">
                  <Typography.Text copyable>
                    {
                      new URL(
                        `/${data.entry}`,
                        data.publicUrl || window.location.origin,
                      ).href
                    }
                  </Typography.Text>
                </Form.Item>
                <Form.Item label="面板账号">
                  <Space.Compact style={{ width: '100%' }}>
                    <Input
                      value={data.username}
                      readOnly
                      aria-label="面板账号"
                    />
                    <Button onClick={() => setAccountKind('username')}>
                      设置
                    </Button>
                  </Space.Compact>
                </Form.Item>
                <Form.Item label="面板密码">
                  <Space.Compact style={{ width: '100%' }}>
                    <Input value="••••••••" readOnly aria-label="面板密码" />
                    <Button onClick={() => setAccountKind('password')}>
                      设置
                    </Button>
                  </Space.Compact>
                </Form.Item>
                <Form.Item label="动态口令认证">
                  <Switch
                    checked={!!totpQuery.data?.enabled}
                    loading={totpQuery.isPending}
                    aria-label="动态口令认证"
                    onChange={(checked) =>
                      setTotpModalMode(checked ? 'enable' : 'disable')
                    }
                  />
                </Form.Item>
                <Form.Item label="单点登录">
                  <Flex align="center" gap={12}>
                    <Switch
                      checked={!!ssoQuery.data?.enabled}
                      loading={ssoQuery.isPending || ssoSaving}
                      aria-label="单点登录"
                      onChange={async (checked) => {
                        const sso = ssoQuery.data;
                        if (!sso) {
                          setSsoModal({ open: true, initialEnabled: checked });
                          return;
                        }
                        if (
                          checked &&
                          (!sso.pluginId ||
                            !sso.issuer ||
                            !sso.clientId ||
                            !sso.subject ||
                            !sso.secretSet)
                        ) {
                          setSsoModal({ open: true, initialEnabled: true });
                          return;
                        }
                        setSsoSaving(true);
                        try {
                          const updated = await saveSSOConfig({
                            enabled: checked,
                            pluginId: sso.pluginId,
                            issuer: sso.issuer,
                            clientId: sso.clientId,
                            subject: sso.subject,
                          });
                          client.setQueryData(['panel-sso'], updated);
                          void client.invalidateQueries({
                            queryKey: ['panel-branding'],
                          });
                          message.success(
                            checked ? '单点登录已开启' : '单点登录已关闭',
                          );
                        } catch {
                          setSsoModal({ open: true, initialEnabled: checked });
                        } finally {
                          setSsoSaving(false);
                        }
                      }}
                    />
                    {ssoQuery.data?.enabled && (
                      <Typography.Link
                        onClick={() => setSsoModal({ open: true })}
                      >
                        设置
                      </Typography.Link>
                    )}
                  </Flex>
                </Form.Item>
                <Form.Item label="面板端口" required>
                  <Space.Compact style={{ width: '100%' }}>
                    <Form.Item
                      name="port"
                      noStyle
                      rules={[
                        {
                          required: true,
                          type: 'integer',
                          min: 1,
                          max: 65535,
                        },
                      ]}
                    >
                      <InputNumber
                        style={{ flex: 1 }}
                        min={1}
                        max={65535}
                        precision={0}
                      />
                    </Form.Item>
                    <Button
                      loading={business}
                      onClick={() => {
                        void networkForm
                          .validateFields(['port'])
                          .then((v) => {
                            modal.confirm({
                              title: '修改面板端口？',
                              content:
                                '面板将按新端口重新监听，请先确认防火墙已放行。',
                              onOk: () =>
                                applyNetwork({
                                  boundDomain: data.boundDomain,
                                  sslEnabled: data.sslEnabled,
                                  certificate: data.certificate,
                                  certificateId: data.certificateId,
                                  port: v.port,
                                  entry: data.entry,
                                }),
                            });
                          })
                          .catch(() => {});
                      }}
                    >
                      设置
                    </Button>
                  </Space.Compact>
                </Form.Item>
                <Form.Item label="面板入口" required>
                  <Space.Compact style={{ width: '100%' }}>
                    <Form.Item
                      name="entry"
                      noStyle
                      rules={[
                        {
                          required: true,
                          pattern: /^[a-zA-Z0-9]{5,}$/,
                          message: '请输入至少 5 位字母或数字',
                        },
                      ]}
                    >
                      <Input addonBefore="/" autoComplete="off" />
                    </Form.Item>
                    <Button
                      loading={business}
                      onClick={() => {
                        void networkForm
                          .validateFields(['entry'])
                          .then((v) => {
                            modal.confirm({
                              title: '修改面板入口？',
                              content: '修改后请使用新的安全入口路径访问面板。',
                              onOk: () =>
                                applyNetwork({
                                  boundDomain: data.boundDomain,
                                  sslEnabled: data.sslEnabled,
                                  certificate: data.certificate,
                                  certificateId: data.certificateId,
                                  port: data.port,
                                  entry: v.entry,
                                }),
                            });
                          })
                          .catch(() => {});
                      }}
                    >
                      设置
                    </Button>
                  </Space.Compact>
                </Form.Item>
                <Form.Item
                  label="绑定域名"
                  extra="不含协议、端口或路径。绑定后使用该域名访问；留空解除绑定。"
                >
                  <Space.Compact style={{ width: '100%' }}>
                    <Form.Item name="boundDomain" noStyle>
                      <Input placeholder="panel.example.com" />
                    </Form.Item>
                    <Button
                      loading={business}
                      onClick={() => {
                        const nextDomain =
                          networkForm.getFieldValue('boundDomain') ?? '';
                        modal.confirm({
                          title: '应用绑定域名设置？',
                          content:
                            '面板访问入口将按新域名切换，请先确认域名解析已生效。',
                          onOk: () =>
                            applyNetwork({
                              boundDomain: nextDomain,
                              sslEnabled: data.sslEnabled,
                              certificate: data.certificate,
                              certificateId: data.certificateId,
                              port: data.port,
                              entry: data.entry,
                            }),
                        });
                      }}
                    >
                      设置
                    </Button>
                  </Space.Compact>
                </Form.Item>
                {(!!data.boundDomain || data.sslEnabled) && (
                  <Form.Item label="面板SSL">
                    <Flex align="center" gap={12}>
                      <Switch
                        checked={data.sslEnabled}
                        loading={business}
                        aria-label="面板SSL"
                        onChange={(checked) => {
                          if (checked) {
                            setSSLOpen(true);
                            return;
                          }
                          modal.confirm({
                            title: '关闭面板 SSL？',
                            content:
                              '关闭后将使用 HTTP 访问面板，并需重新登录。',
                            onOk: () =>
                              applyNetwork({
                                boundDomain: data.boundDomain,
                                sslEnabled: false,
                                certificate: '',
                                certificateId: '',
                                port: data.port,
                                entry: data.entry,
                              }),
                          });
                        }}
                      />
                      {data.sslEnabled && (
                        <Typography.Link onClick={() => setSSLOpen(true)}>
                          面板SSL配置
                        </Typography.Link>
                      )}
                    </Flex>
                  </Form.Item>
                )}
                <Form.Item label="User-Agent 限制">
                  <Flex align="center" gap={12}>
                    <Switch
                      checked={data.userAgentEnabled}
                      loading={business}
                      aria-label="User-Agent 限制"
                      onChange={(checked) => {
                        if (
                          checked &&
                          parseUserAgentList(data.userAgentAllow ?? '')
                            .length === 0
                        ) {
                          setUaModalOpen(true);
                          return;
                        }
                        void applyNetwork({
                          boundDomain: data.boundDomain,
                          sslEnabled: data.sslEnabled,
                          certificate: data.certificate,
                          certificateId: data.certificateId,
                          port: data.port,
                          entry: data.entry,
                          userAgentEnabled: checked,
                        });
                      }}
                    />
                    <Typography.Link onClick={() => setUaModalOpen(true)}>
                      设置
                    </Typography.Link>
                  </Flex>
                </Form.Item>
                <Form.Item label="未认证响应状态" style={{ marginBottom: 0 }}>
                  <Space.Compact style={{ width: '100%' }}>
                    <Input
                      readOnly
                      value={formatUnauthSummary(
                        data.unauthStatus,
                        data.unauthHtml,
                      )}
                      aria-label="未认证响应状态"
                    />
                    <Button onClick={() => setUnauthModalOpen(true)}>
                      设置
                    </Button>
                  </Space.Compact>
                </Form.Item>
              </Form>
            ),
          },
          { key: 'backup', label: '备份设置', children: <BackupChannels /> },
          {
            key: 'credentials',
            label: '外部凭据',
            children: tab === 'credentials' && <PluginCredentialSettings />,
          },
          {
            key: 'alerts',
            label: '告警通知',
            children: <AlertNotifications active={tab === 'alerts'} />,
          },
        ]}
      />
      <MenuVisibilityModal
        open={menuModalOpen}
        hiddenMenus={data.hiddenMenus ?? []}
        menuOrder={data.menuOrder ?? []}
        menuGroups={data.menuGroups ?? []}
        saving={menuSaving}
        onSave={updatePanelMenus}
        onClose={() => setMenuModalOpen(false)}
      />
      {accountKind && (
        <AccountModal
          kind={accountKind}
          username={data.username}
          onClose={() => setAccountKind(null)}
        />
      )}
      {sslOpen && (
        <PanelSSLModal
          settings={data}
          boundDomain={
            networkForm.getFieldValue('boundDomain')?.trim() ?? data.boundDomain
          }
          busy={business}
          onApply={applyNetwork}
          onClose={() => setSSLOpen(false)}
        />
      )}
      {totpModalMode && (
        <TOTPModal
          mode={totpModalMode}
          onSuccess={async () => {
            await Promise.all([
              totpQuery.refetch(),
              client.invalidateQueries({ queryKey: ['panel-branding'] }),
            ]);
          }}
          onClose={() => setTotpModalMode(null)}
        />
      )}
      <SSOModal
        open={ssoModal.open}
        initialEnabled={ssoModal.initialEnabled}
        onClose={() => setSsoModal({ open: false })}
      />
      {uaModalOpen && (
        <UserAgentModal
          open
          userAgentEnabled={data.userAgentEnabled}
          userAgentAllow={data.userAgentAllow ?? ''}
          saving={business}
          onSave={async (values) => {
            await applyNetwork({
              userAgentEnabled: values.userAgentEnabled,
              userAgentAllow: values.userAgentAllow,
            });
            setUaModalOpen(false);
          }}
          onClose={() => setUaModalOpen(false)}
        />
      )}
      {unauthModalOpen && (
        <UnauthResponseModal
          open
          unauthStatus={data.unauthStatus}
          unauthHtml={data.unauthHtml ?? ''}
          saving={business}
          onSave={async (values) => {
            await applyNetwork({
              unauthStatus: values.unauthStatus,
              unauthHtml: values.unauthHtml,
            });
            setUnauthModalOpen(false);
          }}
          onClose={() => setUnauthModalOpen(false)}
        />
      )}
    </>
  );
}
