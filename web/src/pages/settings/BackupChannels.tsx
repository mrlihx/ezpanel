import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Switch,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  backupChannels,
  deleteBackupChannel,
  saveBackupChannel,
  testBackupChannel,
  toggleBackupChannel,
  type BackupChannel,
  type BackupChannelInput,
} from '../../api/settings';
import ContentSizedTable from '../../components/ContentSizedTable';

const protocols = { webdav: 'WebDAV', ftp: 'FTP', sftp: 'SFTP' };
const examples = {
  webdav: 'https://storage.example.com/backup',
  ftp: 'ftp://storage.example.com/backup',
  sftp: 'sftp://storage.example.com/backup',
};

export default function BackupChannels() {
  const { message, modal } = App.useApp();
  const [editing, setEditing] = useState<BackupChannel | null>();
  const [form] = Form.useForm<BackupChannelInput>();
  const protocol = Form.useWatch('protocol', form) ?? 'webdav';
  const client = useQueryClient();
  const list = useQuery({
    queryKey: ['backup-channels'],
    queryFn: backupChannels,
  });
  async function refresh() {
    await Promise.all([
      list.refetch(),
      client.invalidateQueries({ queryKey: ['backup-storages'] }),
      client.invalidateQueries({ queryKey: ['crons'] }),
    ]);
  }
  function openEditor(channel?: BackupChannel) {
    form.resetFields();
    if (channel) form.setFieldsValue(channel);
    setEditing(channel ?? null);
  }
  const save = useMutation({
    mutationFn: ({ id, data }: { id?: string; data: BackupChannelInput }) =>
      saveBackupChannel(id, data),
    onSuccess: async (_, { id }) => {
      setEditing(undefined);
      await refresh();
      void message.success(id ? '备份渠道已修改' : '备份渠道已新增');
    },
  });
  const toggle = useMutation({
    mutationFn: ({ id, enabled }: Pick<BackupChannel, 'id' | 'enabled'>) =>
      toggleBackupChannel(id, enabled),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: deleteBackupChannel,
    onSuccess: async () => {
      await refresh();
      void message.success('备份渠道已删除');
    },
  });
  const test = useMutation({
    mutationFn: testBackupChannel,
    onSuccess: () => void message.success('连接成功，备份目录可访问'),
  });
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Button type="primary" onClick={() => openEditor()}>
        新增备份渠道
      </Button>
      {list.error && <Alert type="error" message={list.error.message} />}
      <ContentSizedTable<BackupChannel>
        fillColumn="endpoint"
        rowKey="id"
        size="middle"
        loading={list.isFetching}
        dataSource={list.data}
        locale={{ emptyText: '暂无备份渠道' }}
        pagination={{ defaultPageSize: 10, showSizeChanger: true }}
        columns={[
          { title: '渠道名称', dataIndex: 'name' },
          {
            title: '类型',
            dataIndex: 'protocol',
            render: (value: keyof typeof protocols) => protocols[value],
          },
          { title: '服务器地址', dataIndex: 'endpoint' },
          {
            title: '账号',
            dataIndex: 'username',
            render: (value: string) => value || '—',
          },
          {
            title: '状态',
            dataIndex: 'enabled',
            render: (value: boolean, row) => (
              <Switch
                aria-label={`${row.name} 状态`}
                checked={value}
                checkedChildren="启用"
                unCheckedChildren="停用"
                loading={toggle.isPending && toggle.variables?.id === row.id}
                onChange={(enabled) => toggle.mutate({ id: row.id, enabled })}
              />
            ),
          },
          {
            title: '操作',
            key: 'actions',
            render: (_, row) => (
              <Space size={12}>
                <Button
                  type="link"
                  size="small"
                  onClick={() => openEditor(row)}
                >
                  修改
                </Button>
                <Button
                  type="link"
                  size="small"
                  loading={
                    test.isPending &&
                    test.variables?.id === row.id &&
                    !test.variables.config
                  }
                  disabled={test.isPending}
                  onClick={() => test.mutate({ id: row.id })}
                >
                  测试连接
                </Button>
                <Button
                  type="link"
                  size="small"
                  danger
                  disabled={remove.isPending}
                  onClick={() =>
                    modal.confirm({
                      title: `删除备份渠道「${row.name}」？`,
                      okText: '删除',
                      okButtonProps: { danger: true },
                      onOk: () => remove.mutateAsync(row.id),
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
        title={editing ? '修改备份渠道' : '新增备份渠道'}
        open={editing !== undefined}
        onCancel={() => setEditing(undefined)}
        onOk={() => form.submit()}
        confirmLoading={save.isPending}
        forceRender
        okText="保存"
        footer={(_, { CancelBtn, OkBtn }) => (
          <Space wrap>
            <Button
              loading={test.isPending && !!test.variables?.config}
              disabled={test.isPending || save.isPending}
              onClick={() =>
                void form
                  .validateFields()
                  .then((config) => test.mutate({ id: editing?.id, config }))
                  .catch(() => {})
              }
            >
              测试连接
            </Button>
            <CancelBtn />
            <OkBtn />
          </Space>
        )}
      >
        <Form
          form={form}
          layout="vertical"
          initialValues={{ protocol: 'webdav', enabled: true }}
          onFinish={(data) => save.mutate({ id: editing?.id, data })}
        >
          <Form.Item
            name="name"
            label="渠道名称"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item
            name="protocol"
            label="渠道类型"
            rules={[{ required: true }]}
          >
            <Select
              options={Object.entries(protocols).map(([value, label]) => ({
                value,
                label,
              }))}
            />
          </Form.Item>
          <Form.Item
            name="endpoint"
            label="服务器目录地址"
            rules={[{ required: true }]}
          >
            <Input placeholder={examples[protocol]} />
          </Form.Item>
          <Form.Item
            name="username"
            label="账号"
            rules={[{ required: protocol !== 'webdav' }]}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="password" label="密码">
            <Input.Password
              autoComplete="new-password"
              placeholder={editing ? '留空沿用已有密码' : undefined}
            />
          </Form.Item>
          {protocol === 'sftp' && (
            <Form.Item
              name="fingerprint"
              label="主机公钥指纹"
              rules={[{ required: true }]}
            >
              <Input placeholder="SHA256:…" />
            </Form.Item>
          )}
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </Space>
  );
}
