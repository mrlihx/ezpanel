import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import LogText from '../../components/LogText';
import { lazy, Suspense, useEffect, useState } from 'react';
import {
  Alert,
  App,
  Badge,
  Button,
  Checkbox,
  Descriptions,
  Form,
  Input,
  Modal,
  Select,
  Skeleton,
  Space,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  Grid,
} from 'antd';
import {
  CheckOutlined,
  CloseOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import ContentSizedTable from '../../components/ContentSizedTable';
import { applicationLogs } from '../../api/software';
import {
  phpConfig,
  savePHPConfig,
  phpExtensions,
  changePHPExtension,
  phpLoad,
  phpInfo,
  phpCLI,
  setDefaultPHPCLI,
  type PHPConfigKind,
  type PHPExtensionOption,
  type PHPExtension,
} from '../../api/php';
import {
  appSettingsModalStyles,
  type ApplicationSettingsProps,
} from './ApplicationSettings';
import { MONO_FONT, SPACING, TERMINAL_COLORS } from '../../styles/tokens';
import { useMessageBox } from '../../stores/messageBox';
const CodeEditor = lazy(() => import('../../components/CodeEditor'));
type ContentProps = {
  id: string;
  busy: boolean;
  onDirty: (dirty: boolean) => void;
};

export default function PHPSettings(props: ApplicationSettingsProps) {
  const { app, onClose } = props;
  const { modal } = App.useApp();
  const screens = Grid.useBreakpoint();
  const [tab, setTab] = useState('service');
  const [dirty, setDirty] = useState(false);
  const contentProps = { id: app.id, busy: props.busy, onDirty: setDirty };
  const discard = (next: () => void) => {
    if (dirty)
      modal.confirm({
        title: '放弃尚未保存的修改？',
        onOk: () => {
          setDirty(false);
          next();
        },
      });
    else next();
  };
  return (
    <Modal
      open
      centered
      title={`${app.name}${app.version ? ` · ${app.version}` : ''} 管理`}
      width={1120}
      styles={appSettingsModalStyles}
      footer={null}
      maskClosable={false}
      onCancel={() => discard(onClose)}
      destroyOnHidden
    >
      <Tabs
        className="app-settings-tabs"
        tabPosition={screens.md ? 'left' : 'top'}
        activeKey={tab}
        onChange={(key) => discard(() => setTab(key))}
        destroyOnHidden
        items={[
          {
            key: 'service',
            label: '服务',
            children: <PHPService {...props} />,
          },
          {
            key: 'extensions',
            label: '安装扩展',
            children: <Extensions {...contentProps} />,
          },
          {
            key: 'limits',
            label: '限制',
            children: <SettingsForm {...contentProps} kind="ini" />,
          },
          {
            key: 'ini',
            label: '配置文件',
            children: <ConfigEditor {...contentProps} kind="ini" />,
          },
          {
            key: 'fpm',
            label: 'FPM配置文件',
            children: <ConfigEditor {...contentProps} kind="fpm" />,
          },
          {
            key: 'functions',
            label: '禁用函数',
            children: <DisabledFunctions {...contentProps} />,
          },
          {
            key: 'performance',
            label: '性能调整',
            children: <SettingsForm {...contentProps} kind="fpm" />,
          },
          {
            key: 'load',
            label: '负载状态',
            children: <LoadStatus id={app.id} busy={props.busy} />,
          },
          {
            key: 'logs',
            label: '运行日志',
            children: <PHPText id={app.id} kind="logs" />,
          },
          {
            key: 'info',
            label: 'phpinfo',
            children: <PHPText id={app.id} kind="info" />,
          },
        ]}
      />
    </Modal>
  );
}
function PHPService({
  app,
  busy,
  control,
  uninstall,
  onClose,
}: ApplicationSettingsProps) {
  const { message } = App.useApp();
  const navigate = useNavigate();
  const client = useQueryClient();
  const [savingCLI, setSavingCLI] = useState(false);
  const usedSites = app.usedBySites ?? [];
  const inUse = usedSites.length > 0;
  const cli = useQuery({
    queryKey: ['php-cli'],
    queryFn: phpCLI,
  });
  const currentCLI = cli.data?.installed.find((item) => item.id === app.id);
  const isDefaultCLI = cli.data?.defaultId === app.id;
  async function updateDefaultCLI(targetId: string) {
    if (savingCLI || !targetId) return;
    setSavingCLI(true);
    try {
      const next = await setDefaultPHPCLI(targetId);
      client.setQueryData(['php-cli'], next);
      const picked = next.installed.find((item) => item.id === targetId);
      void message.success(
        `已将系统默认 PHP CLI 切换为 ${picked ? `PHP-${picked.branch}` : targetId}`,
      );
    } finally {
      setSavingCLI(false);
    }
  }
  return (
    <div className="app-settings-stack">
      <div className="app-settings-section">
        <div className="app-settings-section-title">基本信息</div>
        <Descriptions
          size="small"
          column={{ xs: 1, sm: 2 }}
          items={[
            {
              key: 'status',
              label: '服务状态',
              children: (
                <Badge
                  status={app.running ? 'success' : 'default'}
                  text={app.running ? '运行中' : '已停止'}
                />
              ),
            },
            {
              key: 'version',
              label: '当前版本',
              children: app.version || '未识别',
            },
            ...(app.installDir
              ? [
                  {
                    key: 'directory',
                    label: '安装目录',
                    span: 2,
                    children: (
                      <Typography.Text code copyable>
                        {app.installDir}
                      </Typography.Text>
                    ),
                  },
                ]
              : []),
          ]}
        />
      </div>
      <div className="app-settings-section">
        <div className="app-settings-section-title">服务控制</div>
        <Space wrap>
          {!app.running && (
            <Button
              type="primary"
              disabled={busy}
              onClick={() => control('start')}
            >
              启动
            </Button>
          )}
          {app.running && (
            <Button disabled={busy} onClick={() => control('stop')}>
              停止
            </Button>
          )}
          <Button disabled={busy} onClick={() => control('restart')}>
            重启
          </Button>
          <Button
            disabled={busy || !app.running}
            onClick={() => control('reload')}
          >
            重载配置
          </Button>
        </Space>
      </div>
      <div className="app-settings-section">
        <div className="app-settings-section-title">命令行（CLI）</div>
        <Descriptions
          size="small"
          column={{ xs: 1, sm: 2 }}
          items={[
            {
              key: 'cmd',
              label: '本版本命令',
              children: currentCLI ? (
                <Space size={8} wrap>
                  <Typography.Text code copyable>
                    {currentCLI.command}
                  </Typography.Text>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {currentCLI.binPath}
                  </Typography.Text>
                </Space>
              ) : (
                <Typography.Text type="secondary">—</Typography.Text>
              ),
            },
            {
              key: 'default',
              label: '系统默认 php',
              children: (
                <Space size={8} wrap>
                  {cli.data && cli.data.installed.length > 1 ? (
                    <Select
                      size="small"
                      style={{ width: 150 }}
                      value={cli.data.defaultId || undefined}
                      loading={savingCLI}
                      disabled={busy || savingCLI}
                      options={cli.data.installed.map((item) => ({
                        value: item.id,
                        label: `PHP-${item.branch} (${item.command})`,
                      }))}
                      onChange={(value) => void updateDefaultCLI(value)}
                    />
                  ) : isDefaultCLI ? (
                    <Tag color="success">当前版本（{app.name}）</Tag>
                  ) : (
                    <Typography.Text type="secondary">
                      {cli.data?.defaultId || '未设置'}
                    </Typography.Text>
                  )}
                  {!isDefaultCLI && currentCLI && (
                    <Button
                      size="small"
                      type="primary"
                      loading={savingCLI}
                      disabled={busy || savingCLI}
                      onClick={() => void updateDefaultCLI(app.id)}
                    >
                      设为系统默认 CLI
                    </Button>
                  )}
                  {isDefaultCLI && (
                    <Tag color="blue" style={{ marginInlineEnd: 0 }}>
                      默认 CLI
                    </Tag>
                  )}
                </Space>
              ),
            },
          ]}
        />
        <Typography.Text
          type="secondary"
          style={{ display: 'block', marginTop: 8, fontSize: 12 }}
        >
          计划任务或终端可直接运行{' '}
          <Typography.Text code>
            php &apos;/www/wwwroot/站点目录/script.php&apos;
          </Typography.Text>{' '}
          或{' '}
          <Typography.Text code>
            {currentCLI?.command || 'php83'} ...
          </Typography.Text>
          ，自动以 www-data 身份在对应 PHP 容器内执行。
        </Typography.Text>
      </div>
      <div className="app-settings-section app-settings-danger">
        {inUse && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message={`${app.name} 当前正被 ${usedSites.length} 个网站使用，无法卸载`}
            description={
              <Space direction="vertical" size={6} style={{ width: '100%' }}>
                <Space size={[4, 4]} wrap>
                  {usedSites.map((siteName) => (
                    <Tag key={siteName} color="warning">
                      {siteName}
                    </Tag>
                  ))}
                </Space>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  请先在网站管理中切换上述网站的 PHP 版本或改为纯静态后再卸载。
                </Typography.Text>
              </Space>
            }
            action={
              <Button
                size="small"
                onClick={() => {
                  onClose();
                  navigate('/websites');
                }}
              >
                前往网站管理
              </Button>
            }
          />
        )}
        <div className="app-settings-row">
          <div className="app-settings-row-info">
            <span className="app-settings-row-title">卸载应用</span>
            <Typography.Text type="secondary">
              卸载后将移除 {app.name} 运行环境与进程池配置。
            </Typography.Text>
          </div>
          <Tooltip
            title={
              inUse
                ? '请先切换关联网站的 PHP 版本或改为纯静态后再卸载'
                : undefined
            }
          >
            <Button
              danger
              disabled={busy || !app.uninstallable || inUse}
              onClick={uninstall}
            >
              卸载应用
            </Button>
          </Tooltip>
        </div>
      </div>
    </div>
  );
}
function ConfigEditor({
  id,
  kind,
  busy,
  onDirty,
}: ContentProps & { kind: PHPConfigKind }) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ['php-config', id, kind],
    queryFn: () => phpConfig(id, kind),
  });
  const [draft, setDraft] = useState<string>();
  const [saving, setSaving] = useState(false);
  const dirty = draft !== undefined && draft !== query.data?.content;
  useEffect(() => {
    onDirty(dirty);
    return () => onDirty(false);
  }, [dirty, onDirty]);
  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      {query.error && <Alert type="error" message={query.error.message} />}
      {query.isPending ? (
        <Skeleton active />
      ) : (
        query.data && (
          <>
            <div className="app-settings-toolbar">
              <Typography.Text code copyable>
                {query.data.path}
              </Typography.Text>
              <Space>
                <Button
                  disabled={saving}
                  onClick={() => {
                    const reload = () => {
                      setDraft(undefined);
                      void query.refetch();
                    };
                    if (dirty)
                      modal.confirm({
                        title: '丢弃修改并重新读取配置？',
                        onOk: reload,
                      });
                    else reload();
                  }}
                >
                  重新加载
                </Button>
                <Button
                  type="primary"
                  loading={saving}
                  disabled={busy || !dirty}
                  onClick={async () => {
                    setSaving(true);
                    try {
                      await savePHPConfig(id, kind, query.data!.revision, {
                        content: draft!,
                      });
                      setDraft(undefined);
                      await client.invalidateQueries({
                        queryKey: ['php-config', id],
                      });
                      message.success('配置已保存，重载PHP后生效');
                    } finally {
                      setSaving(false);
                    }
                  }}
                >
                  保存配置
                </Button>
              </Space>
            </div>
            <div className="app-settings-editor-box">
              <Suspense fallback={<Skeleton active />}>
                <CodeEditor
                  path={query.data.path}
                  value={draft ?? query.data.content}
                  onChange={setDraft}
                />
              </Suspense>
            </div>
            <Typography.Text type="secondary">
              保存前备份原文件并检查FPM配置。配置文件为php.ini，FPM配置文件管理进程池。
            </Typography.Text>
          </>
        )
      )}
    </Space>
  );
}
const limitFields = [
  ['upload_max_filesize', '上传文件上限', '单个文件大小，如50M'],
  ['post_max_size', 'POST大小上限', '应不小于上传文件上限，如64M'],
  ['max_execution_time', '执行超时', '秒，0不限制'],
  ['max_input_time', '输入超时', '秒，0不限制'],
  ['memory_limit', '内存上限', '如128M，-1不限制'],
] as const;
const performanceFields = [
  ['pm.max_children', 'max_children', '允许创建的最大子进程数'],
  ['pm.start_servers', 'start_servers', '动态模式的初始进程数'],
  ['pm.min_spare_servers', 'min_spare_servers', '动态模式的最小空闲进程数'],
  ['pm.max_spare_servers', 'max_spare_servers', '动态模式的最大空闲进程数'],
  ['pm.max_requests', 'max_requests', '单进程处理请求数，0不限制'],
  ['pm.process_idle_timeout', '空闲回收时间', '按需模式，如10s'],
] as const;
function SettingsForm({
  id,
  kind,
  busy,
  onDirty,
}: ContentProps & { kind: PHPConfigKind }) {
  const { message } = App.useApp();
  const client = useQueryClient();
  const [form] = Form.useForm<Record<string, string>>();
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const query = useQuery({
    queryKey: ['php-config', id, kind],
    queryFn: () => phpConfig(id, kind),
  });
  const mode = Form.useWatch('pm', form);
  useEffect(() => {
    onDirty(dirty);
    return () => onDirty(false);
  }, [dirty, onDirty]);
  const defaults =
    kind === 'ini'
      ? {
          upload_max_filesize: '2M',
          post_max_size: '8M',
          max_execution_time: '30',
          max_input_time: '60',
          memory_limit: '128M',
        }
      : {
          pm: 'ondemand',
          'pm.max_children': '10',
          'pm.start_servers': '5',
          'pm.min_spare_servers': '5',
          'pm.max_spare_servers': '10',
          'pm.max_requests': '500',
          'pm.process_idle_timeout': '10s',
        };
  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      {query.error && <Alert type="error" message={query.error.message} />}
      {query.isPending ? (
        <Skeleton active />
      ) : (
        query.data && (
          <ApplicationSettingsForm
            key={query.data.revision}
            form={form}

            initialValues={{ ...defaults, ...query.data.values }}
            onValuesChange={() => setDirty(true)}
            onFinish={async (values) => {
              setSaving(true);
              try {
                await savePHPConfig(id, kind, query.data!.revision, { values });
                setDirty(false);
                await client.invalidateQueries({
                  queryKey: ['php-config', id],
                });
                message.success('设置已保存，已发起 PHP-FPM 重载');
              } finally {
                setSaving(false);
              }
            }}
          >
            {kind === 'fpm' && (
              <>
                <Form.Item label="并发方案">
                  <Select
                    placeholder="按内存选择预设"
                    style={{ maxWidth: 350 }}
                    onChange={(value) => {
                      const n = Number(value);
                      form.setFieldsValue({
                        pm: 'dynamic',
                        'pm.max_children': String(n),
                        'pm.start_servers': String(Math.min(n, 5)),
                        'pm.min_spare_servers': String(Math.min(n, 5)),
                        'pm.max_spare_servers': String(Math.min(n, 20)),
                      });
                      setDirty(true);
                    }}
                    options={[
                      { value: 10, label: '512MB内存' },
                      { value: 20, label: '1GB内存' },
                      { value: 50, label: '2GB内存' },
                      { value: 100, label: '4GB内存' },
                      { value: 200, label: '8GB内存' },
                    ]}
                  />
                </Form.Item>
                <Form.Item label="连接信息">
                  <Typography.Text code>
                    {query.data.values.listen || '未配置'}
                  </Typography.Text>
                </Form.Item>
                <Form.Item
                  label="运行模式"
                  name="pm"
                  rules={[{ required: true }]}
                >
                  <Select
                    style={{ maxWidth: 350 }}
                    options={[
                      { value: 'ondemand', label: '按需模式' },
                      { value: 'dynamic', label: '动态模式' },
                      { value: 'static', label: '静态模式' },
                    ]}
                  />
                </Form.Item>
              </>
            )}
            {(kind === 'ini' ? limitFields : performanceFields).map(
              ([name, label, hint]) => {
                const isHidden =
                  kind === 'fpm' &&
                  (((name.includes('spare') || name === 'pm.start_servers') &&
                    mode !== 'dynamic') ||
                    (name === 'pm.process_idle_timeout' &&
                      mode !== 'ondemand'));
                return (
                  <Form.Item
                    key={name}
                    label={label}
                    name={name}
                    extra={hint}
                    hidden={isHidden}
                    rules={
                      isHidden
                        ? undefined
                        : [{ required: true, message: '请填写此项' }]
                    }
                  >
                    <Input style={{ maxWidth: 350 }} />
                  </Form.Item>
                );
              },
            )}
            <Form.Item className="application-settings-actions">
              <Space>
                <Button
                  type="primary"
                  htmlType="submit"
                  loading={saving}
                  disabled={busy || !dirty}
                >
                  保存
                </Button>
                <Button
                  disabled={saving}
                  onClick={() => {
                    form.resetFields();
                    setDirty(false);
                    void query.refetch();
                  }}
                >
                  刷新设置
                </Button>
              </Space>
            </Form.Item>
            {kind === 'fpm' && (
              <Typography.Paragraph type="secondary">
                预设按每进程约20MB估算，实际占用以业务为准。连接端点由网站配置共用，此处保留当前端点；需要修改时请同步调整网站FastCGI配置。
              </Typography.Paragraph>
            )}
          </ApplicationSettingsForm>
        )
      )}
    </Space>
  );
}
function DisabledFunctions({ id, busy }: ContentProps) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ['php-config', id, 'ini'],
    queryFn: () => phpConfig(id, 'ini'),
  });
  const [name, setName] = useState('');
  const [saving, setSaving] = useState(false);
  const names = (query.data?.values.disable_functions || '')
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean);
  const save = async (next: string[]) => {
    if (!query.data) return;
    setSaving(true);
    try {
      await savePHPConfig(id, 'ini', query.data.revision, {
        values: { disable_functions: next.join(',') },
      });
      setName('');
      await client.invalidateQueries({ queryKey: ['php-config', id] });
      message.success('禁用函数已保存，已发起 PHP-FPM 重载');
    } finally {
      setSaving(false);
    }
  };
  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <ApplicationSettingsForm>
        <Form.Item label="函数名称" htmlFor="disabled-function">
          <Space.Compact style={{ width: '100%', maxWidth: 550 }}>
            <Input
              id="disabled-function"
              placeholder="添加要禁用的函数名，如exec"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <Button
              type="primary"
              disabled={busy || !name.trim() || !query.data}
              loading={saving}
              onClick={() =>
                void save([
                  ...new Set([
                    ...names,
                    ...name.split(/[\s,]+/).filter(Boolean),
                  ]),
                ])
              }
            >
              保存
            </Button>
          </Space.Compact>
        </Form.Item>
      </ApplicationSettingsForm>
      {query.error && <Alert type="error" message={query.error.message} />}
      <ContentSizedTable
        fillColumn="name"
        rowKey="name"
        size="small"
        loading={query.isFetching}
        pagination={false}
        dataSource={names.map((name) => ({ name }))}
        columns={[
          { title: '名称', dataIndex: 'name' },
          {
            title: '操作',
            width: 90,
            render: (_, row) => (
              <Button
                type="link"
                disabled={busy || saving}
                onClick={() =>
                  modal.confirm({
                    title: `恢复函数 ${row.name} 的使用？`,
                    onOk: () => save(names.filter((n) => n !== row.name)),
                  })
                }
              >
                删除
              </Button>
            ),
          },
        ]}
      />
      <Typography.Text type="secondary">
        禁止指定函数调用。删除条目会恢复函数可用性，保存后自动重载 PHP。
      </Typography.Text>
    </Space>
  );
}
function Extensions({ id, busy }: ContentProps) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const [tab, setTab] = useState('list');
  const [working, setWorking] = useState(false);
  const [name, setName] = useState('');
  const [latestTask, setLatestTask] = useState<string>();
  const query = useQuery({
    queryKey: ['php-extensions', id],
    queryFn: () => phpExtensions(id),
  });
  const installed = new Map(
    (query.data?.installed ?? []).map((row) => [row.module, row]),
  );
  const change = (
    name: string,
    action: 'install' | 'uninstall',
    version = '',
    option?: PHPExtensionOption,
  ) => {
    let acceptedLicense = false;
    const loader = action === 'install' && option?.source === 'loader';
    const dialog = modal.confirm({
      title: `${action === 'install' ? '安装' : '卸载'} ${name}？`,
      okButtonProps: { disabled: loader },
      content: (
        <Space direction="vertical">
          <Typography.Text>
            只影响当前PHP版本。完成后请重载PHP服务。
          </Typography.Text>
          {option?.notice && <Typography.Text>{option.notice}</Typography.Text>}
          {loader && (
            <Checkbox
              onChange={(event) => {
                acceptedLicense = event.target.checked;
                dialog.update({
                  okButtonProps: { disabled: !acceptedLicense },
                });
              }}
            >
              我已阅读并同意
              <Typography.Link
                href={
                  option.module === 'ioncube'
                    ? 'https://www.ioncube.com/loaders.php'
                    : 'https://www.sourceguardian.com/loaders.html'
                }
                target="_blank"
                rel="noopener noreferrer"
              >
                厂商加载器许可条款
              </Typography.Link>
            </Checkbox>
          )}
        </Space>
      ),
      onOk: async () => {
        setWorking(true);
        try {
          const result = await changePHPExtension(
            id,
            name,
            action,
            version,
            acceptedLicense,
          );
          setLatestTask(result.taskId);
          useMessageBox.getState().show(result.taskId);
          await client.invalidateQueries({ queryKey: ['software-tasks'] });
        } finally {
          setWorking(false);
        }
      },
    });
  };
  const operation = (row: PHPExtension, option?: PHPExtensionOption) => (
    <Tooltip
      title={
        row.builtin
          ? 'PHP内置扩展，不能单独卸载'
          : !row.managed
            ? '由其他配置加载，请在配置文件中管理'
            : '移除当前版本的扩展加载配置'
      }
    >
      <Button
        type="link"
        disabled={busy || working || row.builtin || !row.managed}
        onClick={() => change(row.module, 'uninstall', '', option)}
      >
        卸载
      </Button>
    </Tooltip>
  );
  const installedRows = query.data?.installed ?? [];
  const peclRows = installedRows.filter(
    (row) => row.source === 'pecl' && !row.builtin,
  );
  const installedColumns = [
    { title: '扩展名称', dataIndex: 'name' },
    {
      title: '版本',
      dataIndex: 'version',
      render: (value: string) => value || '—',
    },
    {
      title: '类型',
      render: (_: unknown, row: PHPExtension) =>
        row.builtin
          ? '内置扩展'
          : row.source === 'pecl'
            ? 'PECL扩展'
            : row.source === 'loader'
              ? '厂商加载器'
              : '动态扩展',
    },
    {
      title: '操作',
      width: 100,
      render: (_: unknown, row: PHPExtension) => operation(row),
    },
  ];
  const taskId = latestTask || query.data?.latestTaskId;
  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          { key: 'list', label: '安装列表' },
          { key: 'installed', label: '已安装扩展' },
          { key: 'pecl', label: 'PECL扩展安装' },
        ]}
        tabBarExtraContent={
          <Tooltip title="刷新扩展列表">
            <Button
              icon={<ReloadOutlined />}
              aria-label="刷新扩展列表"
              loading={query.isFetching}
              onClick={() => void query.refetch()}
            />
          </Tooltip>
        }
      />
      {query.error && <Alert type="error" message={query.error.message} />}
      {tab === 'pecl' && (
        <>
          <ApplicationSettingsForm
            onFinish={() => {
              const match = name
                .trim()
                .match(/^([a-zA-Z][a-zA-Z0-9_]*)(?:-([0-9][a-zA-Z0-9.]*))?$/);
              if (!match) {
                void message.info(
                  '请输入PECL扩展名称，可使用redis-6.0.2指定版本',
                );
                return;
              }
              const option = query.data?.catalog.find(
                (row) =>
                  row.name.toLowerCase() === match[1].toLowerCase() ||
                  row.module === match[1].toLowerCase(),
              );
              if (option && option.source !== 'pecl') {
                void message.info('该扩展请从安装列表使用对应的官方安装来源');
                return;
              }
              change(match[1].toLowerCase(), 'install', match[2] || '', option);
            }}
          >
            <Form.Item label="扩展名称" htmlFor="pecl-extension-name">
              <Input
                value={name}
                onChange={(event) => setName(event.target.value)}
                id="pecl-extension-name"
                placeholder="请输入扩展名称，如redis或redis-6.0.2"
              />
            </Form.Item>
            <Form.Item className="application-settings-actions">
              <Space wrap>
                <Button
                  htmlType="submit"
                  type="primary"
                  disabled={busy || !name.trim()}
                  loading={working}
                >
                  点击安装
                </Button>

                <Button
                  disabled={!taskId}
                  onClick={() => useMessageBox.getState().show(taskId)}
                >
                  安装日志
                </Button>
              </Space>
            </Form.Item>
          </ApplicationSettingsForm>
          <ContentSizedTable<PHPExtension>
            fillColumn="name"
            rowKey="module"
            size="small"
            loading={query.isFetching}
            dataSource={peclRows}
            columns={installedColumns}
            pagination={false}
            scroll={{ y: 340 }}
            locale={{ emptyText: '暂无数据' }}
          />
          <Typography.Paragraph type="secondary" style={{ marginTop: 16 }}>
            <ul style={{ paddingLeft: 20, margin: 0 }}>
              <li>相同扩展不能在安装列表和PECL中重复安装。</li>
              <li>出现安装异常时，请查看安装日志。</li>
              <li>安装参数采用默认值；未指定版本时按PHP版本选择兼容版本。</li>
              <li>安装日志按钮显示当前PHP最近一次扩展安装或卸载任务。</li>
            </ul>
          </Typography.Paragraph>
        </>
      )}
      {tab === 'installed' && (
        <ContentSizedTable<PHPExtension>
          fillColumn="name"
          rowKey="module"
          size="small"
          loading={query.isFetching}
          dataSource={installedRows}
          columns={installedColumns}
          pagination={false}
          scroll={{ y: 420 }}
        />
      )}
      {tab === 'list' && (
        <ContentSizedTable<PHPExtensionOption>
          fillColumn="description"
          rowKey="name"
          size="small"
          loading={query.isFetching}
          dataSource={query.data?.catalog ?? []}
          pagination={false}
          scroll={{ y: 440 }}
          columns={[
            { title: '名称', dataIndex: 'name', width: 125 },
            { title: '类型', dataIndex: 'category', width: 95 },
            { title: '说明', dataIndex: 'description' },
            {
              title: '状态',
              width: 65,
              render: (_, row) =>
                installed.has(row.module) ? (
                  <CheckOutlined
                    style={{ color: 'var(--ant-color-success)' }}
                  />
                ) : (
                  <CloseOutlined />
                ),
            },
            {
              title: '操作',
              width: 90,
              fixed: fixedActions,
              render: (_, row) => {
                const loaded = installed.get(row.module);
                return loaded ? (
                  operation(loaded, row)
                ) : (
                  <Tooltip title={row.notice}>
                    <Button
                      type="link"
                      disabled={busy || working || row.disabled}
                      onClick={() => change(row.name, 'install', '', row)}
                    >
                      安装
                    </Button>
                  </Tooltip>
                );
              },
            },
          ]}
        />
      )}
    </Space>
  );
}

const loadFields = [
  ['pool', '应用池'],
  ['process manager', '进程管理方式'],
  ['start time', '启动时间'],
  ['accepted conn', '请求数'],
  ['listen queue', '请求队列'],
  ['max listen queue', '最大等待队列'],
  ['idle processes', '空闲进程数'],
  ['active processes', '活跃进程数'],
  ['total processes', '总进程数'],
  ['max active processes', '最大活跃进程数'],
  ['max children reached', '到达进程上限次数'],
  ['slow requests', '慢请求数'],
] as const;
function LoadStatus({ id, busy }: { id: string; busy: boolean }) {
  const query = useQuery({
    queryKey: ['php-load', id],
    queryFn: () => phpLoad(id),
  });
  const [saving, setSaving] = useState(false);
  const { message } = App.useApp();
  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Button
        icon={<ReloadOutlined />}
        loading={query.isFetching}
        onClick={() => void query.refetch()}
      >
        刷新状态
      </Button>
      {query.error && <Alert type="error" message={query.error.message} />}
      {query.isPending ? (
        <Skeleton active />
      ) : query.data?.enabled === false ? (
        <Alert
          type="info"
          message="尚未启用FPM负载统计"
          description="启用后仅通过面板连接本机FastCGI读取，不创建公开状态页面。"
          action={
            <Button
              disabled={busy}
              loading={saving}
              onClick={async () => {
                setSaving(true);
                try {
                  const config = await phpConfig(id, 'fpm');
                  await savePHPConfig(id, 'fpm', config.revision, {
                    values: { 'pm.status_path': '/__ezpanel_fpm_status' },
                  });
                  await query.refetch();
                  message.success('配置已保存，已发起 PHP-FPM 重载');
                } finally {
                  setSaving(false);
                }
              }}
            >
              启用并重载PHP
            </Button>
          }
        />
      ) : (
        query.data?.values && (
          <Descriptions
            bordered
            column={1}
            size="small"
            items={loadFields.map(([key, label]) => ({
              key,
              label: `${label} (${key})`,
              children:
                key === 'start time'
                  ? new Date(
                      Number(query.data!.values![key]) * 1000,
                    ).toLocaleString()
                  : (query.data!.values![key] ?? '—'),
            }))}
          />
        )
      )}
    </Space>
  );
}
function PHPText({ id, kind }: { id: string; kind: 'info' | 'logs' }) {
  const query = useQuery({
    queryKey: ['php-text', id, kind],
    queryFn: () => (kind === 'info' ? phpInfo(id) : applicationLogs(id)),
  });
  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <div className="app-settings-toolbar">
        <Typography.Text type="secondary">
          {kind === 'info'
            ? '当前版本 CLI 的 phpinfo 信息（环境变量、全局变量不输出），FPM 配置可能存在池级覆盖。'
            : '展示最近的 PHP-FPM 运行日志'}
        </Typography.Text>
        <Button
          icon={<ReloadOutlined />}
          loading={query.isFetching}
          onClick={() => void query.refetch()}
        >
          刷新
        </Button>
      </div>
      {query.error && <Alert type="error" message={query.error.message} />}
      <LogText
        key={kind}
        follow={kind === 'logs'}
        aria-label={kind === 'info' ? 'PHP 信息' : '日志内容'}
        style={{
          fontFamily: MONO_FONT,
          background: TERMINAL_COLORS.background,
          color: TERMINAL_COLORS.foreground,
          padding: SPACING.md,
          height: 'max(360px, calc(72dvh - 135px))',
          overflow: 'auto',
          whiteSpace: 'pre-wrap',
          overflowWrap: 'anywhere',
        }}
      >
        {query.data?.content || (query.isFetching ? '正在读取…' : '暂无数据')}
      </LogText>
    </Space>
  );
}
