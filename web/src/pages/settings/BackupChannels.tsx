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
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  backupChannelProviders,
  backupChannels,
  deleteBackupChannel,
  saveBackupChannel,
  testBackupChannel,
  toggleBackupChannel,
  type BackupChannel,
  type BackupChannelInput,
} from '../../api/settings';
import ContentSizedTable from '../../components/ContentSizedTable';
import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';

const protocols = { webdav: 'WebDAV', ftp: 'FTP', sftp: 'SFTP' };
const examples = {
  webdav: 'https://storage.example.com/backup',
  ftp: 'storage.example.com',
  sftp: 'storage.example.com',
};

export default function BackupChannels() {
  const { message, modal } = App.useApp();
  const [editing, setEditing] = useState<BackupChannel | null>();
  const [form] = Form.useForm<BackupChannelInput>();
  const protocol = Form.useWatch('protocol', form) ?? 'webdav';
  const builtin =
    protocol === 'webdav' || protocol === 'ftp' || protocol === 'sftp';
  const client = useQueryClient();
  const list = useQuery({
    queryKey: ['backup-channels'],
    queryFn: backupChannels,
  });
  const providers = useQuery({
    queryKey: ['backup-channel-providers'],
    queryFn: backupChannelProviders,
  });
  const provider = providers.data?.find((item) => item.protocol === protocol);
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
      {providers.error && (
        <Alert type="error" message={providers.error.message} />
      )}
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
            render: (value: keyof typeof protocols, row) =>
              row.provider ?? protocols[value],
          },
          { title: '服务器地址', dataIndex: 'endpoint' },
          {
            title: '端口',
            dataIndex: 'port',
            render: (value: number | undefined) => value ?? '—',
          },
          {
            title: '备份目录',
            dataIndex: 'directory',
            render: (value: string | undefined) => value ?? '—',
          },
          {
            title: '账号',
            dataIndex: 'username',
            render: (value: string) => value || '—',
          },
          {
            title: '状态',
            dataIndex: 'enabled',
            render: (value: boolean, row) =>
              row.unavailable ? (
                '插件不可用'
              ) : (
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
                  disabled={row.unavailable}
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
                  disabled={test.isPending || row.unavailable}
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
        <ApplicationSettingsForm
          form={form}
          initialValues={{ protocol: 'webdav', enabled: true, directory: '/' }}
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
              onChange={(next) => {
                form.setFieldValue('fields', undefined);
                form.setFieldValue(
                  'port',
                  next === 'sftp' ? 22 : next === 'ftp' ? 21 : undefined,
                );
                if (next === 'webdav' || protocol === 'webdav' || !builtin)
                  form.setFieldValue('endpoint', '');
              }}
              options={[
                ...Object.entries(protocols).map(([value, label]) => ({
                  value,
                  label,
                  disabled: !!editing?.provider,
                })),
                ...(providers.data ?? []).map((item) => ({
                  value: item.protocol,
                  label: item.name,
                  disabled: !!editing && editing.protocol !== item.protocol,
                })),
              ]}
            />
          </Form.Item>
          {provider?.fields.map((field) => (
            <Form.Item
              key={`${provider.protocol}:${field.key}`}
              name={['fields', field.key]}
              label={field.label}
              rules={[
                {
                  required:
                    field.required && !(editing && field.type === 'password'),
                },
              ]}
              preserve={false}
            >
              {field.type === 'password' ? (
                <Input.Password
                  autoComplete="new-password"
                  placeholder={editing ? '留空沿用已保存的凭据' : undefined}
                />
              ) : field.type === 'select' ? (
                <Select
                  options={field.options?.map((value) => ({
                    value,
                    label: value,
                  }))}
                  allowClear={!field.required}
                />
              ) : (
                <Input
                  type={field.type === 'number' ? 'number' : 'text'}
                  maxLength={4096}
                />
              )}
            </Form.Item>
          ))}
          {builtin && (
            <>
              <Form.Item
                name="endpoint"
                label={protocol === 'webdav' ? '服务器目录地址' : '服务器地址'}
                rules={[{ required: true }]}
                preserve={false}
              >
                <Input placeholder={examples[protocol]} />
              </Form.Item>
              {protocol !== 'webdav' && (
                <Form.Item
                  name="port"
                  label="端口"
                  rules={[{ required: true }]}
                  initialValue={protocol === 'sftp' ? 22 : 21}
                  preserve={false}
                >
                  <InputNumber
                    min={1}
                    max={65535}
                    precision={0}
                    style={{ width: '100%' }}
                  />
                </Form.Item>
              )}
              <Form.Item
                name="username"
                label="账号"
                rules={[{ required: protocol !== 'webdav' }]}
                preserve={false}
              >
                <Input autoComplete="off" />
              </Form.Item>
              <Form.Item name="password" label="密码" preserve={false}>
                <Input.Password
                  autoComplete="new-password"
                  placeholder={editing ? '留空沿用已有密码' : undefined}
                />
              </Form.Item>
              {protocol !== 'webdav' && (
                <Form.Item
                  name="directory"
                  label="备份目录"
                  rules={[{ required: true, whitespace: true }]}
                  preserve={false}
                  extra="填写远程服务器上的目录，例如 /backup。"
                >
                  <Input placeholder="/backup" maxLength={4096} />
                </Form.Item>
              )}
            </>
          )}
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
        </ApplicationSettingsForm>
      </Modal>
    </Space>
  );
}
