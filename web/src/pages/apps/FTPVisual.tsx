import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Space,
  Switch,
  Tag,
  Typography,
} from 'antd';
import { RotateCw } from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { request } from '../../api/client';
import ContentSizedTable from '../../components/ContentSizedTable';
import HostToolDirectory from './HostToolDirectory';

type Settings = {
  address: string;
  port: number;
  passiveStart: number;
  passiveEnd: number;
  logging: boolean;
};
type User = {
  name: string;
  root: string;
  password?: string;
  remark: string;
  enabled: boolean;
};

export function FTPServiceSettings() {
  const { message } = App.useApp();
  const client = useQueryClient();
  const [portOpen, setPortOpen] = useState(false);
  const [form] = Form.useForm<Settings>();
  const endpoint = '/api/software/ftp/visual';
  const queryKey = ['ftp-visual'];
  const query = useQuery({
    queryKey,
    queryFn: () => request<Settings>(endpoint),
  });
  const save = useMutation({
    mutationFn: (data: Settings) =>
      request<Settings>(endpoint, { method: 'PUT', data }),
    onSuccess: (next) => {
      client.setQueryData(queryKey, next);
      void client.invalidateQueries({
        queryKey: ['application-log-sources', 'ftp'],
      });
      setPortOpen(false);
      message.success('Pure-FTPd 设置已保存');
    },
  });
  const data = query.data;
  if (query.error) return <Alert type="error" message={query.error.message} />;
  if (!data) return null;
  const host =
    data.address && !['0.0.0.0', '::'].includes(data.address)
      ? data.address
      : window.location.hostname;
  const address =
    host.includes(':') && !host.startsWith('[') ? `[${host}]` : host;
  return (
    <>
      <ApplicationSettingsForm>
        <Form.Item label="当前 FTP 地址">
          <Space wrap>
            <Typography.Text
              copyable
            >{`ftp://${address}:${data.port}`}</Typography.Text>
            <Button onClick={() => setPortOpen(true)}>端口设置</Button>
          </Space>
        </Form.Item>
        <Form.Item
          label="日志管理"
          extra="在「操作日志 → 应用日志 → Pure-FTPd」查看传输日志。"
        >
          <Switch
            aria-label="FTP 传输日志"
            checked={data.logging}
            loading={save.isPending}
            onChange={(logging) => save.mutate({ ...data, logging })}
          />
        </Form.Item>
      </ApplicationSettingsForm>
      <Modal
        title="Pure-FTPd 端口设置"
        open={portOpen}
        destroyOnHidden
        onCancel={() => setPortOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={save.isPending}
      >
        <ApplicationSettingsForm
          form={form}

          preserve={false}
          initialValues={data}
          onFinish={(values) => save.mutate({ ...data, ...values })}
        >
          <Form.Item name="address" label="监听地址">
            <Input placeholder="留空监听所有地址" />
          </Form.Item>
          <Form.Item name="port" label="服务端口" rules={[{ required: true }]}>
            <InputNumber min={1} max={65535} precision={0} />
          </Form.Item>
          <Form.Item
            name="passiveStart"
            label="被动端口起始"
            rules={[{ required: true }]}
          >
            <InputNumber min={0} max={65535} precision={0} />
          </Form.Item>
          <Form.Item
            name="passiveEnd"
            label="被动端口结束"
            extra="被动端口均设为 0 时由服务自动分配。"
            rules={[{ required: true }]}
          >
            <InputNumber min={0} max={65535} precision={0} />
          </Form.Item>
        </ApplicationSettingsForm>
      </Modal>
    </>
  );
}

export function FTPUsers() {
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const [editing, setEditing] = useState<Partial<User>>();
  const [form] = Form.useForm<User>();
  const endpoint = '/api/software/ftp/users';
  const queryKey = ['ftp-users'];
  const query = useQuery({
    queryKey,
    queryFn: () => request<User[]>(endpoint),
  });
  const save = useMutation({
    mutationFn: (data: Partial<User> & { action: string }) =>
      request<void>(endpoint, { method: 'POST', data }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey });
      setEditing(undefined);
      message.success('FTP 用户已更新');
    },
  });
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Button type="primary" onClick={() => setEditing({ enabled: true })}>
        添加 FTP 用户
      </Button>
      {query.error && <Alert type="error" message={query.error.message} />}
      <ContentSizedTable<User>
        fillColumn="root"
        rowKey="name"
        loading={query.isPending}
        dataSource={query.data}
        scroll={{ x: 'max-content' }}
        columns={[
          { title: '用户名', dataIndex: 'name' },
          {
            title: '状态',
            render: (_, row) => (
              <Tag color={row.enabled ? 'success' : 'default'}>
                {row.enabled ? '启用' : '停用'}
              </Tag>
            ),
          },
          { title: '根目录', dataIndex: 'root' },
          { title: '备注', dataIndex: 'remark' },
          {
            title: '操作',
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  size="small"
                  onClick={() => setEditing(row)}
                >
                  编辑
                </Button>
                <Button
                  type="link"
                  size="small"
                  disabled={save.isPending}
                  onClick={() =>
                    save.mutate({
                      action: 'toggle',
                      name: row.name,
                      enabled: !row.enabled,
                    })
                  }
                >
                  {row.enabled ? '停用' : '启用'}
                </Button>
                <Button
                  type="link"
                  size="small"
                  danger
                  onClick={() =>
                    modal.confirm({
                      title: `删除 FTP 用户 ${row.name}？`,
                      content: '该用户的文件会保留。',
                      onOk: () =>
                        save.mutateAsync({ action: 'delete', name: row.name }),
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
        title={editing?.name ? '编辑 FTP 用户' : '添加 FTP 用户'}
        open={!!editing}
        destroyOnHidden
        onCancel={() => setEditing(undefined)}
        onOk={() => form.submit()}
        confirmLoading={save.isPending}
      >
        <ApplicationSettingsForm
          form={form}

          preserve={false}
          initialValues={editing}
          autoComplete="off"
          onFinish={(values) =>
            save.mutate({
              ...values,
              name: editing?.name ?? values.name,
              action: editing?.name ? 'edit' : 'create',
            })
          }
        >
          <Form.Item name="name" label="用户名" rules={[{ required: true }]}>
            <Input disabled={!!editing?.name} />
          </Form.Item>
          <Form.Item label="密码" required={!editing?.name}>
            <Space.Compact block>
              <Form.Item
                name="password"
                noStyle
                rules={[{ required: !editing?.name }]}
              >
                <Input.Password
                  autoComplete="new-password"
                  placeholder={
                    editing?.name ? '留空保留原密码' : '输入或生成密码'
                  }
                />
              </Form.Item>
              <Button
                icon={<RotateCw size={15} strokeWidth={1.9} />}
                aria-label="生成密码"
                onClick={() => {
                  const alphabet =
                    'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_!@#%+';
                  const bytes = crypto.getRandomValues(new Uint8Array(20));
                  form.setFieldValue(
                    'password',
                    Array.from(
                      bytes,
                      (byte) => alphabet[byte % alphabet.length],
                    ).join(''),
                  );
                }}
              />
            </Space.Compact>
          </Form.Item>
          <Form.Item
            name="root"
            label="根目录"
            rules={[{ required: true }]}
            extra="读写权限由宿主机目录权限决定。"
          >
            <HostToolDirectory />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input />
          </Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
        </ApplicationSettingsForm>
      </Modal>
    </Space>
  );
}
