import { useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Alert,
  App,
  Badge,
  Button,
  Card,
  Empty,
  Form,
  Input,
  Modal,
  Select,
  Skeleton,
  Space,
  Switch,
  Tabs,
  Typography,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FolderOpen, Plus, RotateCw, Search } from 'lucide-react';
import ContentSizedTable from '../../components/ContentSizedTable';
import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import FilePicker from '../file/FilePicker';
import { formatTime } from '../../utils/format';
import {
  deleteHardeningRule,
  hardening,
  hardeningRuleAction,
  saveHardeningRule,
  scanHardening,
  setHardeningEnabled,
  type HardeningCategory,
  type HardeningEvent,
  type HardeningMode,
  type HardeningRule,
  type HardeningRuleInput,
  type HardeningState,
} from '../../api/hardening';
import './hardening.css';

const categories: Record<HardeningCategory, string> = {
  service: '服务与启动项',
  environment: '环境与登录配置',
  account: '用户与权限',
  critical: '关键文件与目录',
  schedule: '计划任务',
};
const modes: Record<HardeningMode, string> = {
  monitor: '监测',
  immutable: '禁止修改',
  append: '仅允许追加',
};
const states: Record<
  HardeningRule['status'],
  { label: string; status: 'default' | 'success' | 'warning' | 'error' }
> = {
  off: { label: '总开关已关闭', status: 'default' },
  disabled: { label: '规则已停用', status: 'default' },
  monitoring: { label: '监测中', status: 'success' },
  protected: { label: '已保护', status: 'success' },
  changed: { label: '发现变化', status: 'warning' },
  missing: { label: '路径不存在', status: 'warning' },
  unsupported: { label: '不支持保护', status: 'warning' },
  error: { label: '执行失败', status: 'error' },
  paused: { label: '临时暂停', status: 'default' },
};
const actionNames: Record<string, string> = {
  enable: '开启加固',
  disable: '关闭加固',
  create: '添加规则',
  save: '保存规则',
  check: '检查发现异常',
  update: '修改规则',
  delete: '移除规则',
  baseline: '更新基线',
  pause: '暂停保护',
  resume: '恢复保护',
  scan: '执行检查',
  changed: '发现变化',
  error: '执行失败',
  protect: '施加保护',
  restore: '解除保护',
};
const queryKey = ['security-hardening'];

export default function Hardening() {
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const query = useQuery({
    queryKey,
    queryFn: hardening,
    refetchInterval: 30000,
  });
  const mutation = useMutation({
    mutationFn: (operation: () => Promise<HardeningState>) => operation(),
    onSuccess: (data) => client.setQueryData(queryKey, data),
    onError: () => void query.refetch(),
  });
  const [tab, setTab] = useState('overview');
  const [category, setCategory] = useState<HardeningCategory | 'all'>('all');
  const [keyword, setKeyword] = useState('');
  const [page, setPage] = useState(1);
  const [picking, setPicking] = useState(false);
  const [editing, setEditing] = useState<HardeningRule | 'new'>();
  const [pausing, setPausing] = useState<HardeningRule>();
  const [minutes, setMinutes] = useState(15);
  const [form] = Form.useForm<HardeningRuleInput>();
  const mode = Form.useWatch('mode', form) ?? 'monitor';
  const state = query.data;
  const working = mutation.isPending;
  const rules = state?.rules ?? [];
  const issues = rules.filter((row) =>
    ['changed', 'missing', 'unsupported', 'error'].includes(row.status),
  );
  const displayed = rules.filter(
    (row) =>
      (category === 'all' || row.category === category) &&
      `${row.path} ${row.remark}`.toLowerCase().includes(keyword.toLowerCase()),
  );
  const perform = async (
    operation: () => Promise<HardeningState>,
    text: string,
  ) => {
    await mutation.mutateAsync(operation);
    message.success(text);
  };
  const openEditor = (row: HardeningRule | 'new') => {
    setEditing(row);
    form.resetFields();
    form.setFieldsValue(
      row === 'new'
        ? {
            path: '',
            category: category === 'all' ? 'critical' : category,
            mode: 'monitor',
            enabled: true,
            recursive: false,
            permission: '',
            remark: '',
            exclusions: '',
          }
        : row,
    );
  };
  const save = async () => {
    const fields = await form.validateFields();
    const input = {
      ...fields,
      path: fields.path.trim(),
      permission: fields.permission?.trim() ?? '',
      remark: fields.remark?.trim() ?? '',
      exclusions: fields.exclusions?.trim() ?? '',
      recursive: fields.mode === 'monitor' && fields.recursive,
    };
    const submit = async () => {
      await perform(
        () =>
          saveHardeningRule(input, editing === 'new' ? undefined : editing?.id),
        state?.enabled ? '规则已保存' : '规则已保存，总开关开启后生效',
      );
      if (editing === 'new') {
        setPage(1);
        setKeyword('');
        setCategory('all');
      }
      setEditing(undefined);
    };
    if (input.enabled && input.mode !== 'monitor') {
      modal.confirm({
        title: `对该对象设置“${modes[input.mode]}”？`,
        content: (
          <Space direction="vertical">
            <Typography.Text className="hardening-path">
              {input.path}
            </Typography.Text>
            <Typography.Text>
              此保护会影响文件或目录的编辑、删除及软件升级。维护前需要暂停或解除保护；总开关关闭时仅保存规则。
            </Typography.Text>
          </Space>
        ),
        onOk: submit,
      });
    } else await submit();
  };
  const updateRule = (
    row: HardeningRule,
    changes: Partial<Pick<HardeningRuleInput, 'mode' | 'enabled'>>,
  ) => {
    const input: HardeningRuleInput = {
      path: row.path,
      category: row.category,
      mode: row.mode,
      enabled: row.enabled,
      recursive: row.recursive,
      permission: row.permission,
      remark: row.remark,
      exclusions: row.exclusions ?? '',
      ...changes,
    };
    if (input.mode !== 'monitor') input.recursive = false;
    const submit = () =>
      perform(
        () => saveHardeningRule(input, row.id),
        changes.mode
          ? '保护模式已更新'
          : input.enabled
            ? '规则已启用'
            : '规则已停用',
      );
    if (input.enabled && input.mode !== 'monitor' && state?.enabled)
      modal.confirm({
        title: `启用“${modes[input.mode]}”保护？`,
        content: '将限制该文件或目录的写入操作，维护前需要暂停或解除保护。',
        onOk: submit,
      });
    else void submit();
  };
  const columns = [
    {
      title: '保护对象',
      key: 'path',
      render: (_: unknown, row: HardeningRule) => (
        <>
          <Typography.Text className="hardening-path" copyable>
            {row.path}
          </Typography.Text>
          <Typography.Text type="secondary" className="hardening-table-detail">
            {categories[row.category]}
            {row.recursive ? ' · 包含子目录' : ''}
            {row.exclusions ? ' · 已配置排除规则' : ''}
            {row.remark ? ` · ${row.remark}` : ''}
          </Typography.Text>
        </>
      ),
    },
    {
      title: '模式',
      dataIndex: 'mode',
      render: (_: HardeningMode, row: HardeningRule) => (
        <Select
          aria-label={`${row.path} 保护模式`}
          value={row.mode}
          disabled={working}
          className="hardening-mode-select"
          options={Object.entries(modes).map(([value, label]) => ({
            value,
            label,
            disabled:
              (value !== 'monitor' && !state?.supported) ||
              (value === 'append' && !!row.directory),
          }))}
          onChange={(mode: HardeningMode) => updateRule(row, { mode })}
        />
      ),
    },
    {
      title: '权限',
      key: 'permission',
      render: (_: unknown, row: HardeningRule) => (
        <>
          <span>{row.current?.permission ?? '—'}</span>
          {row.permission && (
            <Typography.Text
              type="secondary"
              className="hardening-table-detail"
            >
              期望 {row.permission}
            </Typography.Text>
          )}
        </>
      ),
    },
    {
      title: '实际状态',
      key: 'status',
      render: (_: unknown, row: HardeningRule) => (
        <>
          <Badge
            status={states[row.status].status}
            text={states[row.status].label}
          />
          {row.message && (
            <Typography.Text
              type="secondary"
              className="hardening-table-detail"
            >
              {row.message}
            </Typography.Text>
          )}
          {row.pausedUntil && (
            <Typography.Text
              type="secondary"
              className="hardening-table-detail"
            >
              {formatTime(row.pausedUntil)} 恢复
            </Typography.Text>
          )}
        </>
      ),
    },
    {
      title: '规则开关',
      key: 'enabled',
      render: (_: unknown, row: HardeningRule) => (
        <Switch
          size="small"
          aria-label={`${row.path} 规则开关`}
          checked={row.enabled}
          disabled={working}
          onChange={(enabled) => updateRule(row, { enabled })}
        />
      ),
    },
    {
      title: '操作',
      key: 'actions',
      render: (_: unknown, row: HardeningRule) => (
        <Space size={12} wrap>
          <Button
            type="link"
            size="small"
            disabled={working}
            onClick={() => openEditor(row)}
          >
            编辑
          </Button>
          <Button
            type="link"
            size="small"
            disabled={working || !row.current}
            onClick={() =>
              modal.confirm({
                title: '将当前状态设为可信基线？',
                content:
                  '确认这是合法变更后再更新。后续检查将与当前文件内容、权限及属主进行比较。',
                onOk: () =>
                  perform(
                    () => hardeningRuleAction(row.id, 'baseline'),
                    '可信基线已更新',
                  ),
              })
            }
          >
            更新基线
          </Button>
          {row.enabled &&
            state?.enabled &&
            (row.pausedUntil ? (
              <Button
                type="link"
                size="small"
                disabled={working}
                onClick={() =>
                  void perform(
                    () => hardeningRuleAction(row.id, 'resume'),
                    '规则已恢复',
                  )
                }
              >
                恢复
              </Button>
            ) : (
              <Button
                type="link"
                size="small"
                disabled={working}
                onClick={() => {
                  setMinutes(15);
                  setPausing(row);
                }}
              >
                临时暂停
              </Button>
            ))}
          <Button
            type="link"
            size="small"
            danger
            disabled={working}
            onClick={() =>
              modal.confirm({
                title: '移除这条保护规则？',
                content:
                  '仅移除规则并撤销本功能增加的文件保护，不删除文件，也不移除文件原有属性。',
                okButtonProps: { danger: true },
                onOk: () =>
                  perform(() => deleteHardeningRule(row.id), '规则已移除'),
              })
            }
          >
            移除
          </Button>
        </Space>
      ),
    },
  ];
  const eventsColumns = [
    {
      title: '时间',
      dataIndex: 'time',
      render: (value: string) => formatTime(value),
    },
    {
      title: '事件',
      dataIndex: 'action',
      render: (value: string) => actionNames[value] ?? value,
    },
    {
      title: '对象',
      dataIndex: 'path',
      render: (value: string) => (
        <span className="hardening-path">{value || '系统加固'}</span>
      ),
    },
    { title: '详情', dataIndex: 'message' },
  ];
  return (
    <Card className="hardening-page">
      {query.error && (
        <Alert
          type="error"
          message={query.error.message}
          action={<Button onClick={() => void query.refetch()}>重试</Button>}
        />
      )}
      <div className="hardening-switch-row">
        <div className="hardening-switch-copy">
          <div className="hardening-switch-title">
            <Typography.Text strong>系统加固</Typography.Text>
            <Badge
              status={state?.enabled ? 'success' : 'default'}
              text={state?.enabled ? '已开启' : '已关闭'}
            />
          </div>
          <Typography.Text type="secondary">
            {state?.enabled
              ? `按已启用规则保护文件，每 ${state.intervalSeconds} 秒检查一次。`
              : '总开关关闭时不施加文件保护、不执行定时监测，规则设置保留。'}
          </Typography.Text>
        </div>
        <Switch
          aria-label="系统加固总开关"
          checked={state?.enabled ?? false}
          disabled={!state || working}
          loading={working}
          onChange={(enabled) =>
            modal.confirm({
              title: enabled ? '开启系统加固？' : '关闭系统加固？',
              content: enabled
                ? '将启用已开启的规则及定时检查。禁止修改和仅追加规则会限制对应文件的写入，可能影响编辑和软件升级。'
                : '将停止定时监测，并解除本功能增加的文件保护。规则和可信基线保留，文件原有属性不变。',
              onOk: () =>
                perform(
                  () => setHardeningEnabled(enabled),
                  enabled ? '系统加固已开启' : '系统加固已关闭',
                ),
            })
          }
        />
      </div>
      {state?.notice && (
        <Alert
          type="info"
          message={state.notice}
          style={{ marginBottom: 16 }}
        />
      )}
      {query.isPending ? (
        <Skeleton active />
      ) : (
        state && (
          <Tabs
            activeKey={tab}
            onChange={setTab}
            items={[
              {
                key: 'overview',
                label: '概览',
                children: (
                  <>
                    <div className="hardening-summary">
                      <span>
                        <strong>{rules.length}</strong>条规则
                      </span>
                      <span>
                        <strong>
                          {
                            rules.filter((row) => row.status === 'protected')
                              .length
                          }
                        </strong>
                        个对象受保护
                      </span>
                      <span>
                        <strong>
                          {
                            rules.filter((row) => row.status === 'monitoring')
                              .length
                          }
                        </strong>
                        项监测中
                      </span>
                      <span>
                        <strong>{issues.length}</strong>项待处理
                      </span>
                      <Typography.Text type="secondary">
                        最近检查：
                        {state.checkedAt
                          ? formatTime(state.checkedAt)
                          : '尚未检查'}
                      </Typography.Text>
                    </div>
                    <div className="hardening-section-heading">
                      <Typography.Text strong>待处理项目</Typography.Text>
                      <Button
                        icon={<RotateCw size={14} />}
                        loading={working}
                        onClick={() =>
                          void perform(scanHardening, '检查已完成')
                        }
                      >
                        立即检查
                      </Button>
                    </div>
                    <Typography.Paragraph type="secondary">
                      总开关关闭时，手动检查不会开启总开关或施加文件保护。
                    </Typography.Paragraph>
                    {issues.length ? (
                      <ContentSizedTable<HardeningRule>
                        rowKey="id"
                        fillColumn="path"
                        dataSource={issues}
                        columns={columns.filter((column) =>
                          ['path', 'status'].includes(column.key ?? ''),
                        )}
                        pagination={{ pageSize: 5, hideOnSinglePage: true }}
                      />
                    ) : (
                      <Empty
                        image={Empty.PRESENTED_IMAGE_SIMPLE}
                        description={
                          state.checkedAt
                            ? '最近一次检查没有待处理项目'
                            : '尚未检查，点击“立即检查”查看当前状态'
                        }
                      />
                    )}
                    <div className="hardening-overview-notes">
                      <Typography.Paragraph type="secondary">
                        监测用于发现内容、权限和属主变化；普通文件使用文件属性保护，目录使用内核实时保护，并按排除规则放行。
                      </Typography.Paragraph>
                      <Typography.Paragraph type="secondary">
                        事件记录显示本功能检查与操作结果。需要追踪系统命令时，可前往{' '}
                        <Link to="/tools/audit">命令审计</Link> 配置 auditd。
                      </Typography.Paragraph>
                      <Typography.Paragraph>
                        本机恢复命令（面板停止后执行，使用实际安装目录）：
                      </Typography.Paragraph>
                      <Typography.Paragraph code copyable>
                        {state.recoveryCommand ??
                          'ezpanel hardening-recover --install-dir /opt/ezpanel'}
                      </Typography.Paragraph>
                    </div>
                  </>
                ),
              },
              {
                key: 'rules',
                label: '保护规则',
                children: (
                  <>
                    <div className="hardening-toolbar">
                      <Button
                        type="primary"
                        icon={<Plus size={14} />}
                        onClick={() => openEditor('new')}
                        disabled={working}
                      >
                        添加规则
                      </Button>
                      <Select
                        aria-label="规则分类"
                        value={category}
                        onChange={(value) => {
                          setCategory(value);
                          setPage(1);
                        }}
                        options={[
                          { value: 'all', label: '全部分类' },
                          ...Object.entries(categories).map(
                            ([value, label]) => ({ value, label }),
                          ),
                        ]}
                      />
                      <Input
                        aria-label="搜索保护规则"
                        placeholder="搜索路径或备注"
                        prefix={<Search size={14} />}
                        value={keyword}
                        allowClear
                        onChange={(event) => {
                          setKeyword(event.target.value);
                          setPage(1);
                        }}
                      />
                    </div>
                    {!state.enabled && (
                      <Typography.Paragraph type="secondary">
                        总开关已关闭，单条规则开关只保存启用意向；实际状态以检查结果为准。
                      </Typography.Paragraph>
                    )}
                    <ContentSizedTable<HardeningRule>
                      rowKey="id"
                      className="security-data-table"
                      fillColumn="path"
                      dataSource={displayed}
                      columns={columns}
                      pagination={{
                        current: page,
                        onChange: setPage,
                        pageSize: 15,
                        hideOnSinglePage: true,
                      }}
                      locale={{ emptyText: '暂无匹配的保护规则' }}
                    />
                  </>
                ),
              },
              {
                key: 'events',
                label: '事件记录',
                children: (
                  <>
                    <div className="hardening-section-heading">
                      <Typography.Text type="secondary">
                        保留最近 500 条检查与操作事件。
                      </Typography.Text>
                      <Button
                        icon={<RotateCw size={14} />}
                        loading={query.isFetching}
                        onClick={() => void query.refetch()}
                      >
                        刷新
                      </Button>
                    </div>
                    <ContentSizedTable<HardeningEvent>
                      rowKey="id"
                      fillColumn="message"
                      dataSource={state.events}
                      columns={eventsColumns}
                      pagination={{ pageSize: 15, hideOnSinglePage: true }}
                      locale={{ emptyText: '暂无加固事件' }}
                    />
                  </>
                ),
              },
            ]}
          />
        )
      )}
      <Modal
        open={!!editing}
        title={editing === 'new' ? '添加保护规则' : '编辑保护规则'}
        width={640}
        onCancel={() => setEditing(undefined)}
        onOk={() => void save()}
        confirmLoading={working}
        destroyOnHidden
      >
        <ApplicationSettingsForm
          form={form}
          className="hardening-rule-form"
          preserve={false}
        >
          <Form.Item
            name="path"
            label="保护路径"
            rules={[
              { required: true, whitespace: true, message: '请输入完整路径' },
              { pattern: /^\//, message: '请输入以 / 开头的绝对路径' },
            ]}
          >
            <Input
              readOnly
              placeholder="选择文件或目录"
              onClick={() => setPicking(true)}
              suffix={
                <Button
                  type="text"
                  size="small"
                  icon={<FolderOpen size={16} />}
                  aria-label="选择保护文件或目录"
                  onClick={() => setPicking(true)}
                />
              }
            />
          </Form.Item>
          <Form.Item name="category" label="分类" rules={[{ required: true }]}>
            <Select
              options={Object.entries(categories).map(([value, label]) => ({
                value,
                label,
              }))}
            />
          </Form.Item>
          <Form.Item name="mode" label="保护模式" rules={[{ required: true }]}>
            <Select
              options={Object.entries(modes).map(([value, label]) => ({
                value,
                label,
                disabled: value !== 'monitor' && !state?.supported,
              }))}
            />
          </Form.Item>
          {mode === 'monitor' ? (
            <Form.Item
              name="recursive"
              label="包含子目录"
              valuePropName="checked"
              extra="目录默认检查直接子项的名称及元数据；开启后包含子目录。检查超出支持范围时会显示错误。"
            >
              <Switch />
            </Form.Item>
          ) : (
            <Alert
              type="warning"
              message={
                mode === 'immutable'
                  ? '支持文件及目录。目录内未排除的路径将实时禁止新建、修改、删除和重命名；维护前请暂停保护。'
                  : '仅支持单个普通文件。启用后只能追加内容，会影响覆盖写入、截断及日志轮转。'
              }
            />
          )}
          {!state?.supported && (
            <Typography.Paragraph type="secondary">
              当前系统不支持文件属性保护，仍可使用监测模式。
            </Typography.Paragraph>
          )}
          <Form.Item
            name="exclusions"
            label="排除规则"
            extra="目录规则可用，一行一个。*.log 匹配任意层级文件名；uploads/** 匹配目录及其内容；支持 *、?、[]、**，同样适用于未来路径。"
          >
            <Input.TextArea
              rows={4}
              placeholder={'uploads/**\ncache/**\n*.log'}
              spellCheck={false}
            />
          </Form.Item>
          {mode === 'immutable' && state?.directoryNotice && (
            <Typography.Paragraph type="secondary">
              {state.directoryNotice}
            </Typography.Paragraph>
          )}
          <Form.Item
            name="permission"
            label="期望权限"
            extra="可选，仅检查，不修改文件权限。例如 644、600、755。"
            rules={[
              {
                pattern: /^[0-7]{3,4}$/,
                message: '请输入 3 或 4 位八进制权限',
              },
            ]}
          >
            <Input placeholder="留空使用可信基线中的权限" maxLength={4} />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input maxLength={200} />
          </Form.Item>
          <Form.Item
            name="enabled"
            label="启用这条规则"
            valuePropName="checked"
            extra={
              state?.enabled
                ? '保存后按所选模式生效。'
                : '总开关当前关闭，开启总开关后生效。'
            }
          >
            <Switch />
          </Form.Item>
        </ApplicationSettingsForm>
      </Modal>
      {picking && (
        <FilePicker
          title="选择保护文件或目录"
          allowDirectories
          initialPath={
            form.getFieldValue('path')?.replace(/\/[^/]+\/?$/, '') || '/'
          }
          onSelect={(path) => {
            form.setFieldValue('path', path);
            setPicking(false);
          }}
          onClose={() => setPicking(false)}
        />
      )}
      <Modal
        open={!!pausing}
        title="临时暂停规则"
        onCancel={() => setPausing(undefined)}
        confirmLoading={working}
        onOk={async () => {
          if (!pausing) return;
          await perform(
            () => hardeningRuleAction(pausing.id, 'pause', minutes),
            '规则已临时暂停',
          );
          setPausing(undefined);
        }}
        destroyOnHidden
      >
        <Typography.Paragraph className="hardening-path">
          {pausing?.path}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary">
          暂停期间解除本功能增加的文件保护并停止该规则的定时监测，到期后自动恢复。维护造成的合法变化需另行更新可信基线。
        </Typography.Paragraph>
        <Select
          aria-label="暂停时长"
          style={{ width: '100%' }}
          value={minutes}
          onChange={setMinutes}
          options={[5, 15, 30, 60].map((value) => ({
            value,
            label: `${value} 分钟`,
          }))}
        />
      </Modal>
    </Card>
  );
}
