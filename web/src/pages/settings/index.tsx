import { useParams, useSearchParams } from 'react-router-dom';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  InputNumber,
  Select,
  Skeleton,
  Space,
  Switch,
  Typography,
} from 'antd';
import { me } from '../../api/auth';
import {
  panelSettings,
  saveBasic,
  saveNetwork,
  type PanelSettings,
  type NetworkSettings,
} from '../../api/settings';
import { useAuth } from '../../stores/auth';
import { useTheme } from '../../stores/theme';
import { LAYOUT } from '../../styles/tokens';
import BackupChannels from './BackupChannels';
import SectionTabs from '../../components/SectionTabs';
import AccountModal from './AccountModal';
import AlertNotifications from './AlertNotifications';
import PanelSSLModal from './PanelSSLModal';
export default function SettingsPage() {
  const [params] = useSearchParams();
  const { section, alertTab } = useParams();
  const legacyAlert = params.get('alertTab');
  const { tab, loading, invalid, selectTab } = useRoutedTab(
    'settings',
    '/settings',
    section,
    ['basic', 'network', 'backup', 'alerts'],
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
  const client = useQueryClient();
  const { message, modal } = App.useApp();
  const [business, setBusiness] = useState(false);
  const [accountKind, setAccountKind] = useState<
    'username' | 'password' | null
  >(null);
  const [sslOpen, setSSLOpen] = useState(false);
  const [networkForm] = Form.useForm();
  const enabled = Form.useWatch('sslEnabled', networkForm);
  const setUser = useAuth((s) => s.setUser);
  const { mode, setMode } = useTheme();
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
                  extra="超过指定时间没有操作面板，自动退出登录；监控刷新不计入操作。支持1分钟至12小时。"
                >
                  <InputNumber
                    min={1 / 60}
                    max={12}
                    precision={4}
                    step={0.5}
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
                form={networkForm}
                initialValues={data}
                onFinish={(v) =>
                  modal.confirm({
                    title: '应用面板安全设置？',
                    content:
                      '面板访问入口将按新设置切换，请先确认域名解析和证书。服务任务会继续运行。',
                    onOk: () =>
                      applyNetwork({
                        boundDomain: v.boundDomain ?? '',
                        sslEnabled: v.sslEnabled ?? false,
                        certificate: data.certificate,
                        certificateId: data.certificateId,
                        port: v.port,
                        entry: v.entry,
                      }),
                  })
                }
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
                <Form.Item
                  label="面板端口"
                  name="port"
                  rules={[
                    { required: true, type: 'integer', min: 1, max: 65535 },
                  ]}
                >
                  <InputNumber min={1} max={65535} precision={0} />
                </Form.Item>
                <Form.Item
                  label="面板入口"
                  name="entry"
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
                <Form.Item
                  label="绑定域名"
                  name="boundDomain"
                  extra="不含协议、端口或路径。绑定后使用该域名访问；留空解除绑定。"
                >
                  <Input placeholder="panel.example.com" />
                </Form.Item>
                <Form.Item
                  label="面板SSL"
                  name="sslEnabled"
                  valuePropName="checked"
                >
                  <Switch />
                </Form.Item>
                {enabled && (
                  <Form.Item label=" ">
                    <Button onClick={() => setSSLOpen(true)}>
                      面板SSL配置
                    </Button>
                  </Form.Item>
                )}
                <Form.Item label=" ">
                  <Button type="primary" htmlType="submit" loading={business}>
                    保存并应用
                  </Button>
                </Form.Item>
              </Form>
            ),
          },
          { key: 'backup', label: '备份设置', children: <BackupChannels /> },
          {
            key: 'alerts',
            label: '告警通知',
            children: <AlertNotifications active={tab === 'alerts'} />,
          },
        ]}
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
    </>
  );
}
