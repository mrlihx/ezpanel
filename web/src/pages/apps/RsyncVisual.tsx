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
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { request } from '../../api/client';
import type { RsyncReceiver as Receiver } from '../../api/rsync';
import RsyncReceiverEditor from './RsyncReceiverEditor';
import ContentSizedTable from '../../components/ContentSizedTable';

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
  const [globalOpen, setGlobalOpen] = useState(false);
  const [receiver, setReceiver] = useState<Receiver>();
  const [globalForm] = Form.useForm<Settings>();
  const save = useMutation({
    mutationFn: (data: Settings) =>
      request<Settings>(endpoint, { method: 'PUT', data }),
    onSuccess: (next) => {
      client.setQueryData(queryKey, next);
      setGlobalOpen(false);
      setReceiver(undefined);
      message.success('Rsync 配置已保存');
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
          onClick={() =>
            setReceiver({
              name: '',
              enabled: true,
              path: '/www/wwwroot',
              allow: '',
              username:
                'sync_' +
                Array.from(crypto.getRandomValues(new Uint8Array(6)), (byte) =>
                  byte.toString(16).padStart(2, '0'),
                ).join(''),
              password: Array.from(
                crypto.getRandomValues(new Uint8Array(18)),
                (byte) => byte.toString(16).padStart(2, '0'),
              ).join(''),
            })
          }
        >
          创建接收任务
        </Button>
        <Button onClick={() => setGlobalOpen(true)}>全局配置</Button>
        <Tag>
          {data.address || '0.0.0.0'}:{data.port}
        </Tag>
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
          { title: '用户名', dataIndex: 'username' },
          { title: '接收目录', dataIndex: 'path' },
          {
            title: 'IP 白名单',
            dataIndex: 'allow',
            render: (value: string) => value || '沿用全局配置',
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
                      title: `删除接收任务 ${row.username}？`,
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
            extra="远程连接需要放行监听端口，修改监听地址或端口后运行中的服务会自动重启。"
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
      {receiver && (
        <RsyncReceiverEditor
          key={receiver.name || 'new'}
          receiver={receiver}
          saving={save.isPending}
          onClose={() => setReceiver(undefined)}
          onSave={(row) =>
            save.mutateAsync({
              ...data,
              receivers: receiver.name
                ? data.receivers.map((item) =>
                    item.name === receiver.name ? row : item,
                  )
                : [...data.receivers, row],
            })
          }
        />
      )}
    </Space>
  );
}
