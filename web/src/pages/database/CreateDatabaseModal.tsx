import { useState } from 'react';
import {
  Alert,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Typography,
} from 'antd';
import { ReloadOutlined, CheckCircleFilled } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { charsetList, databaseList, type Server } from '../../api/database';
import { request } from '../../api/client';
import { LAYOUT } from '../../styles/tokens';

function randomPassword() {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';
  return (
    'Aa9!' +
    Array.from(
      crypto.getRandomValues(new Uint8Array(12)),
      (n) => alphabet[n % alphabet.length],
    ).join('')
  );
}
export default function CreateDatabaseModal({
  servers,
  initialServer,
  onClose,
  onCreated,
  onRemote,
}: {
  servers: Server[];
  initialServer?: string;
  onClose: () => void;
  onCreated: (id: string) => Promise<void>;
  onRemote: () => void;
}) {
  const [form] = Form.useForm();
  const [initialPassword] = useState(randomPassword);
  const [server, setServer] = useState(
    initialServer || servers.find((row) => row.local)?.id || servers[0]?.id,
  );
  const [customUser, setCustomUser] = useState(false),
    [busy, setBusy] = useState(false);
  const access = Form.useWatch('access', form);
  const chars = useQuery({
    queryKey: ['db-charsets', server],
    queryFn: () => charsetList(server!),
    enabled: !!server,
  });
  const status = useQuery({
    queryKey: ['db-databases', server],
    queryFn: () => databaseList(server!),
    enabled: !!server,
  });
  const current = servers.find((row) => row.id === server);
  return (
    <Modal
      title="添加MySQL数据库"
      open
      maskClosable={false}
      width={LAYOUT.databaseModalWidth}
      onCancel={onClose}
      confirmLoading={busy}
      okButtonProps={{ disabled: !server }}
      onOk={async () => {
        const values = await form.validateFields();
        setBusy(true);
        try {
          await request(`/api/db/servers/${server}/databases`, {
            method: 'POST',
            data: {
              name: values.name,
              username: values.username,
              password: values.password,
              charset: values.charset,
              host:
                values.access === 'all'
                  ? '%'
                  : values.access === 'ip'
                    ? values.ip
                    : 'localhost',
            },
          });
          await onCreated(server!);
          onClose();
        } finally {
          setBusy(false);
        }
      }}
    >
      <Form
        form={form}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1 }}
        colon={false}
        initialValues={{
          charset: 'utf8mb4',
          access: 'local',
          password: initialPassword,
        }}
        onValuesChange={(changed) => {
          if ('username' in changed)
            setCustomUser(changed.username !== form.getFieldValue('name'));
          if ('name' in changed && !customUser)
            form.setFieldValue(
              'username',
              String(changed.name || '').slice(0, 32),
            );
        }}
      >
        <Form.Item label="数据库名" required>
          <Space.Compact style={{ width: '100%' }}>
            <Form.Item
              name="name"
              noStyle
              rules={[
                {
                  required: true,
                  pattern: /^[a-zA-Z0-9_]{1,64}$/,
                  message: '请输入1至64位字母、数字或下划线',
                },
              ]}
            >
              <Input placeholder="新的数据库名称" style={{ flex: 1 }} />
            </Form.Item>
            <Form.Item name="charset" noStyle rules={[{ required: true }]}>
              <Select
                aria-label="字符集"
                loading={chars.isFetching}
                style={{ width: 170 }}
                options={
                  chars.data?.list.map((row) => ({
                    value: row.charset,
                    label: row.charset,
                  })) || [{ value: 'utf8mb4', label: 'utf8mb4' }]
                }
              />
            </Form.Item>
          </Space.Compact>
        </Form.Item>
        <Form.Item
          name="username"
          label="用户名"
          rules={[
            {
              required: true,
              pattern: /^[a-zA-Z0-9_]{1,32}$/,
              message: '请输入1至32位字母、数字或下划线',
            },
          ]}
        >
          <Input placeholder="数据库用户" />
        </Form.Item>
        <Form.Item
          name="password"
          label="密码"
          rules={[{ required: true }, { max: 1024 }]}
        >
          <Input
            autoComplete="new-password"
            addonAfter={
              <Button
                type="text"
                size="small"
                icon={<ReloadOutlined />}
                aria-label="生成随机数据库密码"
                onClick={() => form.setFieldValue('password', randomPassword())}
              />
            }
          />
        </Form.Item>
        <Form.Item
          name="access"
          label="访问权限"
          extra="本地服务器与指定IP均默认允许宿主机及同机容器连接。"
        >
          <Select
            options={[
              { value: 'all', label: '所有人' },
              { value: 'local', label: '本地服务器' },
              { value: 'ip', label: '指定IP' },
            ]}
          />
        </Form.Item>
        {access === 'ip' && (
          <Form.Item
            name="ip"
            label="指定IP"
            rules={[
              { required: true, message: '请输入允许访问的IP地址或网段' },
            ]}
          >
            <Input placeholder="IPv4/IPv6 地址或 IPv4 网段（如 192.168.1.0/24）" />
          </Form.Item>
        )}
        <Form.Item label="添加至" required>
          <Space wrap>
            <Select
              aria-label="添加至数据库服务器"
              value={server}
              onChange={setServer}
              style={{ width: 280, maxWidth: '100%' }}
              options={servers.map((row) => ({
                value: row.id,
                disabled: row.running === false,
                label: `${row.name}（${row.host ? `${row.host}:${row.port}` : row.socketPath}）`,
              }))}
            />
            <Button type="link" onClick={onRemote}>
              管理远程数据库
            </Button>
          </Space>
        </Form.Item>
        <Form.Item label=" ">
          <Space wrap>
            {status.error ? (
              <Typography.Text type="danger">
                连接失败：{status.error.message}
              </Typography.Text>
            ) : status.isFetching ? (
              <Typography.Text type="secondary">正在连接…</Typography.Text>
            ) : (
              current && (
                <Typography.Text type="success">
                  <CheckCircleFilled /> {current.host || '本地Socket'}状态：正常
                </Typography.Text>
              )
            )}
            <Button
              type="link"
              icon={<ReloadOutlined />}
              onClick={() => {
                void status.refetch();
                void chars.refetch();
              }}
            >
              刷新
            </Button>
          </Space>
        </Form.Item>
        {chars.error && <Alert type="error" message={chars.error.message} />}
      </Form>
    </Modal>
  );
}
