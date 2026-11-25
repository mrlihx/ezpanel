import { useParams } from 'react-router-dom';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import { SSHProtection } from './Fail2banProtection';
import { useRef, useState, type ComponentRef } from 'react';
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Divider,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Space,
  Switch,
  Tabs,
  Tag,
  Typography,
  theme,
  Grid,
} from 'antd';
import { RotateCw } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  sshState,
  saveSSH,
  sshService,
  sshUsers,
  createSSHUser,
  deleteSSHUser,
  changeSSHPassword,
  sshKeys,
  generateSSHKey,
  type SSHState,
  type SSHUser,
} from '../../api/security';
import { sshLogs } from '../../api/oplog';
import { SSHLogs } from '../oplog/Sources';
import '../oplog/logs.css';
import { LAYOUT, MONO_FONT } from '../../styles/tokens';
import { saveBlob } from '../../utils/download';
function randomPassword() {
  const data = crypto.getRandomValues(new Uint8Array(20));
  const alphabet =
    'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#%';
  return Array.from(data, (value) => alphabet[value % alphabet.length]).join(
    '',
  );
}
function downloadKeys(name: string, content: string) {
  saveBlob(content, `${name}-ed25519`);
}
export default function SSH() {
  const { modal, message } = App.useApp();
  const { token } = theme.useToken();
  const query = useQuery({ queryKey: ['security-ssh'], queryFn: sshState });
  const logs = useQuery({
    queryKey: ['ssh-login-logs'],
    queryFn: sshLogs,
    staleTime: 60000,
  });
  const [working, setWorking] = useState(false);
  const [keys, setKeys] = useState<string>();
  const { view } = useParams();
  const route = useRoutedTab('ssh', '/security/ssh', view, [
    'settings',
    'users',
    'logs',
  ]);
  const tab = route.tab;
  const [logView, setLogView] = useState({ status: 'all', id: 0 });
  const [password, setPassword] = useState<{ name: string; value: string }>();
  const rows = logs.data?.list ?? [];
  const count = (success: boolean, days?: number) =>
    rows.filter(
      (row) =>
        row.success === success &&
        (days === undefined ||
          Date.parse(row.createdAt) >= logs.dataUpdatedAt - days * 86400000),
    ).length;
  const date = (value: Date) =>
    new Intl.DateTimeFormat('sv-SE', {
      timeZone: query.data?.timezone || 'Asia/Shanghai',
    }).format(value);
  const today = date(new Date(logs.dataUpdatedAt || 0)),
    yesterday = date(new Date(logs.dataUpdatedAt - 86400000));
  const periods = [
    {
      name: '今日',
      success: rows.filter(
        (row) => row.success && date(new Date(row.createdAt)) === today,
      ).length,
      failed: rows.filter(
        (row) => !row.success && date(new Date(row.createdAt)) === today,
      ).length,
    },
    {
      name: '昨日',
      success: rows.filter(
        (row) => row.success && date(new Date(row.createdAt)) === yesterday,
      ).length,
      failed: rows.filter(
        (row) => !row.success && date(new Date(row.createdAt)) === yesterday,
      ).length,
    },
    { name: '7天', success: count(true, 7), failed: count(false, 7) },
  ];
  if (route.invalid) return <RouteNotFound back="/security/ssh" />;
  return (
    <>
      {query.error && (
        <Alert
          type="error"
          message={query.error.message}
          action={<Button onClick={() => void query.refetch()}>重试</Button>}
        />
      )}
      <div className="security-ssh-summary">
        <Card
          title="SSH服务"
          extra={
            <Switch
              checked={query.data?.running ?? false}
              disabled={!query.data}
              loading={working}
              onChange={(enabled) =>
                modal.confirm({
                  title: enabled ? '开启SSH服务？' : '关闭SSH服务？',
                  content: enabled
                    ? undefined
                    : '将停止SSH监听，远程终端可能断开。请保留面板访问作为恢复入口。',
                  onOk: async () => {
                    setWorking(true);
                    try {
                      await sshService(enabled);
                      await query.refetch();
                      message.success('SSH服务状态已更新');
                    } finally {
                      setWorking(false);
                    }
                  },
                })
              }
            />
          }
        >
          <Tag color={query.data?.running ? 'success' : 'default'}>
            {query.data?.running ? 'SSH服务已启用' : 'SSH服务已停止'}
          </Tag>
          <Typography.Paragraph style={{ marginTop: 16, marginBottom: 0 }}>
            当前状态：
            {query.isPending
              ? '读取中'
              : query.data?.running
                ? '运行中'
                : '已停止'}
          </Typography.Paragraph>
        </Card>
        <Card loading={logs.isPending}>
          {logs.error && <Alert type="error" message={logs.error.message} />}
          <div className="security-ssh-counts">
            {[true, false].map((success) => (
              <button
                type="button"
                key={String(success)}
                className="security-ssh-total"
                aria-label={
                  success ? '查看SSH成功登录日志' : '查看SSH失败登录日志'
                }
                onClick={() => {
                  setLogView((view) => ({
                    status: String(success),
                    id: view.id + 1,
                  }));
                  void route.selectTab('logs');
                }}
                style={{
                  color: success ? token.colorSuccess : token.colorError,
                  background: success
                    ? token.colorSuccessBg
                    : token.colorErrorBg,
                  borderColor: success
                    ? token.colorSuccessBorder
                    : token.colorErrorBorder,
                }}
              >
                <Typography.Text
                  strong
                  style={{ display: 'block', fontSize: 24, color: 'inherit' }}
                >
                  {logs.data ? count(success) : '—'}
                </Typography.Text>
                <Typography.Text>
                  {success ? 'SSH成功登录' : 'SSH失败尝试'}
                </Typography.Text>
              </button>
            ))}
            <div className="security-ssh-periods">
              {periods.map((row) => (
                <div className="security-ssh-period" key={row.name}>
                  <span>{row.name}</span>
                  <div
                    className="security-ssh-bar"
                    style={{ background: token.colorFillSecondary }}
                  >
                    <span
                      style={{
                        width: `${(100 * row.success) / (row.success + row.failed || 1)}%`,
                        background: token.colorSuccess,
                      }}
                    />
                    <span
                      style={{
                        width: `${(100 * row.failed) / (row.success + row.failed || 1)}%`,
                        background: token.colorError,
                      }}
                    />
                  </div>
                  <Space size={6}>
                    <Typography.Text type="success">
                      {logs.data ? row.success : '—'}
                    </Typography.Text>
                    <Typography.Text type="danger">
                      {logs.data ? row.failed : '—'}
                    </Typography.Text>
                  </Space>
                </div>
              ))}
            </div>
          </div>
          <Typography.Text type="secondary">
            统计范围：{logs.data?.scope ?? '最近5000条sshd记录中的登录事件'}。
          </Typography.Text>
        </Card>
      </div>
      <Card>
        <Tabs
          activeKey={tab}
          onChange={route.selectTab}
          destroyOnHidden
          items={[
            {
              key: 'settings',
              label: '基础设置',
              children: query.data && (
                <BasicSettings
                  key={query.data.revision}
                  data={query.data}
                  refresh={() => void query.refetch()}
                  onKeys={() => setKeys('root')}
                  onPassword={(value) => setPassword({ name: 'root', value })}
                />
              ),
            },
            {
              key: 'users',
              label: 'SSH系统账号管理',
              children: (
                <Accounts
                  onKeys={setKeys}
                  onPassword={(name) => setPassword({ name, value: '' })}
                />
              ),
            },
            {
              key: 'logs',
              label: 'SSH登录日志',
              children: (
                <SSHLogs
                  key={logView.id}
                  initialStatus={logView.status}
                  tableClassName="security-data-table"
                />
              ),
            },
          ]}
        />
      </Card>
      {keys && (
        <KeysModal key={keys} name={keys} onClose={() => setKeys(undefined)} />
      )}
      {password && (
        <PasswordModal
          target={password}
          onClose={() => setPassword(undefined)}
        />
      )}
    </>
  );
}
function BasicSettings({
  data,
  refresh,
  onKeys,
  onPassword,
}: {
  data: SSHState;
  refresh: () => void;
  onKeys: () => void;
  onPassword: (value: string) => void;
}) {
  const { modal, message } = App.useApp();
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);
  const [password, setPassword] = useState('');
  return (
    <Space direction="vertical" size="large" style={{ width: '100%' }}>
      <Card title="SSH基础设置" size="small">
        <Form
          form={form}
          layout="vertical"
          initialValues={{
            password: data.values.passwordauthentication === 'yes',
            pubkey: data.values.pubkeyauthentication === 'yes',
            port: Number(data.values.port.split(',')[0]),
            rootLogin: data.values.permitrootlogin,
          }}
          onFinish={(values) =>
            modal.confirm({
              title: '保存并应用SSH设置？',
              content: data.socketActivated
                ? '当前使用socket激活，修改端口会重新启动SSH监听。请先确认外部防火墙/安全组已放行新端口。'
                : '保存前检查配置，应用时重载SSH服务。',
              onOk: async () => {
                setSaving(true);
                try {
                  const result = await saveSSH({
                    ...values,
                    revision: data.revision,
                  });
                  refresh();
                  if (result.keyGenerated) onKeys();
                  message.success('SSH设置已保存');
                } finally {
                  setSaving(false);
                }
              },
            })
          }
        >
          <Row gutter={24}>
            <Col xs={24} md={12}>
              <Form.Item
                name="password"
                label="SSH密码登录"
                valuePropName="checked"
              >
                <Switch />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item
                name="pubkey"
                label="SSH密钥登录"
                valuePropName="checked"
              >
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item
            name="port"
            label="SSH端口"
            rules={[{ required: true, message: '请输入SSH端口' }]}
          >
            <InputNumber
              min={1}
              max={65535}
              style={{ width: 260, maxWidth: '100%' }}
            />
          </Form.Item>
          <Form.Item
            name="rootLogin"
            label="Root登录设置"
            rules={[{ required: true }]}
          >
            <Select
              style={{ width: 400, maxWidth: '100%' }}
              options={[
                { value: 'yes', label: 'yes - 允许密码和密钥登录' },
                {
                  value: 'prohibit-password',
                  label: 'prohibit-password - 只允许密钥登录',
                },
                {
                  value: 'forced-commands-only',
                  label: 'forced-commands-only - 仅允许受限命令密钥',
                },
                { value: 'no', label: 'no - 禁止root登录' },
              ]}
            />
          </Form.Item>
          <Space>
            <Button type="primary" htmlType="submit" loading={saving}>
              保存设置
            </Button>
            <Button
              icon={<RotateCw size={15} strokeWidth={1.9} />}
              disabled={saving}
              onClick={refresh}
            >
              刷新
            </Button>
          </Space>
        </Form>
        <Typography.Paragraph
          type="secondary"
          style={{ marginTop: 16, marginBottom: 0 }}
        >
          开启密钥登录并保存时，自动生成Ed25519登录密钥。关闭密码登录前，请先下载密钥并验证登录。
        </Typography.Paragraph>
      </Card>
      <Card title="Root设置" size="small">
        <Typography.Paragraph strong>Root密码</Typography.Paragraph>
        <Space wrap style={{ width: '100%' }}>
          <Space.Compact style={{ width: 382, maxWidth: '100%' }}>
            <Input.Password
              style={{ flex: 1, minWidth: 0 }}
              autoComplete="new-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              placeholder="请输入新密码，至少12字节"
            />
            <Button
              icon={<RotateCw size={15} strokeWidth={1.9} />}
              aria-label="生成随机密码"
              onClick={() => setPassword(randomPassword())}
            />
          </Space.Compact>
          <Button
            type="primary"
            disabled={!password}
            onClick={() => onPassword(password)}
          >
            重置
          </Button>
        </Space>
        <Divider />
        <Typography.Paragraph strong>Root密钥</Typography.Paragraph>
        <Space wrap>
          <Button type="primary" onClick={onKeys}>
            查看密钥
          </Button>
          <Button
            onClick={async () => {
              const key = await sshKeys('root');
              if (key.privateKey) downloadKeys('root', key.privateKey);
              else onKeys();
            }}
          >
            下载
          </Button>
        </Space>
        <Typography.Paragraph
          type="secondary"
          style={{ marginTop: 12, marginBottom: 0 }}
        >
          Ed25519登录私钥，请妥善保存。重新生成后，旧的面板密钥将失效。
        </Typography.Paragraph>
      </Card>
      <SSHProtection port={Number(data.values.port.split(',')[0]) || 22} />
    </Space>
  );
}
function Accounts({
  onKeys,
  onPassword,
}: {
  onKeys: (name: string) => void;
  onPassword: (name: string) => void;
}) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { modal, message } = App.useApp();
  const query = useQuery({
    queryKey: ['security-ssh-users'],
    queryFn: sshUsers,
  });
  const [adding, setAdding] = useState(false);
  const [working, setWorking] = useState(false);
  const [form] = Form.useForm();
  return (
    <>
      <div className="security-toolbar">
        <Button
          type="primary"
          onClick={() => {
            form.resetFields();
            form.setFieldsValue({
              shell: '/bin/bash',
              password: randomPassword(),
            });
            setAdding(true);
          }}
        >
          添加账号
        </Button>
        <Button
          icon={<RotateCw size={15} strokeWidth={1.9} />}
          aria-label="刷新SSH账号"
          loading={query.isFetching}
          onClick={() => void query.refetch()}
        />
      </div>
      {query.error && <Alert type="error" message={query.error.message} />}
      <ContentSizedTable<SSHUser>
        className="security-data-table"
        fillColumn="home"
        rowKey="name"
        size="middle"
        dataSource={query.data?.list ?? []}
        loading={query.isFetching}
        columns={[
          { title: '账号', dataIndex: 'name', width: 160, ellipsis: true },
          { title: 'UID', dataIndex: 'uid', width: 72 },
          { title: '主目录', dataIndex: 'home', ellipsis: true },
          { title: 'Shell', dataIndex: 'shell', width: 160, ellipsis: true },
          {
            title: '操作',
            key: 'actions',
            fixed: fixedActions,
            render: (_, row) => (
              <Space size={12}>
                <Button
                  type="link"
                  size="small"
                  onClick={() => onPassword(row.name)}
                >
                  改密
                </Button>
                <Button
                  type="link"
                  size="small"
                  onClick={() => onKeys(row.name)}
                >
                  密钥
                </Button>
                {!row.system && (
                  <Button
                    type="link"
                    size="small"
                    danger
                    onClick={() => {
                      let confirmation = '';
                      let removeHome = false;
                      modal.confirm({
                        title: `删除SSH账号 ${row.name}？`,
                        content: (
                          <Space direction="vertical">
                            <Typography.Text>
                              输入完整账号名确认。删除后该账号无法登录。
                            </Typography.Text>
                            <Input
                              onChange={(event) => {
                                confirmation = event.target.value;
                              }}
                              placeholder={row.name}
                            />
                            <Switch
                              checkedChildren="删除主目录"
                              unCheckedChildren="保留主目录"
                              onChange={(value) => {
                                removeHome = value;
                              }}
                            />
                          </Space>
                        ),
                        onOk: async () => {
                          if (confirmation !== row.name) {
                            void message.info('请输入完整账号名');
                            throw new Error('账号名不匹配');
                          }
                          await deleteSSHUser(
                            row.name,
                            confirmation,
                            removeHome,
                          );
                          await query.refetch();
                          message.success('账号已删除');
                        },
                      });
                    }}
                  >
                    删除
                  </Button>
                )}
              </Space>
            ),
          },
        ]}
      />
      <Modal
        open={adding}
        title="添加SSH系统账号"
        maskClosable={false}
        confirmLoading={working}
        onCancel={() => setAdding(false)}
        onOk={async () => {
          const values = await form.validateFields();
          setWorking(true);
          try {
            await createSSHUser(values);
            setAdding(false);
            await query.refetch();
            message.success('账号已创建');
          } finally {
            setWorking(false);
          }
        }}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="name"
            label="账号"
            rules={[
              { required: true },
              {
                pattern: /^[a-z_][a-z0-9_-]{0,31}$/,
                message: '使用小写字母、数字、下划线或短横线',
              },
            ]}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={[{ required: true, min: 12, message: '密码至少12字节' }]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item name="shell" label="Shell">
            <Select
              options={[
                { value: '/bin/bash', label: '/bin/bash' },
                { value: '/bin/sh', label: '/bin/sh' },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
function PasswordModal({
  target,
  onClose,
}: {
  target: { name: string; value: string };
  onClose: () => void;
}) {
  const { message } = App.useApp();
  const [value, setValue] = useState(target.value);
  const [saving, setSaving] = useState(false);
  return (
    <Modal
      open
      title={`重置 ${target.name} 密码`}
      maskClosable={false}
      onCancel={onClose}
      confirmLoading={saving}
      okButtonProps={{ disabled: value.length < 12 }}
      onOk={async () => {
        setSaving(true);
        try {
          await changeSSHPassword(target.name, value);
          message.success('系统账号密码已修改，请保存新密码');
          onClose();
        } finally {
          setSaving(false);
        }
      }}
    >
      <Typography.Paragraph>
        修改的是Linux系统账号密码。至少12字节，不允许冒号和换行。
      </Typography.Paragraph>
      <Space.Compact style={{ width: '100%' }}>
        <Input.Password
          autoComplete="new-password"
          value={value}
          onChange={(event) => setValue(event.target.value)}
        />
        <Button
          icon={<RotateCw size={15} strokeWidth={1.9} />}
          aria-label="生成随机密码"
          onClick={() => setValue(randomPassword())}
        />
      </Space.Compact>
    </Modal>
  );
}
function KeysModal({ name, onClose }: { name: string; onClose: () => void }) {
  const { message, modal } = App.useApp();
  const query = useQuery({
    queryKey: ['security-ssh-keys', name],
    queryFn: () => sshKeys(name),
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  const [saving, setSaving] = useState(false);
  const [generatedKey, setGeneratedKey] = useState<string>();
  const input = useRef<ComponentRef<typeof Input.TextArea>>(null);
  const privateKey = generatedKey ?? query.data?.privateKey ?? '';
  const generate = async () => {
    setSaving(true);
    try {
      const result = await generateSSHKey(name, !!privateKey);
      setGeneratedKey(result.privateKey);
      message.success('Ed25519密钥已生成，请下载保存');
    } finally {
      setSaving(false);
    }
  };
  return (
    <Modal
      open
      title={`${name === 'root' ? 'Root' : name} SSH密钥登录`}
      width={LAYOUT.shellWidth}
      maskClosable={false}
      onCancel={onClose}
      footer={null}
    >
      {query.error && <Alert type="error" message={query.error.message} />}
      <Input.TextArea
        ref={input}
        style={{ fontFamily: MONO_FONT }}
        rows={12}
        readOnly
        aria-label={`${name} Ed25519登录私钥`}
        value={privateKey}
        placeholder={
          query.isPending
            ? '正在读取密钥…'
            : '尚未生成面板管理的密钥，请点击生成密钥。'
        }
      />
      <Space wrap style={{ marginTop: 16 }}>
        <Button
          type="primary"
          disabled={!privateKey || saving}
          onClick={() => {
            const area = input.current?.resizableTextArea?.textArea;
            if (!area) return;
            area.select();
            if (document.execCommand('copy')) message.success('密钥已复制');
            else message.info('请手动复制已选中的密钥');
          }}
        >
          复制
        </Button>
        <Button
          type="primary"
          disabled={!privateKey || saving}
          onClick={() => downloadKeys(name, privateKey)}
        >
          下载
        </Button>
        <Button
          loading={saving}
          disabled={!query.data}
          onClick={() => {
            if (!privateKey) void generate();
            else
              modal.confirm({
                title: '重新生成Ed25519密钥？',
                content:
                  '旧的面板登录密钥将立即失效，其他已导入的公钥保持不变。',
                onOk: generate,
              });
          }}
        >
          {privateKey ? '重新生成' : '生成密钥'}
        </Button>
      </Space>
    </Modal>
  );
}
