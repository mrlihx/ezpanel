import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Switch,
  Tag,
  Typography,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  alertChannelName,
  alertChannels,
  deleteAlertChannel,
  saveAlertChannel,
  testAlertChannel,
  type AlertChannel,
  type AlertChannelInput,
  type AlertChannelKind,
} from '../../api/alerts';
import { usePluginList } from '../../api/plugin';
import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import ContentSizedTable from '../../components/ContentSizedTable';
import { LAYOUT, SPACING } from '../../styles/tokens';

function AlertChannelEditor({
  kind,
  label,
  channel,
  defaultChannel,
  saving,
  onClose,
  onSave,
}: {
  kind: AlertChannelKind;
  label: string;
  channel?: AlertChannel;
  defaultChannel: boolean;
  saving: boolean;
  onClose: () => void;
  onSave: (data: AlertChannelInput) => void;
}) {
  const [form] = Form.useForm<AlertChannelInput>();
  const method = Form.useWatch('method', form) ?? channel?.method ?? 'POST';
  const clearPassword = Form.useWatch('clearPassword', form);
  return (
    <Modal
      title={`${channel ? '修改' : '添加'}${label}配置`}
      open
      width={LAYOUT.modalWidth}
      confirmLoading={saving}
      onCancel={onClose}
      onOk={() => form.submit()}
      okText="保存"
    >
      <ApplicationSettingsForm<AlertChannelInput>
        form={form}
        initialValues={
          channel ?? {
            enabled: true,
            isDefault: defaultChannel,
            smtpPort: 587,
            smtpMode: 'starttls',
            method: 'POST',
            format: 'json',
            verifyTLS: true,
          }
        }
        onFinish={(values) => onSave({ ...values, kind })}
      >
        <Form.Item
          name="name"
          label="配置名称"
          rules={[{ required: true, whitespace: true }]}
        >
          <Input maxLength={64} />
        </Form.Item>
        {kind === 'email' ? (
          <>
            <Form.Item
              name="smtpHost"
              label="SMTP 服务器"
              rules={[{ required: true, whitespace: true }]}
            >
              <Input placeholder="域名或 IP 地址" />
            </Form.Item>
            <Form.Item
              name="smtpPort"
              label="SMTP 端口"
              rules={[{ required: true, type: 'integer', min: 1, max: 65535 }]}
            >
              <InputNumber min={1} max={65535} precision={0} />
            </Form.Item>
            <Form.Item
              name="smtpMode"
              label="加密方式"
              rules={[{ required: true }]}
            >
              <Select
                options={[
                  { value: 'starttls', label: 'STARTTLS' },
                  { value: 'tls', label: 'SSL/TLS' },
                  { value: 'plain', label: '无加密' },
                ]}
              />
            </Form.Item>
            <Form.Item
              name="from"
              label="发件邮箱"
              rules={[{ required: true, whitespace: true }]}
            >
              <Input />
            </Form.Item>
            <Form.Item name="username" label="SMTP 用户名">
              <Input autoComplete="off" placeholder="留空使用发件邮箱" />
            </Form.Item>
            <Form.Item name="password" label="密码或授权码">
              <Input.Password
                autoComplete="new-password"
                disabled={clearPassword}
                placeholder={
                  channel?.hasPassword
                    ? '留空沿用已有密码'
                    : '需要 SMTP 认证时填写'
                }
              />
            </Form.Item>
            {channel?.hasPassword && (
              <Form.Item
                name="clearPassword"
                valuePropName="checked"
                label="已有密码"
              >
                <Checkbox
                  onChange={(event) => {
                    if (event.target.checked)
                      form.setFieldValue('password', undefined);
                  }}
                >
                  清除已有密码
                </Checkbox>
              </Form.Item>
            )}
            <Form.Item
              name="recipients"
              label="收件邮箱"
              rules={[{ required: true, type: 'array', min: 1 }]}
            >
              <Select
                mode="tags"
                tokenSeparators={['\n', ',', '，', ';', '；']}
                placeholder="输入邮箱后按回车，支持粘贴多行"
              />
            </Form.Item>
          </>
        ) : kind === 'webhook' ? (
          <>
            <Form.Item
              name="url"
              label="Webhook 地址"
              rules={[{ required: !channel?.hasURL, whitespace: true }]}
            >
              <Input.Password
                autoComplete="new-password"
                placeholder={
                  channel?.hasURL ? '留空沿用已有地址' : 'HTTP 或 HTTPS 地址'
                }
              />
            </Form.Item>
            <Form.Item
              name="method"
              label="请求方法"
              rules={[{ required: true }]}
            >
              <Select
                options={['GET', 'POST', 'PUT', 'PATCH'].map((value) => ({
                  value,
                  label: value,
                }))}
              />
            </Form.Item>
            {method !== 'GET' && (
              <Form.Item
                name="format"
                label="数据格式"
                rules={[{ required: true }]}
              >
                <Select
                  options={[
                    { value: 'json', label: 'JSON' },
                    { value: 'form', label: '表单（URL 编码）' },
                  ]}
                />
              </Form.Item>
            )}
            <Form.Item
              name="params"
              label="请求参数"
              extra={
                channel?.hasParams
                  ? '留空沿用已有参数，填 {} 发送空对象。支持 {{title}} 和 {{message}}。'
                  : '留空发送默认标题和正文。支持 {{title}} 和 {{message}}。'
              }
            >
              <Input.TextArea
                autoSize={{ minRows: 2, maxRows: 5 }}
                placeholder={'{"title":"{{title}}","message":"{{message}}"}'}
              />
            </Form.Item>
            <Form.Item
              name="headers"
              label="请求头"
              extra={
                channel?.hasHeaders
                  ? '留空沿用已有请求头，填 {} 清空。'
                  : '可选，填写 JSON 对象。'
              }
            >
              <Input.TextArea
                autoSize={{ minRows: 2, maxRows: 5 }}
                placeholder={'{"Authorization":"Bearer …"}'}
              />
            </Form.Item>
            <Form.Item
              name="verifyTLS"
              label="校验证书"
              valuePropName="checked"
            >
              <Switch />
            </Form.Item>
          </>
        ) : (
          <Form.Item name="target" label="通知目标">
            <Input maxLength={256} placeholder="填写插件要求的接收人或群组" />
          </Form.Item>
        )}
        <Form.Item name="enabled" label="启用配置" valuePropName="checked">
          <Switch />
        </Form.Item>
        <Form.Item name="isDefault" label="设为默认" valuePropName="checked">
          <Switch />
        </Form.Item>
      </ApplicationSettingsForm>
    </Modal>
  );
}

export default function AlertChannels() {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const [editing, setEditing] = useState<{
    kind: AlertChannelKind;
    channel?: AlertChannel;
  }>();
  const channels = useQuery({
    queryKey: ['settings-alerts', 'channels'],
    queryFn: alertChannels,
  });
  const plugins = usePluginList(true);
  const refresh = () =>
    client.invalidateQueries({ queryKey: ['settings-alerts', 'channels'] });
  const save = useMutation({
    mutationFn: (data: AlertChannelInput) =>
      saveAlertChannel(editing?.channel?.id, data),
    onSuccess: async () => {
      setEditing(undefined);
      void message.success('告警渠道已保存');
      await refresh();
    },
  });
  const test = useMutation({
    mutationFn: testAlertChannel,
    onSuccess: ({ success, error }) => {
      if (success) void message.success('测试通知已发送');
      else void message.error(error || '测试通知发送失败');
      void client.invalidateQueries({ queryKey: ['settings-alerts', 'logs'] });
    },
  });
  const list = channels.data?.list ?? [];
  const groups = new Map<
    AlertChannelKind,
    { label: string; available: boolean }
  >([
    ['email', { label: alertChannelName('email'), available: true }],
    ['webhook', { label: alertChannelName('webhook'), available: true }],
  ]);
  for (const plugin of plugins.data ?? []) {
    if (plugin.notificationProvider) {
      groups.set(`plugin:${plugin.id}`, {
        label: plugin.notificationProvider.title,
        available: plugin.runtimeStatus === 'running',
      });
    }
  }
  for (const row of list) {
    if (!groups.has(row.kind))
      groups.set(row.kind, {
        label: alertChannelName(row.kind),
        available: false,
      });
  }
  return (
    <Flex vertical gap={SPACING.xl}>
      {channels.error && (
        <Alert
          type="error"
          showIcon
          message={channels.error.message}
          action={
            <Button size="small" onClick={() => void channels.refetch()}>
              重试
            </Button>
          }
        />
      )}
      {[...groups].map(([kind, { label, available }]) => {
        const rows = list.filter((channel) => channel.kind === kind);
        if (!available && !rows.length) return null;
        return (
          <Flex key={kind} vertical gap={SPACING.md}>
            <Flex align="center" gap={SPACING.md} justify="space-between" wrap>
              <Typography.Title level={5} style={{ margin: 0 }}>
                {label}
              </Typography.Title>
              <Button
                type="primary"
                disabled={!channels.data || !available}
                onClick={() => setEditing({ kind })}
              >
                添加{label}配置
              </Button>
            </Flex>
            <ContentSizedTable<AlertChannel>
              fillColumn="name"
              rowKey="id"
              size="middle"
              loading={channels.isFetching}
              dataSource={rows}
              pagination={false}
              locale={{ emptyText: `暂无${label}配置` }}
              columns={[
                {
                  title: '配置名称',
                  dataIndex: 'name',
                  render: (name: string, row) => (
                    <Space size={SPACING.sm}>
                      {name}
                      {row.isDefault && <Tag>默认</Tag>}
                    </Space>
                  ),
                },
                kind === 'email'
                  ? {
                      title: 'SMTP 服务器',
                      key: 'server',
                      render: (_, row) => `${row.smtpHost}:${row.smtpPort}`,
                    }
                  : kind === 'webhook'
                    ? {
                        title: '请求方法',
                        dataIndex: 'method',
                      }
                    : {
                        title: '通知目标',
                        dataIndex: 'target',
                      },
                {
                  title: '状态',
                  dataIndex: 'enabled',
                  render: (enabled: boolean) => (
                    <Tag color={enabled ? 'success' : 'default'}>
                      {enabled ? '已启用' : '已停用'}
                    </Tag>
                  ),
                },
                {
                  title: '操作',
                  key: 'actions',
                  render: (_, row) => (
                    <Space size={SPACING.xs}>
                      <Button
                        type="link"
                        size="small"
                        onClick={() => setEditing({ kind, channel: row })}
                      >
                        修改
                      </Button>
                      <Button
                        type="link"
                        size="small"
                        loading={test.isPending && test.variables === row.id}
                        disabled={test.isPending}
                        onClick={() => test.mutate(row.id)}
                      >
                        测试发送
                      </Button>
                      <Button
                        type="link"
                        size="small"
                        danger
                        onClick={() =>
                          modal.confirm({
                            title: `删除告警渠道「${row.name}」？`,
                            okText: '删除',
                            okButtonProps: { danger: true },
                            onOk: async () => {
                              await deleteAlertChannel(row.id);
                              await refresh();
                              void message.success('告警渠道已删除');
                            },
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
          </Flex>
        );
      })}
      {editing && (
        <AlertChannelEditor
          {...editing}
          label={
            groups.get(editing.kind)?.label ?? alertChannelName(editing.kind)
          }
          defaultChannel={
            !list.some(
              (channel) => channel.kind === editing.kind && channel.isDefault,
            )
          }
          saving={save.isPending}
          onClose={() => setEditing(undefined)}
          onSave={(data) => save.mutate(data)}
        />
      )}
    </Flex>
  );
}
