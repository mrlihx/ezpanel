import { useState } from 'react';
import { App, Button, Form, Input, InputNumber, Modal, Space } from 'antd';
import {
  saveServer,
  testServer,
  type Server,
  type ServerInput,
} from '../../api/database';
import { LAYOUT } from '../../styles/tokens';
type Fields = {
  host: string;
  port: number;
  username: string;
  password?: string;
  name?: string;
};
export default function ServerForm({
  server,
  onClose,
  onSaved,
}: {
  server?: Server;
  onClose: () => void;
  onSaved: (id: string) => void;
}) {
  const [form] = Form.useForm<Fields>();
  const [busy, setBusy] = useState(false);
  const { message } = App.useApp();
  const submit = async (test: boolean) => {
    const values = await form.validateFields();
    setBusy(true);
    try {
      const data: ServerInput = {
        ...values,
        name: values.name?.trim() || `${values.host}:${values.port}`,
        type: server?.type ?? 'mysql',
        network: 'tcp',
        socketPath: '',
        tls: server?.tls ?? { mode: 'disabled', serverName: '' },
        serverId: server?.id,
        password:
          server && !values.password ? undefined : (values.password ?? ''),
      };
      if (test) {
        const result = await testServer(data);
        void message.success(`连接成功：${result.version}`);
      } else {
        const result = await saveServer(server?.id, data);
        onSaved(result.id);
      }
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open
      title={server ? '编辑远程数据库' : '添加远程数据库'}
      width={LAYOUT.databaseModalWidth}
      onCancel={onClose}
      footer={
        <Space>
          <Button loading={busy} onClick={() => void submit(true)}>
            测试连接
          </Button>
          <Button onClick={onClose}>取消</Button>
          <Button
            type="primary"
            loading={busy}
            onClick={() => void submit(false)}
          >
            保存
          </Button>
        </Space>
      }
    >
      <Form
        form={form}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1 }}
        colon={false}
        initialValues={server ?? { host: '', port: 3306, username: 'root' }}
      >
        <Form.Item name="host" label="服务器地址" rules={[{ required: true }]}>
          <Input />
        </Form.Item>
        <Form.Item name="port" label="数据库端口" rules={[{ required: true }]}>
          <InputNumber min={1} max={65535} />
        </Form.Item>
        <Form.Item
          name="username"
          label="管理员名称"
          rules={[{ required: true }]}
        >
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item name="password" label="管理员密码">
          <Input.Password
            autoComplete="new-password"
            placeholder={server ? '留空保持不变' : '允许空密码'}
          />
        </Form.Item>
        <Form.Item name="name" label="备注" rules={[{ max: 64 }]}>
          <Input />
        </Form.Item>
      </Form>
    </Modal>
  );
}
