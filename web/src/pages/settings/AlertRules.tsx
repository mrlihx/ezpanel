import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Flex,
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
import {
  alertChannels,
  alertRules,
  alertTargets,
  deleteAlertRule,
  saveAlertRule,
  setAlertRuleEnabled,
  type AlertChannel,
  type AlertRule,
  type AlertRuleInput,
  type AlertRuleKind,
} from '../../api/alerts';
import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import ContentSizedTable from '../../components/ContentSizedTable';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';

const kinds = {
  service: '服务异常',
  project: '项目异常',
  certificate: '证书到期',
};
const kindOptions = Object.entries(kinds).map(([value, label]) => ({
  value,
  label,
}));
const states = {
  pending: { label: '待检查', color: 'default' },
  normal: { label: '正常', color: 'success' },
  triggered: { label: '已触发', color: 'warning' },
  error: { label: '检查失败', color: 'error' },
  disabled: { label: '已停用', color: 'default' },
};

function AlertRuleEditor({
  rule,
  channels,
  saving,
  onClose,
  onSave,
  openChannels,
}: {
  rule: AlertRule | null;
  channels: AlertChannel[];
  saving: boolean;
  onClose: () => void;
  onSave: (data: AlertRuleInput) => void;
  openChannels: () => void;
}) {
  const [form] = Form.useForm<AlertRuleInput>();
  const kind = Form.useWatch('kind', form) ?? rule?.kind ?? 'service';
  const targets = useQuery({
    queryKey: ['settings-alerts', 'targets', kind],
    queryFn: () => alertTargets(kind),
  });
  const channelOptions = channels.map((channel) => ({
    value: channel.id,
    label: channel.enabled ? channel.name : `${channel.name}（停用）`,
  }));
  const initialValues: Partial<AlertRuleInput> = rule
    ? { ...rule, days: rule.days || 7 }
    : {
        enabled: true,
        kind: 'service',
        channelIds: channels
          .filter((channel) => channel.enabled && channel.isDefault)
          .map((channel) => channel.id),
        intervalSeconds: 600,
        maxDaily: 1,
        days: 7,
      };
  return (
    <Modal
      title={rule ? '修改告警任务' : '添加告警任务'}
      open
      width={LAYOUT.modalWidth}
      confirmLoading={saving}
      onCancel={onClose}
      onOk={() => form.submit()}
      okText="保存"
    >
      <ApplicationSettingsForm<AlertRuleInput>
        form={form}
        initialValues={initialValues}
        onValuesChange={(values) => {
          if (values.kind) form.setFieldValue('target', undefined);
        }}
        onFinish={(data) => onSave({ ...data, days: data.days ?? 7 })}
      >
        <Form.Item
          name="title"
          label="任务名称"
          rules={[{ required: true, whitespace: true }]}
        >
          <Input maxLength={64} />
        </Form.Item>
        <Form.Item name="kind" label="告警类型" rules={[{ required: true }]}>
          <Select options={kindOptions} />
        </Form.Item>
        <Form.Item
          name="target"
          label={kind === 'certificate' ? 'HTTPS 网站' : '检查对象'}
          rules={[{ required: true }]}
          extra={
            targets.error && (
              <Button
                type="link"
                size="small"
                onClick={() => void targets.refetch()}
              >
                {targets.error.message}，点击重试
              </Button>
            )
          }
        >
          <Select
            showSearch
            optionFilterProp="label"
            loading={targets.isFetching}
            placeholder="请选择检查对象"
            notFoundContent={targets.isFetching ? '加载中' : '暂无可选对象'}
            options={targets.data?.list.map(({ id, name }) => ({
              value: id,
              label: name,
            }))}
          />
        </Form.Item>
        {kind === 'certificate' && (
          <Form.Item
            name="days"
            label="到期阈值"
            rules={[{ required: true, type: 'integer', min: 1, max: 365 }]}
          >
            <InputNumber min={1} max={365} precision={0} addonAfter="天" />
          </Form.Item>
        )}
        <Form.Item
          name="channelIds"
          label="告警方式"
          rules={[{ required: true, type: 'array', min: 1 }]}
          extra={
            channelOptions.length === 0 && (
              <Button type="link" size="small" onClick={openChannels}>
                前往告警设置配置渠道
              </Button>
            )
          }
        >
          <Select
            mode="multiple"
            options={channelOptions}
            optionFilterProp="label"
            placeholder="请选择通知渠道"
          />
        </Form.Item>
        <Form.Item
          name="intervalSeconds"
          label="检查间隔"
          rules={[{ required: true, type: 'integer', min: 60, max: 86400 }]}
        >
          <InputNumber min={60} max={86400} precision={0} addonAfter="秒" />
        </Form.Item>
        <Form.Item
          name="maxDaily"
          label="每日通知上限"
          rules={[{ required: true, type: 'integer', min: 1, max: 100 }]}
        >
          <InputNumber min={1} max={100} precision={0} addonAfter="次" />
        </Form.Item>
        <Form.Item name="enabled" label="启用任务" valuePropName="checked">
          <Switch />
        </Form.Item>
      </ApplicationSettingsForm>
    </Modal>
  );
}

export default function AlertRules({
  active,
  openLogs,
  openChannels,
}: {
  active: boolean;
  openLogs: (rule: AlertRule) => void;
  openChannels: () => void;
}) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const [editing, setEditing] = useState<AlertRule | null>();
  const [kind, setKind] = useState<AlertRuleKind>();
  const [status, setStatus] = useState<keyof typeof states>();
  const [keyword, setKeyword] = useState('');
  const rules = useQuery({
    queryKey: ['settings-alerts', 'rules'],
    queryFn: alertRules,
    enabled: active,
    refetchInterval: 15000,
  });
  const channels = useQuery({
    queryKey: ['settings-alerts', 'channels'],
    queryFn: alertChannels,
  });
  const channelList = channels.data?.list ?? [];
  const channelNames = new Map(channelList.map(({ id, name }) => [id, name]));
  const refresh = () =>
    client.invalidateQueries({ queryKey: ['settings-alerts', 'rules'] });
  const save = useMutation({
    mutationFn: (data: AlertRuleInput) => saveAlertRule(editing?.id, data),
    onSuccess: async () => {
      setEditing(undefined);
      void message.success('告警任务已保存');
      await refresh();
    },
  });
  const toggle = useMutation({
    mutationFn: ({ id, enabled }: Pick<AlertRule, 'id' | 'enabled'>) =>
      setAlertRuleEnabled(id, enabled),
    onSuccess: refresh,
  });
  const rows = rules.data?.list.filter((rule) => {
    const state = rule.enabled ? rule.state : 'disabled';
    return (
      (!kind || rule.kind === kind) &&
      (!status || state === status) &&
      [rule.title, rule.targetName].some((value) =>
        value.toLowerCase().includes(keyword),
      )
    );
  });
  const error = rules.error ?? channels.error;
  return (
    <Flex vertical gap={SPACING.lg}>
      <Flex wrap gap={SPACING.md} justify="space-between">
        <Button
          type="primary"
          disabled={!channels.data}
          onClick={() => setEditing(null)}
        >
          添加告警任务
        </Button>
        <Flex wrap gap={SPACING.sm}>
          <Select
            aria-label="告警类型"
            style={{ width: 130 }}
            allowClear
            placeholder="全部类型"
            options={kindOptions}
            onChange={setKind}
          />
          <Select
            aria-label="任务状态"
            style={{ width: 130 }}
            allowClear
            placeholder="全部状态"
            options={Object.entries(states).map(([value, { label }]) => ({
              value,
              label,
            }))}
            onChange={setStatus}
          />
          <Input.Search
            aria-label="搜索告警任务"
            placeholder="任务名称或检查对象"
            style={{ width: 220, maxWidth: '100%' }}
            allowClear
            onSearch={(value) => setKeyword(value.trim().toLowerCase())}
          />
        </Flex>
      </Flex>
      {error && (
        <Alert
          showIcon
          type="error"
          message={error.message}
          action={
            <Button
              size="small"
              onClick={() =>
                void (rules.error ? rules.refetch() : channels.refetch())
              }
            >
              重试
            </Button>
          }
        />
      )}
      <ContentSizedTable<AlertRule>
        fillColumn="title"
        rowKey="id"
        size="middle"
        loading={rules.isLoading}
        dataSource={rows}
        locale={{
          emptyText:
            kind || status || keyword
              ? '没有符合条件的告警任务'
              : '暂无告警任务',
        }}
        pagination={{ defaultPageSize: 10, showSizeChanger: true }}
        columns={[
          { title: '任务名称', dataIndex: 'title' },
          {
            title: '状态',
            key: 'state',
            render: (_, row) => {
              const state = states[row.enabled ? row.state : 'disabled'];
              return (
                <Tag
                  color={state.color}
                  title={
                    row.lastCheckedAt
                      ? `上次检查 ${formatTime(row.lastCheckedAt)}`
                      : undefined
                  }
                >
                  {state.label}
                </Tag>
              );
            },
          },
          {
            title: '告警方式',
            dataIndex: 'channelIds',
            render: (ids: string[]) =>
              ids
                .map((id) => channelNames.get(id))
                .filter(Boolean)
                .join('、') || '—',
          },
          {
            title: '触发条件',
            key: 'condition',
            render: (_, row) =>
              `${row.targetName || row.target} · ${row.kind === 'certificate' ? `剩余 ≤ ${row.days} 天` : kinds[row.kind]}`,
          },
          {
            title: '启用',
            dataIndex: 'enabled',
            render: (enabled: boolean, row) => (
              <Switch
                aria-label={`启用「${row.title}」`}
                checked={enabled}
                disabled={toggle.isPending}
                loading={toggle.isPending && toggle.variables?.id === row.id}
                onChange={(value) =>
                  toggle.mutate({ id: row.id, enabled: value })
                }
              />
            ),
          },
          {
            title: '操作',
            key: 'actions',
            render: (_, row) => (
              <Space size={SPACING.xs}>
                <Button type="link" size="small" onClick={() => openLogs(row)}>
                  告警记录
                </Button>
                <Button
                  type="link"
                  size="small"
                  onClick={() => setEditing(row)}
                >
                  修改
                </Button>
                <Button
                  type="link"
                  size="small"
                  danger
                  onClick={() =>
                    modal.confirm({
                      title: `删除告警任务「${row.title}」？`,
                      okText: '删除',
                      okButtonProps: { danger: true },
                      onOk: async () => {
                        await deleteAlertRule(row.id);
                        await refresh();
                        void message.success('告警任务已删除');
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
      {editing !== undefined && (
        <AlertRuleEditor
          rule={editing}
          channels={channelList}
          saving={save.isPending}
          onClose={() => setEditing(undefined)}
          onSave={(data) => save.mutate(data)}
          openChannels={() => {
            setEditing(undefined);
            openChannels();
          }}
        />
      )}
    </Flex>
  );
}
