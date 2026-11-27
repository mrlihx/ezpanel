import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Empty,
  Flex,
  Form,
  Input,
  Modal,
  Radio,
  Select,
  Spin,
  Table,
  Tag,
  Typography,
  type TableColumnsType,
} from 'antd';
import { request } from '../../api/client';
import {
  pluginAction,
  pluginCredentials,
  type Plugin,
  type PluginEnv,
} from '../../api/plugin';
import TaskDrawer from '../../components/TaskDrawer';
import { SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';
import PluginEnvInput from './PluginEnvInput';
import '../apps/apps.css';

const scopeFields = {
  sites: 'siteIds',
  containers: 'containerIds',
  databases: 'databaseIds',
  certificates: 'certificateIds',
  compose: 'composeNames',
  apps: 'appIds',
} as const;
type ResourceKind = keyof typeof scopeFields;
type ResourceSelection = { mode: 'off' | 'all' | 'selected'; ids: string[] };
type Grant = {
  allResources?: ResourceKind[];
  siteIds?: string[];
  containerIds?: string[];
  databaseIds?: string[];
  certificateIds?: string[];
  composeNames?: string[];
  appIds?: string[];
  credentials?: Record<string, string>;
  channelId?: string;
  schedules?: Record<string, string>;
  events?: string[];
};
type GrantForm = Grant & {
  resources?: Partial<Record<ResourceKind, ResourceSelection>>;
};
type Option = { id: string; name: string; kind?: string };
type Scope = {
  kind: ResourceKind;
  label: string;
  options: Option[] | null;
  unavailable: boolean;
  disabled?: boolean;
  disabledPermissions?: string[];
};
type Job = {
  id: string;
  title: string;
  params?: PluginEnv[];
  webhook?: boolean;
};
type Run = {
  taskId: string;
  target: string;
  status: string;
  startedAt: string;
};
type Delivery = {
  id: string;
  eventName: string;
  status: string;
  attempts: number;
  lastError?: string;
  updatedAt: string;
};
type Capabilities = {
  grant: Grant;
  scopes: Scope[];
  channels: Option[] | null;
  credentialSlots: { id: string; title: string }[] | null;
  notifications: boolean;
  jobs: Job[] | null;
  events: string[] | null;
  tasks: Run[];
  deliveries: Delivery[];
  running: boolean;
  disabledPermissions?: string[];
};

const eventPrerequisites: Record<string, string> = {
  'site.backup.finished': 'backups.read',
  'certificate.renewal.finished': 'certificates.read',
  'site.project.finished': 'sites.read',
  'compose.task.finished': 'compose.read',
  'database.backup.finished': 'databases.backup',
};

const eventTitles: Record<string, string> = {
  'site.backup.finished': '网站备份完成',
  'certificate.renewal.finished': '证书续期完成',
  'site.project.finished': '网站项目任务完成',
  'compose.task.finished': 'Compose 任务完成',
  'database.backup.finished': '数据库备份任务完成',
};
const statuses: Record<string, string> = {
  running: '运行中',
  success: '成功',
  failed: '失败',
  interrupted: '已中断',
  pending: '待投递',
  sent: '已投递',
  skipped: '已跳过',
};

export default function PluginCapabilities({ plugin }: { plugin: Plugin }) {
  const client = useQueryClient();
  const { message } = App.useApp();
  const [busy, setBusy] = useState(false);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [runningJob, setRunningJob] = useState<string>();
  const [parameterJob, setParameterJob] = useState<Job>();
  const [form] = Form.useForm<GrantForm>();
  const endpoint = `/api/plugins/${encodeURIComponent(plugin.id)}/capabilities`;
  const query = useQuery({
    queryKey: ['plugins', 'capabilities', plugin.id],
    queryFn: ({ signal }) => request<Capabilities>(endpoint, { signal }),
  });
  const data = query.data;
  const disabledSet = new Set(data?.disabledPermissions ?? []);
  const credentialsDisabled = disabledSet.has('credentials.read');
  const notificationsDisabled = disabledSet.has('notifications.send');
  const eventsDisabled = disabledSet.has('events.subscribe');
  const jobsDisabled = disabledSet.has('jobs.run');
  const webhookDisabled = disabledSet.has('jobs.webhook');
  const hasSettings = !!(
    data?.scopes.length ||
    data?.credentialSlots?.length ||
    data?.events?.length ||
    data?.jobs?.length ||
    data?.notifications
  );
  const credentials = useQuery({
    queryKey: ['plugin-credentials'],
    queryFn: pluginCredentials,
    enabled: !!data?.credentialSlots?.length,
  });
  async function save({ resources, ...values }: GrantForm) {
    const allResources: ResourceKind[] = [];
    const grant: Grant = { ...values, allResources };
    for (const scope of data?.scopes ?? []) {
      const selection = resources?.[scope.kind];
      if (selection?.mode === 'all') allResources.push(scope.kind);
      grant[scopeFields[scope.kind]] =
        selection?.mode === 'selected' ? selection.ids : [];
    }
    setBusy(true);
    try {
      await request(endpoint, { method: 'PUT', data: grant });
      await query.refetch();
      await Promise.all([
        client.invalidateQueries({ queryKey: ['plugins', 'site-actions'] }),
        client.invalidateQueries({ queryKey: ['plugins', 'resource-actions'] }),
        client.invalidateQueries({
          queryKey: ['plugins', 'choices', plugin.id],
        }),
      ]);
      void message.success('插件授权和计划已保存');
    } catch {
      // 请求层已显示保存失败原因。
    } finally {
      setBusy(false);
    }
  }
  async function run(job: string, params?: Record<string, string>) {
    setRunningJob(job);
    try {
      const result = await pluginAction<{ taskId: string }>(
        plugin.id,
        `jobs/${encodeURIComponent(job)}/run`,
        params ? { params } : undefined,
      );
      setParameterJob(undefined);
      setTaskId(result.taskId);
      void query.refetch();
    } catch (error) {
      void message.error(
        error instanceof Error ? error.message : '任务启动失败',
      );
    } finally {
      setRunningJob(undefined);
    }
  }
  const columns: TableColumnsType<Run> = [
    { title: '任务', dataIndex: 'target' },
    {
      title: '状态',
      dataIndex: 'status',
      render: (status: string) => statuses[status] ?? status,
    },
    { title: '开始时间', dataIndex: 'startedAt', render: formatTime },
    {
      title: '操作',
      render: (_, row) => (
        <Button type="link" onClick={() => setTaskId(row.taskId)}>
          查看日志
        </Button>
      ),
    },
  ];
  const deliveryColumns: TableColumnsType<Delivery> = [
    {
      title: '事件',
      dataIndex: 'eventName',
      render: (name: string) => eventTitles[name] ?? name,
    },
    {
      title: '状态',
      dataIndex: 'status',
      render: (status: string) => statuses[status] ?? status,
    },
    { title: '尝试次数', dataIndex: 'attempts' },
    { title: '最后更新', dataIndex: 'updatedAt', render: formatTime },
    { title: '失败原因', dataIndex: 'lastError' },
  ];

  return (
    <>
      {query.error && <Alert type="error" message={query.error.message} />}
      {query.isPending && (
        <Flex justify="center">
          <Spin />
        </Flex>
      )}
      {data && !hasSettings && (
        <Empty description="此插件无需资源授权或执行计划" />
      )}
      {data && hasSettings && (
        <div className="app-settings-stack">
          <Form
            form={form}
            disabled={busy}
            layout="horizontal"
            labelCol={{ flex: '128px' }}
            wrapperCol={{ flex: 1 }}
            labelAlign="left"
            colon={false}
            initialValues={{
              ...data.grant,
              resources: Object.fromEntries(
                data.scopes.map((scope) => {
                  const saved = data.grant[scopeFields[scope.kind]] ?? [];
                  const ids = scope.unavailable
                    ? saved
                    : saved.filter((id) =>
                        scope.options?.some((row) => row.id === id),
                      );
                  return [
                    scope.kind,
                    {
                      mode: data.grant.allResources?.includes(scope.kind)
                        ? 'all'
                        : ids.length
                          ? 'selected'
                          : 'off',
                      ids,
                    },
                  ];
                }),
              ),
              channelId: data.channels?.some(
                (channel) => channel.id === data.grant.channelId,
              )
                ? data.grant.channelId
                : undefined,
              events: data.grant.events?.filter((name) =>
                data.events?.includes(name),
              ),
            }}
            onFinish={save}
          >
            <div className="app-settings-stack">
              {data.scopes.length > 0 && (
                <div className="app-settings-section">
                  <div className="app-settings-section-title">资源授权</div>
                  {data.scopes.map((scope, index) => (
                    <Form.Item
                      key={scope.kind}
                      label={scope.label}
                      name={['resources', scope.kind]}
                      extra={
                        scope.disabled
                          ? '对应能力权限已关闭，请先在「能力」页开启'
                          : scope.disabledPermissions?.length
                            ? `部分能力已关闭：${scope.disabledPermissions.join('、')}`
                            : undefined
                      }
                      style={
                        index === data.scopes.length - 1
                          ? { marginBottom: 0 }
                          : undefined
                      }
                      rules={[
                        {
                          validator: (_, selection?: ResourceSelection) =>
                            !scope.disabled &&
                            selection?.mode === 'selected' &&
                            !selection.ids.length
                              ? Promise.reject(
                                  new Error(`请选择${scope.label}`),
                                )
                              : Promise.resolve(),
                        },
                      ]}
                    >
                      <ResourceScopeInput scope={scope} />
                    </Form.Item>
                  ))}
                </div>
              )}
              {(!!data.credentialSlots?.length ||
                data.notifications ||
                !!data.events?.length) && (
                <div className="app-settings-section">
                  <div className="app-settings-section-title">
                    凭据、通知与事件
                  </div>
                  {data.credentialSlots?.map((slot) => (
                    <Form.Item
                      key={slot.id}
                      label={slot.title}
                      name={['credentials', slot.id]}
                      extra={
                        credentialsDisabled
                          ? '「读取外部凭据」（credentials.read）权限已关闭，请先在「能力」页开启'
                          : undefined
                      }
                    >
                      <Select
                        allowClear
                        disabled={credentialsDisabled}
                        style={{ maxWidth: 420 }}
                        loading={credentials.isFetching}
                        placeholder="选择面板设置中的外部凭据"
                        options={credentials.data?.map((item) => ({
                          value: item.id,
                          label: item.name,
                        }))}
                      />
                    </Form.Item>
                  ))}
                  {data.notifications && (
                    <Form.Item
                      label="通知渠道"
                      name="channelId"
                      extra={
                        notificationsDisabled
                          ? '「发送通知」（notifications.send）权限已关闭，请先在「能力」页开启'
                          : undefined
                      }
                    >
                      <Select
                        allowClear
                        disabled={notificationsDisabled}
                        style={{ maxWidth: 420 }}
                        placeholder="选择已有的通知渠道"
                        options={data.channels?.map((channel) => ({
                          value: channel.id,
                          label: channel.name,
                        }))}
                      />
                    </Form.Item>
                  )}
                  {!!data.events?.length && (
                    <Form.Item
                      label="事件订阅"
                      name="events"
                      extra={
                        eventsDisabled
                          ? '「订阅面板事件」（events.subscribe）权限已关闭，请先在「能力」页开启'
                          : undefined
                      }
                      style={{ marginBottom: 0 }}
                    >
                      <Checkbox.Group
                        style={{
                          display: 'flex',
                          flexWrap: 'wrap',
                          gap: '8px 16px',
                          paddingTop: 5,
                        }}
                        options={data.events.map((name) => {
                          const prereq = eventPrerequisites[name];
                          const prereqDisabled =
                            !!prereq && disabledSet.has(prereq);
                          const title = eventTitles[name] ?? name;
                          return {
                            value: name,
                            disabled: eventsDisabled || prereqDisabled,
                            label: prereqDisabled
                              ? `${title}（需开启 ${prereq}）`
                              : title,
                          };
                        })}
                      />
                    </Form.Item>
                  )}
                </div>
              )}
              {!!data.jobs?.length && (
                <div className="app-settings-section">
                  <div className="app-settings-section-title">定时任务</div>
                  {data.jobs.map((job, index) => (
                    <Form.Item
                      key={job.id}
                      label={job.title}
                      style={
                        index === data.jobs!.length - 1
                          ? { marginBottom: 0 }
                          : undefined
                      }
                      extra={
                        <>
                          {jobsDisabled
                            ? '「执行插件任务」（jobs.run）权限已关闭，请先在「能力」页开启。'
                            : '五段 Cron，使用面板时区。留空可停止调度。'}
                          {job.webhook && (
                            <div>
                              Webhook{' '}
                              <code>{`/plugin-webhooks/${plugin.id}/${job.id}`}</code>
                              {webhookDisabled && (
                                <Tag style={{ marginInlineStart: 6 }}>
                                  已关闭
                                </Tag>
                              )}
                            </div>
                          )}
                        </>
                      }
                    >
                      <Flex
                        align="center"
                        gap={SPACING.sm}
                        style={{ maxWidth: 420 }}
                      >
                        <Form.Item name={['schedules', job.id]} noStyle>
                          <Input
                            disabled={jobsDisabled}
                            style={{ flex: 1 }}
                            placeholder="0 8 * * *"
                            aria-label={`${job.title}执行计划`}
                          />
                        </Form.Item>
                        <Button
                          disabled={
                            jobsDisabled || !data.running || !!runningJob
                          }
                          loading={runningJob === job.id}
                          onClick={() => {
                            if (job.params?.length) setParameterJob(job);
                            else void run(job.id);
                          }}
                        >
                          立即运行
                        </Button>
                      </Flex>
                    </Form.Item>
                  ))}
                </div>
              )}
              <Flex justify="flex-start">
                <Button type="primary" htmlType="submit" loading={busy}>
                  保存授权
                </Button>
              </Flex>
            </div>
          </Form>
          {(!!data.jobs?.length || data.tasks.length > 0) && (
            <div className="app-settings-section">
              <div className="app-settings-section-title">任务执行记录</div>
              <Table
                rowKey="taskId"
                size="small"
                columns={columns}
                dataSource={data.tasks}
                pagination={{ pageSize: 5, hideOnSinglePage: true }}
                scroll={{ x: true }}
                locale={{ emptyText: '暂无插件任务记录' }}
              />
            </div>
          )}
          {!!data.events?.length && (
            <div className="app-settings-section">
              <div className="app-settings-section-title">事件投递记录</div>
              <Table
                rowKey="id"
                size="small"
                columns={deliveryColumns}
                dataSource={data.deliveries}
                pagination={{ pageSize: 5, hideOnSinglePage: true }}
                scroll={{ x: true }}
                locale={{ emptyText: '暂无事件投递记录' }}
              />
            </div>
          )}
        </div>
      )}
      {parameterJob && (
        <Modal
          open
          title={parameterJob.title}
          footer={null}
          onCancel={() => setParameterJob(undefined)}
        >
          <Form<Record<string, string>>
            layout="horizontal"
            labelCol={{ span: 8 }}
            initialValues={Object.fromEntries(
              (parameterJob.params ?? []).map((field) => [
                field.key,
                field.default,
              ]),
            )}
            onFinish={(values) => void run(parameterJob.id, values)}
          >
            {parameterJob.params?.map((field) => (
              <Form.Item
                key={field.key}
                name={field.key}
                label={field.label || field.key}
                extra={field.help}
                rules={[{ required: field.required, max: 512 }]}
              >
                <PluginEnvInput field={field} />
              </Form.Item>
            ))}
            <Button type="primary" htmlType="submit" loading={!!runningJob}>
              运行
            </Button>
          </Form>
        </Modal>
      )}
      <TaskDrawer
        taskId={taskId}
        open={!!taskId}
        title="插件任务"
        onClose={() => setTaskId(null)}
        onSettled={() => void query.refetch()}
      />
    </>
  );
}

function ResourceScopeInput({
  scope,
  value = { mode: 'off', ids: [] },
  onChange,
}: {
  scope: Scope;
  value?: ResourceSelection;
  onChange?: (value: ResourceSelection) => void;
}) {
  const [open, setOpen] = useState(false);
  const [draftIds, setDraftIds] = useState<string[]>([]);
  const [keyword, setKeyword] = useState('');

  const options = (scope.options ?? []).map((row) => ({
    id: row.id,
    label:
      scope.kind === 'databases' && row.kind
        ? `${row.name}（${row.kind}）`
        : row.name,
  }));

  const openPicker = () => {
    setDraftIds(value.ids);
    setKeyword('');
    setOpen(true);
  };

  const normalizedKeyword = keyword.trim().toLowerCase();
  const filteredOptions = normalizedKeyword
    ? options.filter((opt) =>
        opt.label.toLowerCase().includes(normalizedKeyword),
      )
    : options;
  const draftSet = new Set(draftIds);

  const toggleItem = (id: string) => {
    setDraftIds((current) =>
      current.includes(id)
        ? current.filter((item) => item !== id)
        : [...current, id],
    );
  };

  return (
    <>
      <Flex align="center" gap={SPACING.sm} wrap>
        <Radio.Group
          disabled={scope.disabled}
          optionType="button"
          aria-label={`${scope.label}授权范围`}
          value={value.mode}
          onChange={(event) => {
            if (scope.disabled) return;
            const nextMode = event.target.value as ResourceSelection['mode'];
            if (nextMode === 'selected') {
              if (!open) openPicker();
              return;
            }
            onChange?.({
              ...value,
              mode: nextMode,
            });
          }}
        >
          <Radio.Button value="off">未授权</Radio.Button>
          <Radio.Button value="all">全部</Radio.Button>
          <Radio.Button
            value="selected"
            onClick={() => {
              if (!scope.disabled && value.mode === 'selected') openPicker();
            }}
          >
            指定
          </Radio.Button>
        </Radio.Group>
        {value.mode === 'all' && (
          <Typography.Text type="secondary">
            包含当前及以后新增的{scope.label}
          </Typography.Text>
        )}
        {value.mode === 'selected' && (
          <Tag
            color={value.ids.length ? 'processing' : 'warning'}
            style={{
              cursor: scope.disabled ? 'not-allowed' : 'pointer',
              opacity: scope.disabled ? 0.6 : 1,
              marginInlineEnd: 0,
              userSelect: 'none',
            }}
            onClick={() => {
              if (!scope.disabled) openPicker();
            }}
          >
            {value.ids.length
              ? scope.disabled
                ? `已选 ${value.ids.length} 项`
                : `已选 ${value.ids.length} 项 · 点击修改`
              : `点击勾选${scope.label}`}
          </Tag>
        )}
      </Flex>
      <Modal
        open={open}
        title={`选择授权的${scope.label}`}
        width={520}
        okText="确定"
        cancelText="取消"
        destroyOnHidden
        onCancel={() => setOpen(false)}
        onOk={() => {
          onChange?.({
            mode: draftIds.length ? 'selected' : 'off',
            ids: draftIds,
          });
          setOpen(false);
        }}
      >
        <Flex vertical gap={SPACING.sm}>
          {scope.unavailable && (
            <Alert
              type="warning"
              showIcon
              message={`部分${scope.label}暂时无法读取，选择「全部」仍可授权大类。`}
            />
          )}
          <Input
            allowClear
            placeholder={`搜索${scope.label}...`}
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
          />
          <Flex justify="space-between" align="center">
            <Typography.Text type="secondary">
              已选 {draftIds.length} / {options.length} 项
            </Typography.Text>
            <Flex gap={SPACING.xs}>
              <Button
                type="link"
                size="small"
                disabled={!filteredOptions.length}
                onClick={() =>
                  setDraftIds((current) => [
                    ...new Set([
                      ...current,
                      ...filteredOptions.map((item) => item.id),
                    ]),
                  ])
                }
              >
                全选
              </Button>
              <Button
                type="link"
                size="small"
                disabled={!draftIds.length}
                onClick={() => setDraftIds([])}
              >
                清空
              </Button>
            </Flex>
          </Flex>
          <div
            style={{
              maxHeight: 'min(52dvh, 380px)',
              overflowY: 'auto',
              border: '1px solid var(--ant-color-border-secondary)',
              borderRadius: 8,
            }}
          >
            {filteredOptions.length === 0 ? (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={`暂无可选${scope.label}`}
                style={{ marginBlock: SPACING.lg }}
              />
            ) : (
              filteredOptions.map((item, idx) => {
                const checked = draftSet.has(item.id);
                return (
                  <Flex
                    key={item.id}
                    align="center"
                    gap={SPACING.sm}
                    onClick={() => toggleItem(item.id)}
                    style={{
                      padding: '10px 12px',
                      cursor: 'pointer',
                      borderBottom:
                        idx === filteredOptions.length - 1
                          ? 'none'
                          : '1px solid var(--ant-color-border-secondary)',
                    }}
                  >
                    <Checkbox
                      checked={checked}
                      onClick={(event) => event.stopPropagation()}
                      onChange={() => toggleItem(item.id)}
                    />
                    <Typography.Text ellipsis>{item.label}</Typography.Text>
                  </Flex>
                );
              })
            )}
          </div>
        </Flex>
      </Modal>
    </>
  );
}
