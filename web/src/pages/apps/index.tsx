import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import {
  Alert,
  App,
  AutoComplete,
  Badge,
  Button,
  Card,
  Empty,
  Flex,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Tabs,
  Tag,
  Skeleton,
  Tooltip,
  Typography,
  Grid,
  type TableColumnsType,
} from 'antd';
import { ArrowUpOutlined } from '@ant-design/icons';
import { LayoutGrid, RotateCw, Search } from 'lucide-react';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  catalog,
  installApplication,
  updateApplication,
  switchApplication,
  checkApplicationUpdates,
  uninstallApplication,
  applicationService,
  applicationVersions,
  applicationSources,
  type Application,
  type Release,
} from '../../api/software';
import { listTasks } from '../../api/task';
import { registries } from '../../api/docker';
import type { Plugin } from '../../api/plugin';
import PluginsPage from '../plugins';
import { useMessageBox } from '../../stores/messageBox';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import SectionTabs from '../../components/SectionTabs';
import ApplicationSettings from './ApplicationSettings';
import RuntimeEnvironments from './RuntimeEnvironments';
import DatabaseInstances from './DatabaseInstances';
import { LAYOUT, SPACING } from '../../styles/tokens';
import './apps.css';
const categories: Record<string, string> = {
  web: '运行环境',
  database: '数据库',
  tools: '工具',
};
type AppRow = Application & { plugin?: Plugin };
const projectRuntimes = ['python', 'nodejs', 'go'];
export default function AppStorePage() {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ['software-catalog'],
    queryFn: ({ signal }) => catalog(signal),
    staleTime: 60000,
  });
  // 订阅主布局已有的任务查询，不再为商店重复启动轮询。
  const running = useQuery({
    queryKey: ['software-tasks', 'running'],
    queryFn: ({ signal }) =>
      listTasks({ module: 'software', status: 'running' }, signal),
    enabled: false,
  });
  const activeTasks = running.data?.list ?? query.data?.activeTasks ?? [];
  const [params, setParams] = useSearchParams();
  const [view, setView] = useState('all');
  const [runtimeList, setRuntimeList] = useState<string>();
  const [serviceList, setServiceList] = useState<string>();
  const [keyword, setKeyword] = useState('');
  const [selected, setSelected] = useState<Application>();
  const [manualOperation, setManualOperation] = useState<{
    app: Application;
    action: 'install' | 'uninstall' | 'update' | 'switch';
    version?: string;
    environmentId?: string;
    instanceId?: string;
    newInstance?: boolean;
  }>();
  const [working, setWorking] = useState(false);
  const [confirmedMirror, setConfirmedMirror] = useState('');
  const manualVersions = useRef({ version: false, target: false });
  const [form] = Form.useForm<{
    confirmName: string;
    version: string;
    targetVersion: string;
    source: string;
    mirror: string;
    registry: string;
    instanceName: string;
  }>();
  const requestedApp = query.data?.list.find(
    (item) => item.id === params.get('install'),
  );
  const settingsApp = query.data?.list.find(
    (item) => item.id === params.get('settings'),
  );
  const { section } = useParams();
  const navigate = useNavigate();
  const {
    tab: activeCategory,
    loading: navigationLoading,
    invalid,
    selectTab,
  } = useRoutedTab(
    'apps',
    '/apps',
    section,
    Object.keys(categories),
    {
      keys: ['tab'],
      tab: settingsApp?.category ?? requestedApp?.category ?? params.get('tab'),
    },
    !section &&
      query.isPending &&
      (params.has('install') || params.has('settings')),
  );
  const applications = query.data?.list ?? [];
  const phpApps = applications.filter((app) => app.id.startsWith('php-'));
  const phpInstalled = phpApps.filter((app) => app.installed);
  const runtimeApp = applications.find((app) => app.id === runtimeList);
  const databaseApp = applications.find(
    (app) =>
      app.id === (serviceList ?? settingsApp?.id) &&
      app.category === 'database',
  );
  const taskFor = (id: string) =>
    activeTasks.find((task) =>
      id === 'php'
        ? task.resourceId.startsWith('php-')
        : task.resourceId === id,
    );
  const operation = useMemo<typeof manualOperation>(
    () =>
      manualOperation ??
      (requestedApp &&
      (projectRuntimes.includes(requestedApp.id)
        ? requestedApp.installable
        : !requestedApp.installed ||
          Boolean(requestedApp.updateable && requestedApp.updateVersion))
        ? {
            app: requestedApp,
            action:
              requestedApp.installed &&
              !projectRuntimes.includes(requestedApp.id)
                ? ('update' as const)
                : ('install' as const),
            version: undefined,
          }
        : undefined),
    [manualOperation, requestedApp],
  );
  function setOperation(value: typeof manualOperation) {
    manualVersions.current = { version: false, target: false };
    setManualOperation(value);
    if (params.has('install')) {
      const next = new URLSearchParams(params);
      next.delete('install');
      setParams(next, { replace: true });
    }
  }
  function resetVersions() {
    manualVersions.current = { version: false, target: false };
    form.resetFields(['version', 'targetVersion']);
  }
  const source = Form.useWatch('source', form) ?? 'official';
  const mirror = Form.useWatch('mirror', form) ?? '';
  const registry = Form.useWatch('registry', form) ?? '';
  const sources = useQuery({
    queryKey: ['app-sources', operation?.app.id],
    queryFn: () => applicationSources(operation!.app.id),
    enabled: !!operation && operation.action !== 'uninstall',
  });
  const containerInstall = sources.data?.container === true;
  const sourcePreference = `ezpanel.install-source.${containerInstall ? 'docker' : operation?.app.id}`;
  const registryList = useQuery({
    queryKey: ['docker-registries'],
    queryFn: registries,
    enabled: !!operation && containerInstall && source === 'registry',
  });
  useEffect(() => {
    if (
      !operation ||
      !sources.data ||
      sources.isFetching ||
      operation.action === 'uninstall' ||
      form.isFieldTouched('source')
    )
      return;
    const { list, current, mirrors } = sources.data;
    const remember =
      operation.action === 'install' ||
      operation.action === 'update' ||
      operation.action === 'switch';
    const previous = remember
      ? localStorage.getItem(`${sourcePreference}.type`)
      : null;
    const preferredSource = list.some((item) => item.value === previous)
      ? previous
      : current.source;
    const preferredMirror = remember
      ? localStorage.getItem(`${sourcePreference}.mirror`)
      : null;
    form.setFieldsValue({
      source: preferredSource ?? current.source,
      mirror:
        preferredMirror ||
        (current.source === 'custom' ? current.mirror : '') ||
        mirrors?.[0] ||
        '',
      registry: remember
        ? (localStorage.getItem(`${sourcePreference}.registry`) ?? '')
        : '',
    });
  }, [sources.data, sources.isFetching, operation, form, sourcePreference]);
  const sourceReady =
    source === 'registry'
      ? !!registry.trim()
      : source !== 'custom' ||
        (/^https:\/\//.test(mirror.trim()) &&
          (containerInstall ||
            mirror.trim() === confirmedMirror ||
            (sources.data?.current.source === 'custom' &&
              mirror.trim() === sources.data.current.mirror)));
  const versionSource = containerInstall ? 'official' : source;
  const versionMirror = versionSource === 'custom' ? mirror.trim() : '';
  const versionKey = [
    'app-versions',
    operation?.app.id,
    versionSource,
    versionMirror,
  ];
  const versions = useQuery({
    queryKey: versionKey,
    queryFn: ({ signal }) =>
      applicationVersions(
        operation!.app.id,
        false,
        { source: versionSource, mirror: versionMirror },
        signal,
      ),
    enabled:
      !!operation &&
      operation.action !== 'uninstall' &&
      !!sources.data &&
      !sources.isFetching &&
      (containerInstall || sourceReady),
  });
  const releaseList = useMemo(() => {
    const rows = versions.data?.list ?? [];
    if (
      operation?.action !== 'install' ||
      source !== 'official' ||
      !projectRuntimes.includes(operation.app.id)
    )
      return rows;
    return [
      ...rows,
      ...(operation.app.installedVersions ?? [])
        .filter((version) => !rows.some((row) => row.version === version))
        .map((version): Release => ({
          version,
          source: '已有镜像',
          method: 'docker',
        })),
    ];
  }, [versions.data, operation, source]);
  const installVersionOptions = projectRuntimes.includes(
    operation?.app.id ?? '',
  )
    ? [...releaseList].sort((a, b) =>
        b.version.localeCompare(a.version, 'en', { numeric: true }),
      )
    : releaseList;
  const switchTargets =
    versions.data?.list.filter((r) => r.version !== operation?.app.version) ??
    [];
  const upgradeTarget = versions.data?.list.find(
    (r) =>
      operation?.app.version &&
      (r.method === 'package'
        ? r.version === operation.app.updateVersion
        : r.version.split('.').slice(0, 2).join('.') ===
            operation.app.version.split('.').slice(0, 2).join('.') &&
          r.version.localeCompare(operation.app.version, 'en', {
            numeric: true,
          }) > 0),
  );
  useEffect(() => {
    if (
      !operation ||
      operation.action === 'uninstall' ||
      sources.isFetching ||
      versions.isFetching ||
      form.getFieldValue('source') !== source ||
      (source === 'custom' && form.getFieldValue('mirror') !== mirror)
    )
      return;
    const patch: Record<string, string> = {};
    if (!manualVersions.current.version) {
      const value =
        operation.action === 'install'
          ? releaseList.find((release) => !release.unavailableReason)?.version
          : operation.app.version;
      if (value) patch.version = value;
    }
    const target =
      operation.action === 'switch'
        ? versions.data?.list.find(
            (item) => item.version !== operation.app.version,
          )
        : upgradeTarget;
    if (target && !manualVersions.current.target)
      patch.targetVersion = target.version;
    if (Object.keys(patch).length) form.setFieldsValue(patch);
  }, [
    operation,
    versions.data,
    upgradeTarget,
    form,
    sources.isFetching,
    versions.isFetching,
    source,
    mirror,
    releaseList,
  ]);
  const appBusy = (app: Application) =>
    working ||
    activeTasks.some(
      (task) =>
        task.resourceId === app.id ||
        ((app.id === 'mysql' || app.id === 'mariadb') &&
          (task.resourceId === 'mysql' || task.resourceId === 'mariadb')),
    );
  function closeLinkedSettings() {
    if (!params.has('settings')) return;
    const next = new URLSearchParams(params);
    next.delete('settings');
    next.delete('instance');
    setParams(next, { replace: true });
  }
  const current =
    query.data?.list.find((app) => app.id === selected?.id) ??
    selected ??
    (settingsApp?.category !== 'database' ? settingsApp : undefined) ??
    (requestedApp?.installed &&
    !requestedApp.updateVersion &&
    !projectRuntimes.includes(requestedApp.id)
      ? requestedApp
      : undefined);
  const refresh = () => {
    for (const key of [
      'software-catalog',
      'sysinfo-summary',
      'website-env',
      'website-php-versions',
      'runtime-environments',
      'database-instances',
      'db-env',
    ])
      void client.invalidateQueries({ queryKey: [key] });
  };
  function openOperation(
    app: Application,
    action: 'install' | 'uninstall' | 'update' | 'switch',
    version?: string,
    options?: {
      environmentId?: string;
      instanceId?: string;
      newInstance?: boolean;
    },
  ) {
    if (
      action === 'uninstall' &&
      app.usedBySites &&
      app.usedBySites.length > 0
    ) {
      modal.warning({
        title: `无法卸载 ${app.name}`,
        content: (
          <Space direction="vertical" size={8} style={{ width: '100%' }}>
            <div>
              当前有 {app.usedBySites.length} 个网站正在使用 {app.name}
              ，无法直接卸载：
            </div>
            <Space size={[4, 4]} wrap>
              {app.usedBySites.map((siteName) => (
                <Tag key={siteName} color="warning">
                  {siteName}
                </Tag>
              ))}
            </Space>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              请先在「网站」中将这些网站的 PHP
              版本切换为其他版本或改为纯静态后再卸载。
            </Typography.Text>
          </Space>
        ),
        okText: '前往网站管理',
        cancelText: '关闭',
        okCancel: true,
        onOk: () => {
          setServiceList(undefined);
          setSelected(undefined);
          navigate('/websites');
        },
      });
      return;
    }
    form.resetFields();
    setConfirmedMirror('');
    setOperation({ app, action, version, ...options });
  }
  async function perform() {
    if (!operation) return;
    const { app, action } = operation;
    const values = await form.validateFields();
    const selection = {
      source: values.source,
      mirror: (values.source === 'registry'
        ? values.registry
        : values.mirror
      )?.trim(),
    };
    setWorking(true);
    try {
      const result =
        action === 'switch'
          ? await switchApplication(app.id, values.targetVersion, selection)
          : action === 'update'
            ? await updateApplication(
                app.id,
                values.targetVersion,
                selection,
                operation.instanceId,
              )
            : action === 'install'
              ? await installApplication(
                  app.id,
                  values.version,
                  selection,
                  operation.newInstance
                    ? { newInstance: true, instanceName: values.instanceName }
                    : undefined,
                )
              : await uninstallApplication(
                  app.id,
                  values.confirmName,
                  operation.environmentId,
                );
      if (action === 'install' || action === 'update' || action === 'switch') {
        localStorage.setItem(`${sourcePreference}.type`, selection.source);
        if (
          selection.mirror &&
          (selection.source === 'custom' || selection.source === 'registry')
        ) {
          const key = selection.source === 'registry' ? 'registry' : 'mirror';
          localStorage.setItem(`${sourcePreference}.${key}`, selection.mirror);
        }
      }
      useMessageBox.getState().show(result.taskId);
      void client.invalidateQueries({ queryKey: ['software-tasks'] });
      setOperation(undefined);
      refresh();
    } finally {
      setWorking(false);
    }
  }
  function control(
    app: Application,
    action: 'start' | 'stop' | 'restart' | 'reload',
  ) {
    const text = {
      start: '启动',
      stop: '停止',
      restart: '重启',
      reload: '重载配置',
    }[action];
    modal.confirm({
      title: `${text} ${app.name}？`,
      content:
        action === 'reload' && app.id === 'supervisor'
          ? '配置有变化的受管进程可能会重新启动。'
          : action === 'stop' || action === 'restart'
            ? '操作期间依赖此应用的服务可能暂时不可用。'
            : undefined,
      onOk: async () => {
        setWorking(true);
        try {
          await applicationService(app.id, action);
          if (app.id === 'fail2ban')
            void client.invalidateQueries({
              queryKey: ['fail2ban-protection'],
            });
          message.success(
            action === 'reload' && app.id.startsWith('php-')
              ? '已发起 PHP-FPM 重载'
              : `${text}操作已完成`,
          );
          refresh();
        } finally {
          setWorking(false);
        }
      },
    });
  }
  function systemActions(app: AppRow) {
    return app.installed ? (
      <Space size={12}>
        {app.updateable && app.updateVersion && (
          <Button
            type="link"
            style={{ padding: 0 }}
            disabled={appBusy(app)}
            onClick={() => openOperation(app, 'update')}
          >
            更新
          </Button>
        )}
        <Button
          type="link"
          style={{ padding: 0 }}
          onClick={() => setSelected(app)}
        >
          设置
        </Button>
        <Button
          type="link"
          style={{ padding: 0 }}
          danger
          disabled={!app.uninstallable || appBusy(app)}
          onClick={() => openOperation(app, 'uninstall')}
        >
          卸载
        </Button>
      </Space>
    ) : (
      <Tooltip title={app.reason}>
        <Button
          type="link"
          disabled={!app.installable || appBusy(app)}
          onClick={() => openOperation(app, 'install')}
        >
          {projectRuntimes.includes(app.id) ? '安装版本' : '安装'}
        </Button>
      </Tooltip>
    );
  }
  function listActions(app: AppRow) {
    if (app.id === 'php') {
      return (
        <Button type="link" onClick={() => setServiceList('php')}>
          {app.installed ? '版本管理' : '安装版本'}
        </Button>
      );
    }
    if (app.category === 'database') {
      return app.instanceCount > 0 ? (
        <Button type="link" onClick={() => setServiceList(app.id)}>
          实例管理
        </Button>
      ) : (
        <Tooltip title={app.reason}>
          <Button
            type="link"
            disabled={!app.installable || appBusy(app)}
            onClick={() => openOperation(app, 'install')}
          >
            新建实例
          </Button>
        </Tooltip>
      );
    }
    if (app.installed && projectRuntimes.includes(app.id)) {
      return (
        <Button type="link" onClick={() => setRuntimeList(app.id)}>
          环境管理
        </Button>
      );
    }
    return systemActions(app);
  }
  const toolbar = (showCheckUpdates: boolean) => (
    <Space>
      {showCheckUpdates && (
        <Button
          loading={working}
          onClick={async () => {
            setWorking(true);
            try {
              const r = await checkApplicationUpdates('tools');
              void client.invalidateQueries({ queryKey: ['app-versions'] });
              refresh();
              const errors = r.list.filter((item) => item.notice);
              if (errors.length)
                modal.info({
                  title: '部分软件未能更新版本目录',
                  content: errors.map((item) => (
                    <div key={item.id}>
                      {item.id}：{item.notice}
                    </div>
                  )),
                });
              else message.success('已检查当前下载源的可用更新');
            } finally {
              setWorking(false);
            }
          }}
        >
          检查更新
        </Button>
      )}
      <Button
        icon={<RotateCw size={16} strokeWidth={1.9} />}
        loading={query.isFetching}
        onClick={refresh}
        aria-label="刷新应用"
        title="刷新应用"
      />
    </Space>
  );
  const [
    nameColumn,
    versionColumn,
    descriptionColumn,
    sourceColumn,
    statusColumn,
  ]: TableColumnsType<AppRow> = [
    {
      title: '软件名称',
      key: 'name',
      className: 'app-catalog-name',
      render: (_, app) => {
        const name = <Typography.Text strong>{app.name}</Typography.Text>;
        return (
          <Space>
            <span className="apple-app-icon">
              <LayoutGrid size={15} strokeWidth={1.9} />
            </span>
            {app.id === 'php' ||
            (app.instanceCount > 0 && app.category === 'database') ? (
              <Button
                type="link"
                style={{ paddingInline: 0 }}
                onClick={() => setServiceList(app.id)}
              >
                {app.name}
              </Button>
            ) : app.installed && projectRuntimes.includes(app.id) ? (
              <Button
                type="link"
                style={{ paddingInline: 0 }}
                onClick={() => setRuntimeList(app.id)}
              >
                {app.name}
              </Button>
            ) : app.installed && !app.plugin ? (
              <Button
                type="link"
                style={{ paddingInline: 0 }}
                onClick={() => setSelected(app)}
              >
                {app.name}
              </Button>
            ) : (
              name
            )}
          </Space>
        );
      },
    },
    {
      title: '版本',
      key: 'version',
      className: 'app-catalog-version',
      render: (_, app) => {
        if (!app.installed) return app.version || '—';
        const versions =
          app.id === 'php'
            ? phpInstalled.map((branch) => ({
                key: branch.id,
                version: branch.version ?? branch.id.slice(4),
                updateVersion: branch.updateable
                  ? branch.updateVersion
                  : undefined,
              }))
            : (app.installedVersions?.length
                ? app.installedVersions
                : app.version
                  ? [app.version]
                  : []
              ).map((version) => ({
                key: version,
                version,
                updateVersion:
                  app.updateable && version === app.version
                    ? app.updateVersion
                    : undefined,
              }));
        if (!versions.length) return '未识别';
        return (
          <Space wrap size={[4, 4]}>
            {versions.map(({ key, version, updateVersion }) => (
              <Tooltip
                key={key}
                title={updateVersion && `可升级至 ${updateVersion}`}
              >
                <Tag
                  color={updateVersion ? 'red' : undefined}
                  icon={updateVersion ? <ArrowUpOutlined /> : undefined}
                  style={{ marginInlineEnd: 0 }}
                >
                  {version}
                </Tag>
              </Tooltip>
            ))}
          </Space>
        );
      },
    },
    {
      title: '说明',
      dataIndex: 'description',
      className: 'app-catalog-description',
      render: (description: string, row) =>
        row.id === 'openresty' ? `Web 网关 · ${description}` : description,
    },
    {
      title: '安装来源',
      key: 'source',
      className: 'app-catalog-source',
      render: (_, app) => (app.installed ? app.pkgManager || '未识别' : ''),
    },
    {
      title: '状态',
      key: 'status',
      className: 'app-catalog-status',
      render: (_, app) => {
        const task = taskFor(app.id);
        if (task)
          return (
            <Button
              type="link"
              onClick={() => useMessageBox.getState().show(task.taskId)}
            >
              <Badge
                status="processing"
                text={
                  (
                    {
                      install: '安装中',
                      update: '更新中',
                      uninstall: '卸载中',
                      switch: '切换中',
                    } as Record<string, string>
                  )[task.action] ?? '处理中'
                }
              />
            </Button>
          );
        if (app.category === 'database' && app.instanceCount) {
          const running = app.runningInstances ?? 0;
          return (
            <Badge
              status={running ? 'success' : 'default'}
              text={`${running} 个运行中`}
            />
          );
        }
        if (app.installed && projectRuntimes.includes(app.id)) {
          const running = query.data?.projectRunningCounts[app.id] ?? 0;
          return (
            <Badge
              status={running ? 'success' : 'default'}
              text={`${running} 个运行中`}
            />
          );
        }
        return (
          <Tooltip title={app.reason}>
            <Badge
              status={
                app.id === 'php' &&
                phpInstalled.some((php) => php.running === true)
                  ? 'success'
                  : !app.installed
                    ? 'default'
                    : app.running === true
                      ? 'success'
                      : app.running === false
                        ? 'warning'
                        : 'processing'
              }
              text={
                app.id === 'php' && phpInstalled.length
                  ? `${phpInstalled.filter((php) => php.running === true).length} 个运行中`
                  : !app.installed
                    ? '未安装'
                    : app.plugin && !app.plugin.enabled
                      ? '未启用'
                      : app.running === true
                        ? '运行中'
                        : app.running === false
                          ? '已停止'
                          : '已安装'
              }
            />
          </Tooltip>
        );
      },
    },
  ];
  const columnsByCategory: Record<string, TableColumnsType<AppRow>> = {
    web: [
      { ...nameColumn, title: '运行环境' },
      { ...versionColumn, title: '已安装版本' },
      {
        title: '环境数',
        key: 'instances',
        render: (_, app) =>
          app.id === 'php'
            ? phpInstalled.length
            : projectRuntimes.includes(app.id)
              ? (app.instanceCount ?? 0)
              : Number(app.installed),
      },
      {
        title: '项目实例',
        key: 'projects',
        render: (_, app) =>
          projectRuntimes.includes(app.id)
            ? (query.data?.projectInstanceCounts?.[app.id] ?? 0)
            : '—',
      },
      descriptionColumn,
      statusColumn,
    ],
    database: [
      { ...nameColumn, title: '数据库服务' },
      { ...versionColumn, title: '已部署版本' },
      {
        title: '实例数',
        key: 'instances',
        render: (_, app) => app.instanceCount,
      },
      { ...statusColumn, title: '运行状态' },
      sourceColumn,
    ],
    tools: [
      { ...nameColumn, title: '工具' },
      versionColumn,
      descriptionColumn,
      statusColumn,
    ],
  };
  if (invalid) return <RouteNotFound back="/apps" />;
  if (navigationLoading) return <Skeleton active />;
  return (
    <Space direction="vertical" size={SPACING.lg} style={{ width: '100%' }}>
      {query.error && (
        <Alert
          type="error"
          message="应用目录读取失败"
          description={query.error.message}
          action={<Button onClick={() => void query.refetch()}>重试</Button>}
        />
      )}
      <SectionTabs
        activeKey={activeCategory}
        onChange={(key) => {
          void selectTab(key);
          setView('all');
        }}
        items={Object.entries(categories).map(([key, label]) => ({
          key,
          label,
        }))}
      />
      <PluginsPage
        renderList={(plugins, pluginActions, importApp) => {
          const rows: AppRow[] = applications.filter(
            (app) =>
              app.category === activeCategory &&
              app.id !== 'docker' &&
              !app.id.startsWith('php-'),
          );
          if (activeCategory === 'web' && phpApps.length > 0) {
            rows.unshift({
              ...phpApps[0],
              id: 'php',
              name: 'PHP',
              description: '多版本 PHP-FPM',
              installed: phpInstalled.length > 0,
              installedVersions: phpInstalled.map(
                (app) => app.version ?? app.id.slice(4),
              ),
              version: null,
              running: null,
              updateVersion: phpInstalled.find(
                (app) => app.updateable && app.updateVersion,
              )?.updateVersion,
              updateable: true,
              reason: '',
            });
          } else if (activeCategory === 'tools') {
            rows.push(
              ...plugins.map((p) => ({
                id: `plugin:${p.id}`,
                name: p.name,
                description: p.description,
                category: 'tools',
                version: p.version,
                installed: true,
                instanceCount: 0,
                running: p.runtimeStatus === 'running',
                manageable: true,
                installable: false,
                uninstallable: true,
                reason: p.enabled ? '' : '未启用',
                pkgManager: '导入应用',
                plugin: p,
              })),
            );
          }
          return (
            <Card>
              <Flex
                justify="space-between"
                align="center"
                gap={SPACING.md}
                wrap
                style={{ marginBottom: SPACING.lg }}
              >
                <Tabs
                  size="small"
                  activeKey={view}
                  onChange={(next) => setView(next as typeof view)}
                  tabBarStyle={{ marginBottom: 0 }}
                  items={[
                    { key: 'all', label: '所有状态' },
                    { key: 'installed', label: '已安装' },
                    { key: 'uninstalled', label: '未安装' },
                    { key: 'upgradable', label: '可升级' },
                  ]}
                />
                <Space wrap>
                  <Input
                    allowClear
                    prefix={<Search size={15} strokeWidth={1.9} />}
                    aria-label="搜索应用"
                    placeholder="搜索应用名称或描述"
                    value={keyword}
                    onChange={(e) => setKeyword(e.target.value)}
                    style={{ width: LAYOUT.appSearchWidth, maxWidth: '100%' }}
                  />
                  {activeCategory === 'tools' && (
                    <Button onClick={importApp}>导入应用</Button>
                  )}
                  {toolbar(activeCategory === 'tools')}
                </Space>
              </Flex>
              <ContentSizedTable<AppRow>
                key={activeCategory}
                className={
                  activeCategory === 'database'
                    ? 'app-database-catalog'
                    : undefined
                }
                fillColumn={
                  activeCategory === 'database' ? 'name' : 'description'
                }
                growColumns={
                  activeCategory === 'database' ? ['version'] : ['name']
                }
                size="middle"
                rowKey="id"
                loading={query.isPending}
                dataSource={rows.filter(
                  (app) =>
                    (view !== 'installed' ||
                      app.installed ||
                      app.instanceCount > 0 ||
                      !!taskFor(app.id)) &&
                    (view !== 'uninstalled' ||
                      (!app.installed && !app.instanceCount)) &&
                    (view !== 'upgradable' ||
                      (app.installed &&
                        app.updateable &&
                        !!app.updateVersion)) &&
                    `${app.name} ${app.id} ${app.description} ${app.installedVersions?.join(' ') ?? ''} ${app.version ?? ''}`
                      .toLowerCase()
                      .includes(keyword.toLowerCase()),
                )}
                pagination={{ defaultPageSize: 20, showSizeChanger: true }}
                locale={{
                  emptyText: <Empty description="暂无符合条件的应用" />,
                }}
                columns={[
                  ...columnsByCategory[activeCategory],
                  {
                    title: '操作',
                    fixed: fixedActions,
                    render: (_, app) =>
                      app.plugin ? pluginActions(app.plugin) : listActions(app),
                  },
                ]}
              />
            </Card>
          );
        }}
      />
      {serviceList === 'php' && (
        <Modal
          open
          centered
          title="PHP 版本管理"
          width={LAYOUT.databaseModalWidth}
          onCancel={() => setServiceList(undefined)}
          footer={null}
        >
          <ContentSizedTable<AppRow>
            fillColumn="name"
            rowKey="id"
            pagination={false}
            dataSource={phpApps}
            columns={[
              {
                title: 'PHP 分支',
                key: 'name',
                render: (_, app) => app.id.slice(4),
              },
              {
                title: '已安装版本',
                key: 'version',
                render: (_, app) =>
                  app.installed && app.version ? (
                    <Button
                      type="link"
                      size="small"
                      style={{ padding: 0 }}
                      title="点击检查更新"
                      onClick={async () => {
                        await applicationVersions(app.id, true);
                        await client.invalidateQueries({
                          queryKey: ['software'],
                        });
                        void message.success(`已检查 ${app.name} 可用更新`);
                      }}
                    >
                      {app.version}
                      {app.updateable && app.updateVersion
                        ? ` → ${app.updateVersion}`
                        : ''}
                    </Button>
                  ) : (
                    '—'
                  ),
              },
              statusColumn,
              { title: '操作', render: (_, app) => systemActions(app) },
            ]}
          />
        </Modal>
      )}
      {databaseApp && (
        <DatabaseInstances
          app={databaseApp}
          busy={appBusy(databaseApp)}
          initialInstance={
            settingsApp?.id === databaseApp.id
              ? params.get('instance') || 'default'
              : undefined
          }
          onClose={() => {
            setServiceList(undefined);
            closeLinkedSettings();
          }}
          onUpdate={(row) => {
            openOperation(
              {
                ...databaseApp,
                name: row.name,
                version: row.version,
                updateVersion: row.updateVersion,
              },
              'update',
              undefined,
              { instanceId: row.id },
            );
          }}
          onCreate={() =>
            openOperation(databaseApp, 'install', undefined, {
              newInstance: true,
            })
          }
        />
      )}
      {runtimeApp && (
        <RuntimeEnvironments
          app={runtimeApp}
          busy={appBusy(runtimeApp)}
          onClose={() => setRuntimeList(undefined)}
          onInstall={() => openOperation(runtimeApp, 'install')}
          onUninstall={(environment) =>
            openOperation(runtimeApp, 'uninstall', environment.version, {
              environmentId: environment.id,
            })
          }
        />
      )}
      {current && (
        <ApplicationSettings
          key={current.id}
          app={current}
          busy={appBusy(current)}
          switchVersion={() => {
            setSelected(undefined);
            openOperation(current, 'switch');
          }}
          control={(action) => control(current, action)}
          uninstall={() => {
            setSelected(undefined);
            openOperation(current, 'uninstall');
          }}
          onClose={() => {
            setSelected(undefined);
            if (params.has('install')) setParams({}, { replace: true });
            else closeLinkedSettings();
          }}
        />
      )}
      <Modal
        title={
          operation
            ? `${operation.action === 'install' ? '安装' : operation.action === 'update' ? '更新' : operation.action === 'switch' ? '切换版本' : '卸载'} ${operation.app.name}${operation.version ? ` ${operation.version}` : ''}`
            : ''
        }
        open={!!operation}
        onCancel={() => {
          if (!working) setOperation(undefined);
        }}
        onOk={() => void perform()}
        confirmLoading={working}
        okText={
          operation?.action === 'uninstall'
            ? '确认卸载'
            : operation?.action === 'update' || operation?.action === 'switch'
              ? '构建并切换'
              : '立即安装'
        }
        okButtonProps={{
          danger: operation?.action === 'uninstall',
          disabled:
            operation?.action !== 'uninstall' &&
            (!sourceReady ||
              sources.isFetching ||
              !releaseList.some((item) => !item.unavailableReason) ||
              versions.isFetching ||
              ((operation?.action === 'update' ||
                operation?.action === 'switch') &&
                !(operation.action === 'switch'
                  ? switchTargets.length
                  : upgradeTarget))),
        }}
        destroyOnHidden
      >
        {operation && (
          <ApplicationSettingsForm
            form={form}

            colon={false}
            initialValues={{ source: 'official', mirror: '' }}
          >
            {operation.action !== 'uninstall' ? (
              <>
                {operation.newInstance && (
                  <Form.Item label="实例名称" name="instanceName">
                    <Input maxLength={100} placeholder="留空则自动命名" />
                  </Form.Item>
                )}
                <Form.Item
                  label="软件版本"
                  name="version"
                  rules={[{ required: true, message: '请选择要安装的版本' }]}
                >
                  <Select
                    showSearch
                    onChange={() => {
                      manualVersions.current.version = true;
                    }}
                    disabled={
                      operation.action === 'update' ||
                      operation.action === 'switch'
                    }
                    loading={versions.isFetching}
                    placeholder="请选择版本"
                    options={installVersionOptions.map((r) => ({
                      value: r.version,
                      disabled: !!r.unavailableReason,
                      label: containerInstall
                        ? [
                            r.version,
                            r.unavailableReason ||
                              r.platform ||
                              (r.source === '已有镜像' ? r.source : undefined),
                          ]
                            .filter(Boolean)
                            .join(' · ')
                        : `${r.version} · ${r.version === operation.app.version ? '当前已安装' : r.source}`,
                    }))}
                  />
                </Form.Item>
                {(operation.action === 'update' ||
                  operation.action === 'switch') && (
                  <Form.Item
                    name="targetVersion"
                    label="更新目标"
                    rules={[{ required: true }]}
                  >
                    <Select
                      onChange={() => {
                        manualVersions.current.target = true;
                      }}
                      options={
                        operation.action === 'switch'
                          ? switchTargets.map((r) => ({
                              value: r.version,
                              label: r.version,
                            }))
                          : upgradeTarget
                            ? [
                                {
                                  value: upgradeTarget.version,
                                  label: upgradeTarget.version,
                                },
                              ]
                            : []
                      }
                      placeholder="当前源未发现同系列较新版本"
                    />
                  </Form.Item>
                )}
                <Form.Item
                  label="下载源"
                  name="source"
                  rules={[{ required: true }]}
                >
                  <Select
                    loading={sources.isFetching}
                    options={sources.data?.list}
                    onChange={(value) => {
                      if (!containerInstall) resetVersions();
                      else if (
                        value === 'custom' &&
                        !form.getFieldValue('mirror')
                      )
                        form.setFieldValue(
                          'mirror',
                          sources.data?.mirrors?.[0] ?? '',
                        );
                    }}
                  />
                </Form.Item>
                {source === 'custom' && (
                  <Form.Item
                    label={containerInstall ? '加速地址' : '下载目录'}
                    name="mirror"
                    rules={[
                      { required: true },
                      {
                        pattern: /^https:\/\//,
                        message: '请输入 HTTPS 地址',
                      },
                    ]}
                    extra={
                      containerInstall
                        ? '可选择 Docker 设置中保存的加速地址，也可填写其他 Docker Hub 镜像加速地址。'
                        : operation.app.id === 'mariadb'
                          ? '目录须包含 mariadb-版本/source/ 子目录。'
                          : '目录须可列出对应软件源码包或二进制包。'
                    }
                  >
                    {containerInstall ? (
                      <AutoComplete
                        style={{ width: '100%' }}
                        options={sources.data?.mirrors?.map((address) => ({
                          value: address,
                        }))}
                        placeholder="https://mirror.example.com"
                      />
                    ) : (
                      <Input
                        placeholder="https://mirror.example.com/releases/"
                        onChange={() => {
                          setConfirmedMirror('');
                          resetVersions();
                        }}
                      />
                    )}
                  </Form.Item>
                )}
                {source === 'registry' && (
                  <Form.Item
                    label="仓库地址"
                    name="registry"
                    rules={[{ required: true, whitespace: true }]}
                    extra="可带端口和路径前缀，仓库中需有对应版本的镜像。"
                  >
                    <AutoComplete
                      style={{ width: '100%' }}
                      options={registryList.data
                        ?.filter((item) => !item.hasPassword)
                        .map((item) => ({ value: item.address }))}
                      placeholder="registry.example.com:5000"
                    />
                  </Form.Item>
                )}
                {source === 'custom' && !containerInstall && (
                  <Form.Item className="application-settings-actions">
                    <Button
                      onClick={() =>
                        void form
                          .validateFields(['source', 'mirror'])
                          .then((values) =>
                            setConfirmedMirror(values.mirror.trim()),
                          )
                      }
                    >
                      获取版本
                    </Button>
                  </Form.Item>
                )}
                {sources.error && (
                  <Alert
                    type="error"
                    message={sources.error.message}
                    action={
                      <Button onClick={() => void sources.refetch()}>
                        重试
                      </Button>
                    }
                  />
                )}
                {versions.data?.notice && (
                  <Alert type="warning" message={versions.data.notice} />
                )}
                {!containerInstall && (
                  <Form.Item label="版本目录">
                    <Button
                      size="small"
                      loading={versions.isFetching}
                      disabled={!sourceReady}
                      onClick={() =>
                        void applicationVersions(operation.app.id, true, {
                          source,
                          mirror:
                            source === 'custom' ? mirror?.trim() : undefined,
                        })
                          .then((data) => client.setQueryData(versionKey, data))
                          .catch((e) =>
                            message.error(
                              e instanceof Error
                                ? e.message
                                : '刷新版本目录失败',
                            ),
                          )
                      }
                    >
                      刷新版本目录
                    </Button>
                  </Form.Item>
                )}
                {versions.error && (
                  <Alert type="error" message={versions.error.message} />
                )}
                {(operation.action === 'update' ||
                  operation.action === 'switch') && (
                  <Alert
                    type="info"
                    message={
                      operation.app.id.startsWith('php-')
                        ? '先验证新版 PHP-FPM 与扩展，再切换流量。验证失败时旧版继续提供服务。'
                        : operation.app.category === 'database' &&
                            operation.app.id !== 'memcached'
                          ? '升级需要维护窗口。旧实例停机后会验证隔离的数据副本，并保留原数据备份。'
                          : '后台准备新版本，切换时服务可能短暂停止。'
                    }
                  />
                )}
              </>
            ) : (
              <>
                <Alert
                  type="warning"
                  message={
                    operation.environmentId
                      ? '将移除该运行环境，保留 Docker 镜像和其他环境。'
                      : operation.app.id === 'rsync'
                        ? '卸载会停止接收服务并保留任务和配置。若系统依赖 Rsync，则停用接收服务并保留系统软件包。'
                        : '卸载会停止服务，配置、网站和数据库数据保留。'
                  }
                  style={{ marginBottom: SPACING.lg }}
                />
                <Form.Item
                  name="confirmName"
                  label="软件名称"
                  extra={`输入「${operation.app.name}」确认卸载`}
                  rules={[
                    { required: true },
                    {
                      validator: (_, value) =>
                        value === operation.app.name
                          ? Promise.resolve()
                          : Promise.reject(new Error('软件名称必须精确匹配')),
                    },
                  ]}
                >
                  <Input autoComplete="off" />
                </Form.Item>
              </>
            )}
          </ApplicationSettingsForm>
        )}
      </Modal>
    </Space>
  );
}
