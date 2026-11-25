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
  Select,
  Space,
  Switch,
  Tag,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { request } from '../../api/client';
import { rsyncWorkspaceQuery } from '../../api/rsync';
import ContentSizedTable from '../../components/ContentSizedTable';
import HostToolDirectory from './HostToolDirectory';

type Receiver = {
  name: string;
  path: string;
  username: string;
  password?: string;
  enabled: boolean;
  readOnly: boolean;
  exclude: string;
};
type Settings = {
  revision: string;
  address: string;
  port: number;
  maxConnections: number;
  timeout: number;
  dontCompress: string;
  allow: string;
  receivers: Receiver[];
};

const endpoint = '/api/software/rsync/visual';
const queryKey = ['rsync-visual'];

export default function RsyncVisual({
  running,
  busy,
  onToggle,
}: {
  running: boolean;
  busy: boolean;
  onToggle: () => void;
}) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const config = useQuery({
    queryKey,
    queryFn: () => request<Settings>(endpoint),
  });
  const workspace = useQuery(rsyncWorkspaceQuery);
  const [globalOpen, setGlobalOpen] = useState(false);
  const [receiver, setReceiver] = useState<Partial<Receiver>>();
  const [globalForm] = Form.useForm<Settings>();
  const [receiverForm] = Form.useForm<Receiver>();
  const save = useMutation({
    mutationFn: (data: Settings) =>
      request<Settings>(endpoint, { method: 'PUT', data }),
    onSuccess: (next) => {
      client.setQueryData(queryKey, next);
      setGlobalOpen(false);
      setReceiver(undefined);
      message.success('Rsync 配置已保存，端口或监听地址修改后请重启服务');
    },
  });
  const data = config.data;
  if (config.error)
    return <Alert type="error" message={config.error.message} />;
  if (!data) return null;
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Space wrap>
        <Button
          type="primary"
          onClick={() => setReceiver({ enabled: true, readOnly: false })}
        >
          创建接收任务
        </Button>
        <Button onClick={() => setGlobalOpen(true)}>全局配置</Button>
        <Tag>TCP {data.port}</Tag>
        <Space>
          <span>接收服务</span>
          <Switch
            aria-label="接收服务"
            checked={running}
            loading={busy}
            onChange={onToggle}
          />
        </Space>
      </Space>
      <ContentSizedTable<Receiver>
        fillColumn="path"
        rowKey="name"
        dataSource={data.receivers}
        pagination={false}
        scroll={{ x: 'max-content' }}
        columns={[
          { title: '模块名称', dataIndex: 'name' },
          { title: '用户名', dataIndex: 'username' },
          { title: '接收目录', dataIndex: 'path' },
          {
            title: '访问权限',
            render: (_, row) => (row.readOnly ? '只读' : '仅接收'),
          },
          {
            title: '接收开关',
            render: (_, row) => (
              <Switch
                aria-label={`${row.name} 接收开关`}
                checked={row.enabled}
                disabled={save.isPending}
                onChange={(enabled) =>
                  save.mutate({
                    ...data,
                    receivers: data.receivers.map((item) =>
                      item.name === row.name ? { ...item, enabled } : item,
                    ),
                  })
                }
              />
            ),
          },
          {
            title: '操作',
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  size="small"
                  onClick={() => setReceiver(row)}
                >
                  编辑
                </Button>
                <Button
                  type="link"
                  size="small"
                  danger
                  onClick={() =>
                    modal.confirm({
                      title: `删除接收任务 ${row.name}？`,
                      onOk: () =>
                        save.mutateAsync({
                          ...data,
                          receivers: data.receivers.filter(
                            (item) => item.name !== row.name,
                          ),
                        }),
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
        title="Rsync 全局配置"
        open={globalOpen}
        destroyOnHidden
        onCancel={() => setGlobalOpen(false)}
        onOk={() => globalForm.submit()}
        confirmLoading={save.isPending}
      >
        <ApplicationSettingsForm
          form={globalForm}

          preserve={false}
          initialValues={data}
          onFinish={(values) => save.mutate({ ...data, ...values })}
        >
          <Form.Item name="address" label="监听地址">
            <Input placeholder="留空监听所有地址" />
          </Form.Item>
          <Form.Item
            name="port"
            label="监听端口"
            rules={[{ required: true }]}
            extra="远程连接需要放行监听端口。"
          >
            <InputNumber min={1} max={65535} precision={0} />
          </Form.Item>
          <Form.Item
            name="maxConnections"
            label="最大连接数"
            rules={[{ required: true }]}
            extra="0 表示不限制。"
          >
            <InputNumber min={0} max={10000} precision={0} />
          </Form.Item>
          <Form.Item
            name="timeout"
            label="超时时间（秒）"
            rules={[{ required: true }]}
          >
            <InputNumber min={0} max={86400} precision={0} />
          </Form.Item>
          <Form.Item name="dontCompress" label="不压缩的文件">
            <Input placeholder="例如 *.gz, *.zip, *.mp4" />
          </Form.Item>
          <Form.Item name="allow" label="IP 白名单">
            <Input.TextArea
              rows={3}
              placeholder="每行一个 IP 地址或 CIDR 网段"
            />
          </Form.Item>
        </ApplicationSettingsForm>
      </Modal>
      <Modal
        title={receiver?.name ? '编辑接收任务' : '创建接收任务'}
        open={!!receiver}
        destroyOnHidden
        onCancel={() => setReceiver(undefined)}
        onOk={() => receiverForm.submit()}
        confirmLoading={save.isPending}
      >
        <ApplicationSettingsForm
          form={receiverForm}

          preserve={false}
          initialValues={receiver}
          onFinish={(row) =>
            save.mutate({
              ...data,
              receivers: receiver?.name
                ? data.receivers.map((item) =>
                    item.name === row.name ? row : item,
                  )
                : [...data.receivers, row],
            })
          }
        >
          <Form.Item name="name" label="模块名称" rules={[{ required: true }]}>
            <Input disabled={!!receiver?.name} />
          </Form.Item>
          <Form.Item name="path" label="接收目录" rules={[{ required: true }]}>
            <HostToolDirectory />
          </Form.Item>
          <Form.Item
            name="username"
            label="用户名"
            rules={[{ required: true }]}
          >
            <Input />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={[{ required: !receiver?.name }]}
          >
            <Input.Password
              placeholder={receiver?.name ? '留空保留原密码' : '输入认证密码'}
            />
          </Form.Item>
          {!!workspace.data?.rules.length && (
            <Form.Item label="填入排除规则">
              <Select
                placeholder="从已保存的规则填入"
                options={workspace.data.rules.map((rule) => ({
                  value: rule.id,
                  label: rule.name,
                }))}
                onChange={(id) =>
                  receiverForm.setFieldValue(
                    'exclude',
                    workspace.data.rules.find((rule) => rule.id === id)
                      ?.patterns ?? '',
                  )
                }
              />
            </Form.Item>
          )}
          <Form.Item name="exclude" label="排除规则">
            <Input.TextArea rows={3} placeholder="每行一个匹配规则" />
          </Form.Item>
          <Form.Item name="enabled" label="启用接收" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="readOnly" label="只读" valuePropName="checked">
            <Switch />
          </Form.Item>
        </ApplicationSettingsForm>
      </Modal>
    </Space>
  );
}
