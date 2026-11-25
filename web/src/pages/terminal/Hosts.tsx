import {
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Tooltip,
  Typography,
} from 'antd';
import { Plus } from 'lucide-react';
import type { TerminalHost, TerminalHostInput } from '../../api/terminal';
import ContentSizedTable from '../../components/ContentSizedTable';
import { LAYOUT } from '../../styles/tokens';

export function HostList({
  hosts,
  loading,
  pending,
  onConnect,
  onAdd,
  onEdit,
  onDelete,
}: {
  hosts: TerminalHost[];
  loading: boolean;
  pending: boolean;
  onConnect: (host: TerminalHost) => void;
  onAdd: () => void;
  onEdit: (host: TerminalHost) => void;
  onDelete: (id: string) => Promise<unknown>;
}) {
  return (
    <Card>
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Button type="primary" icon={<Plus size={16} />} onClick={onAdd}>
          新建连接
        </Button>
        <ContentSizedTable
          rowKey="id"
          fillColumn="address"
          loading={loading}
          dataSource={hosts}
          pagination={false}
          locale={{ emptyText: '暂无主机' }}
          columns={[
            { title: '名称', dataIndex: 'name' },
            {
              title: '主机地址',
              dataIndex: 'address',
              render: (_, host) =>
                (host.address.includes(':')
                  ? '[' + host.address + ']'
                  : host.address) +
                ':' +
                host.port,
            },
            { title: '用户名', dataIndex: 'username' },
            {
              title: '认证方式',
              dataIndex: 'authType',
              render: (value: TerminalHost['authType']) =>
                value === 'password' ? '密码' : '私钥',
            },
            {
              title: '主机指纹',
              render: (_, host) =>
                host.fingerprint ? (
                  <Tooltip title={host.fingerprint}>
                    <span>已记录</span>
                  </Tooltip>
                ) : (
                  <Typography.Text type="secondary">
                    首次连接自动记录
                  </Typography.Text>
                ),
            },
            {
              title: '操作',
              render: (_, host) => (
                <Space>
                  <Button type="link" onClick={() => onConnect(host)}>
                    连接
                  </Button>
                  <Button type="link" onClick={() => onEdit(host)}>
                    编辑
                  </Button>
                  <Popconfirm
                    title="删除 SSH 主机？"
                    description={host.name}
                    okText="删除"
                    cancelText="取消"
                    onConfirm={() => onDelete(host.id)}
                  >
                    <Button type="link" danger disabled={pending}>
                      删除
                    </Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Space>
    </Card>
  );
}

export function HostForm({
  host,
  saving,
  onSave,
  onClose,
  getContainer,
}: {
  host?: TerminalHost;
  saving: boolean;
  onSave: (input: TerminalHostInput) => void;
  onClose: () => void;
  getContainer?: () => HTMLElement;
}) {
  const [form] = Form.useForm<TerminalHostInput>();
  const authType =
    Form.useWatch('authType', form) ?? host?.authType ?? 'password';
  const requireCredential = !host || host.authType !== authType;
  const { name, address, port, username } = host ?? {};
  return (
    <Modal
      open
      title={host ? '编辑主机' : '新建连接'}
      getContainer={getContainer}
      confirmLoading={saving}
      closable={!saving}
      cancelButtonProps={{ disabled: saving }}
      maskClosable={false}
      onCancel={onClose}
      onOk={() => form.submit()}
      okText="保存"
      cancelText="取消"
    >
      <Form
        form={form}
        initialValues={{
          name,
          address,
          port: port ?? 22,
          username: username ?? 'root',
          authType: host?.authType ?? 'password',
          fingerprint: host?.fingerprint,
        }}
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1 }}
        colon={false}
        onFinish={onSave}
        disabled={saving}
      >
        <Form.Item
          label="名称"
          name="name"
          rules={[{ required: true, whitespace: true }]}
        >
          <Input placeholder="用于识别主机" maxLength={128} />
        </Form.Item>
        <Form.Item
          label="主机地址"
          name="address"
          rules={[{ required: true, whitespace: true }]}
        >
          <Input
            placeholder="IP 地址或域名"
            maxLength={253}
            autoCapitalize="off"
          />
        </Form.Item>
        <Form.Item
          label="SSH 端口"
          name="port"
          rules={[{ required: true, type: 'integer', min: 1, max: 65535 }]}
        >
          <InputNumber min={1} max={65535} precision={0} />
        </Form.Item>
        <Form.Item
          label="用户名"
          name="username"
          rules={[{ required: true, whitespace: true }]}
        >
          <Input maxLength={256} autoCapitalize="off" />
        </Form.Item>
        <Form.Item
          label="认证方式"
          name="authType"
          rules={[{ required: true }]}
        >
          <Select
            options={[
              { value: 'password', label: '密码' },
              { value: 'privateKey', label: '私钥' },
            ]}
          />
        </Form.Item>
        {authType === 'password' ? (
          <Form.Item
            label="SSH 密码"
            name="password"
            preserve={false}
            rules={[{ required: requireCredential }]}
          >
            <Input.Password
              autoComplete="new-password"
              placeholder={requireCredential ? undefined : '留空保留原密码'}
            />
          </Form.Item>
        ) : (
          <>
            <Form.Item
              label="SSH 私钥"
              name="privateKey"
              preserve={false}
              rules={[{ required: requireCredential, whitespace: true }]}
            >
              <Input.TextArea
                rows={3}
                placeholder={
                  requireCredential ? '粘贴私钥内容' : '留空保留原私钥'
                }
                spellCheck={false}
                autoComplete="off"
              />
            </Form.Item>
            <Form.Item label="私钥口令" name="passphrase" preserve={false}>
              <Input.Password autoComplete="new-password" />
            </Form.Item>
          </>
        )}
      </Form>
    </Modal>
  );
}
