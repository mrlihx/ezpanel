import { lazy, Suspense, useEffect, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Skeleton,
  Space,
  Switch,
  Table,
  Tabs,
  Typography,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import {
  frpConfig,
  saveFRPConfig,
  type FRPConfig,
  type FRPProxy,
  type FRPRole,
} from '../../api/frp';
const CodeEditor = lazy(() => import('../../components/CodeEditor'));
const proxyTypes = ['tcp', 'udp', 'http', 'https'];

type Props = {
  role: FRPRole;
  busy: boolean;
  onDirty: (value: boolean) => void;
};
export default function FRPConfiguration({ role, busy, onDirty }: Props) {
  const { modal, message } = App.useApp();
  const [tab, setTab] = useState('visual');
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [raw, setRaw] = useState<string>();
  const [form] = Form.useForm();
  const query = useQuery({
    queryKey: ['frp-config', role],
    queryFn: () => frpConfig(role),
  });
  const markDirty = (value: boolean) => {
    setDirty(value);
    onDirty(value);
  };
  useEffect(() => () => onDirty(false), [onDirty]);
  const changeTab = (value: string) => {
    const change = () => {
      markDirty(false);
      setRaw(undefined);
      setTab(value);
    };
    if (dirty) modal.confirm({ title: '放弃尚未保存的配置？', onOk: change });
    else change();
  };
  async function save(mode: 'visual' | 'raw') {
    if (!query.data) return;
    let data: { content?: string; values?: Record<string, unknown> };
    if (mode === 'raw') data = { content: raw ?? query.data.content };
    else {
      const fields = await form.validateFields();
      const { allowPorts, ...values } = fields;
      if (role === 'frps')
        values.allowPorts = String(allowPorts ?? '')
          .split(/[\s,，]+/)
          .filter(Boolean)
          .map((text) => {
            const ports = text.split('-').map(Number);
            return ports.length === 1
              ? { single: ports[0] }
              : { start: ports[0], end: ports[1] };
          });
      data = { values };
    }
    setSaving(true);
    try {
      const result = await saveFRPConfig(role, {
        revision: query.data.revision,
        ...data,
      });
      message.success(
        result.mode === 'reload'
          ? '已保存并重载代理配置'
          : result.mode === 'restart'
            ? '已保存并重启服务'
            : '已保存，启动时生效',
      );
      markDirty(false);
      setRaw(undefined);
      await query.refetch();
    } finally {
      setSaving(false);
    }
  }
  if (query.isPending) return <Skeleton active />;
  if (!query.data)
    return (
      <Alert type="error" message={query.error?.message ?? '配置读取失败'} />
    );
  return (
    <div className="frp-configuration">
      <Tabs
        activeKey={tab}
        onChange={changeTab}
        items={[
          { key: 'visual', label: '服务配置' },
          { key: 'raw', label: '配置文件' },
        ]}
      />
      <Typography.Text code copyable>
        {query.data.path}
      </Typography.Text>
      {tab === 'raw' ? (
        <>
          <Typography.Text type="secondary">
            保存前校验配置。frps
            及客户端公共参数变化会重启服务，客户端代理规则变化自动重载。
          </Typography.Text>
          <div className="frp-editor">
            <Suspense fallback={<Skeleton active />}>
              <CodeEditor
                path={query.data.path}
                value={raw ?? query.data.content}
                onChange={(value) => {
                  setRaw(value);
                  markDirty(value !== query.data?.content);
                }}
              />
            </Suspense>
          </div>
          <div className="frp-config-actions">
            <Button
              type="primary"
              loading={saving}
              disabled={busy || !dirty}
              onClick={() => void save('raw')}
            >
              保存配置文件
            </Button>
          </div>
        </>
      ) : (
        <>
          <FRPVisual
            key={query.data.revision}
            role={role}
            config={query.data}
            form={form}
            disabled={busy || saving}
            onChange={() => markDirty(true)}
          />
          <div className="frp-config-actions">
            <Button
              type="primary"
              loading={saving}
              disabled={busy || !dirty}
              onClick={() => void save('visual')}
            >
              保存服务配置
            </Button>
          </div>
        </>
      )}
    </div>
  );
}

type VisualProps = {
  role: FRPRole;
  config: FRPConfig;
  form: ReturnType<typeof Form.useForm>[0];
  disabled: boolean;
  onChange: () => void;
};
function FRPVisual({ role, config, form, disabled, onChange }: VisualProps) {
  useEffect(() => {
    form.resetFields();
  }, [config.revision, form]);
  const values = config.values;
  const tokenAuth =
    (!values.auth?.method || values.auth.method === 'token') &&
    !values.auth?.tokenSource;
  const initial: Record<string, unknown> = {
    bindAddr: values.bindAddr ?? '0.0.0.0',
    bindPort: values.bindPort ?? 7000,
    serverAddr: values.serverAddr ?? '127.0.0.1',
    serverPort: values.serverPort ?? 7000,
    user: values.user ?? '',
    'auth.token': values.auth?.token ?? '',
    'transport.protocol': values.transport?.protocol ?? 'tcp',
    'transport.tls.enable': values.transport?.tls?.enable ?? true,
    'log.level': values.log?.level ?? 'info',
    'log.maxDays': values.log?.maxDays ?? 7,
    allowPorts:
      values.allowPorts
        ?.map((row) => row.single || `${row.start}-${row.end}`)
        .join(', ') ?? '',
    'webServer.addr': values.webServer?.addr ?? '127.0.0.1',
    'webServer.port': values.webServer?.port ?? 0,
    'webServer.user': values.webServer?.user ?? '',
    'webServer.password': values.webServer?.password ?? '',
    kcpBindPort: values.kcpBindPort ?? 0,
    quicBindPort: values.quicBindPort ?? 0,
    vhostHTTPPort: values.vhostHTTPPort ?? 0,
    vhostHTTPSPort: values.vhostHTTPSPort ?? 0,
    proxies: (values.proxies ?? [])
      .filter((row) => proxyTypes.includes(row.type))
      .map((row) => ({ ...row, originalName: row.name })),
  };
  const port = (name: string, label: string, optional = false) => (
    <Form.Item
      name={name}
      label={label}
      rules={[{ required: true }]}
      extra={optional ? '0 表示关闭' : undefined}
    >
      <InputNumber min={optional ? 0 : 1} max={65535} precision={0} />
    </Form.Item>
  );
  return (
    <ApplicationSettingsForm
      form={form}
      initialValues={initial}
      onValuesChange={onChange}
      disabled={disabled}
      className="frp-visual-form"
    >
      {role === 'frps' ? (
        <>
          <Form.Item
            name="bindAddr"
            label="监听地址"
            rules={[{ required: true }]}
          >
            <Input />
          </Form.Item>
          {port('bindPort', '监听端口')}
          {port('vhostHTTPPort', 'HTTP 代理端口', true)}
          {port('vhostHTTPSPort', 'HTTPS 代理端口', true)}
          {port('kcpBindPort', 'KCP 端口', true)}
          {port('quicBindPort', 'QUIC 端口', true)}
          <Form.Item
            name="allowPorts"
            label="允许转发端口"
            extra="留空表示不限，支持 6000, 7000-8000"
            rules={[
              {
                validator: (_, value: string) => {
                  const valid =
                    !value ||
                    value
                      .split(/[\s,，]+/)
                      .filter(Boolean)
                      .every((text) => {
                        if (!/^\d+(?:-\d+)?$/.test(text)) return false;
                        const [a, b = a] = text.split('-').map(Number);
                        return a >= 1 && b >= a && b <= 65535;
                      });
                  return valid
                    ? Promise.resolve()
                    : Promise.reject(new Error('请输入有效端口或端口范围'));
                },
              },
            ]}
          >
            <Input />
          </Form.Item>
        </>
      ) : (
        <>
          <Form.Item
            name="serverAddr"
            label="服务端地址"
            rules={[{ required: true }]}
          >
            <Input placeholder="IP 或域名" />
          </Form.Item>
          {port('serverPort', '服务端端口')}
          <Form.Item name="user" label="用户标识">
            <Input />
          </Form.Item>
          <Form.Item name="transport.protocol" label="传输协议">
            <Select
              options={['tcp', 'kcp', 'quic', 'websocket', 'wss'].map(
                (value) => ({ value, label: value.toUpperCase() }),
              )}
            />
          </Form.Item>
          <Form.Item
            name="transport.tls.enable"
            label="TLS 加密"
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
        </>
      )}
      {tokenAuth ? (
        <Form.Item
          name="auth.token"
          label="认证令牌"
          rules={[{ required: true, whitespace: true }]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
      ) : (
        <Alert
          type="info"
          message="当前使用高级鉴权，请在配置文件中修改鉴权参数"
        />
      )}
      {role === 'frps' && (
        <>
          <Form.Item name="webServer.addr" label="管理界面地址">
            <Input />
          </Form.Item>
          {port('webServer.port', '管理界面端口', true)}
          <Form.Item name="webServer.user" label="管理账号">
            <Input />
          </Form.Item>
          <Form.Item name="webServer.password" label="管理密码">
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </>
      )}
      <Form.Item name="log.level" label="日志级别">
        <Select
          options={['trace', 'debug', 'info', 'warn', 'error'].map((value) => ({
            value,
            label: value,
          }))}
        />
      </Form.Item>
      <Form.Item
        name="log.maxDays"
        label="日志保留天数"
        rules={[{ required: true }]}
      >
        <InputNumber min={1} max={365} precision={0} />
      </Form.Item>
      {role === 'frpc' && (
        <Form.Item name="proxies" label="代理规则">
          <ProxyRules disabled={disabled} />
        </Form.Item>
      )}
      <Typography.Text type="secondary">
        可视化保存保留高级配置字段，并重新整理 TOML
        格式。客户端代理规则修改自动重载，其他变更重启服务后生效。
      </Typography.Text>
    </ApplicationSettingsForm>
  );
}

function ProxyRules({
  value = [],
  onChange,
  disabled,
}: {
  value?: FRPProxy[];
  onChange?: (rows: FRPProxy[]) => void;
  disabled: boolean;
}) {
  const { modal } = App.useApp();
  const [editing, setEditing] = useState<{ index: number; value: FRPProxy }>();
  const [form] = Form.useForm();
  const kind = Form.useWatch('type', form) ?? 'tcp';
  const show = (index: number, row: FRPProxy) => {
    form.setFieldsValue({
      ...row,
      domains: row.customDomains?.join(', ') ?? '',
    });
    setEditing({ index, value: row });
  };
  return (
    <div>
      <Button
        disabled={disabled}
        onClick={() =>
          show(-1, {
            name: '',
            type: 'tcp',
            localIP: '127.0.0.1',
            localPort: 80,
            remotePort: 6000,
          })
        }
      >
        添加代理
      </Button>
      <Table
        size="small"
        rowKey="name"
        pagination={false}
        dataSource={value}
        scroll={{ x: 520 }}
        columns={[
          { title: '名称', dataIndex: 'name', ellipsis: true },
          { title: '类型', dataIndex: 'type', width: 70 },
          {
            title: '本地服务',
            render: (_, row) => `${row.localIP}:${row.localPort}`,
          },
          {
            title: '远程端口 / 域名',
            render: (_, row) => row.remotePort ?? row.customDomains?.join(', '),
          },
          {
            title: '操作',
            width: 100,
            render: (_, row, index) => (
              <Space>
                <Button
                  type="link"
                  size="small"
                  disabled={disabled}
                  onClick={() => show(index, row)}
                >
                  修改
                </Button>
                <Button
                  type="link"
                  size="small"
                  danger
                  disabled={disabled}
                  onClick={() =>
                    modal.confirm({
                      title: `删除代理 ${row.name}？`,
                      onOk: () =>
                        onChange?.(value.filter((_, i) => i !== index)),
                    })
                  }
                >
                  删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        open={!!editing}
        title={editing?.index === -1 ? '添加代理' : '修改代理'}
        onCancel={() => setEditing(undefined)}
        onOk={async () => {
          const fields = await form.validateFields();
          const { domains, ...proxy } = fields;
          const row: FRPProxy = {
            ...proxy,
            originalName: editing?.value.originalName,
            customDomains: String(domains ?? '')
              .split(/[\s,，]+/)
              .filter(Boolean),
          };
          onChange?.(
            editing?.index === -1
              ? [...value, row]
              : value.map((item, i) => (i === editing?.index ? row : item)),
          );
          setEditing(undefined);
        }}
        destroyOnHidden
      >
        <ApplicationSettingsForm form={form} preserve={false}>
          <Form.Item
            name="name"
            label="名称"
            rules={[
              { required: true },
              {
                validator: (_, name: string) =>
                  value.some(
                    (row, i) => row.name === name && i !== editing?.index,
                  )
                    ? Promise.reject(new Error('名称已存在'))
                    : Promise.resolve(),
              },
            ]}
          >
            <Input />
          </Form.Item>
          <Form.Item name="type" label="类型">
            <Select
              options={proxyTypes.map((item) => ({
                value: item,
                label: item.toUpperCase(),
              }))}
            />
          </Form.Item>
          <Form.Item
            name="localIP"
            label="本地地址"
            rules={[{ required: true }]}
          >
            <Input />
          </Form.Item>
          <Form.Item
            name="localPort"
            label="本地端口"
            rules={[{ required: true }]}
          >
            <InputNumber min={1} max={65535} precision={0} />
          </Form.Item>
          {['tcp', 'udp'].includes(kind) ? (
            <Form.Item
              name="remotePort"
              label="远程端口"
              rules={[{ required: true }]}
            >
              <InputNumber min={1} max={65535} precision={0} />
            </Form.Item>
          ) : (
            <Form.Item
              name="domains"
              label="域名"
              rules={[{ required: true, whitespace: true }]}
            >
              <Input placeholder="多个域名用逗号分隔" />
            </Form.Item>
          )}
        </ApplicationSettingsForm>
      </Modal>
    </div>
  );
}
