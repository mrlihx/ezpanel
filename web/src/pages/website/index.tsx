import { useEffect, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Card,
  Checkbox,
  Popover,
  Empty,
  Flex,
  Input,
  Modal,
  Skeleton,
  Space,
  Tooltip,
  Typography,
  type TableColumnsType,
  Grid,
} from 'antd';
import { Plus, RotateCw, Settings } from 'lucide-react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from 'react-router-dom';
import ContentSizedTable from '../../components/ContentSizedTable';
import ApplicationInstallGuide from '../../components/ApplicationInstallGuide';
import { request } from '../../api/client';
import ResourceActions, {
  useResourceActions,
} from '../../plugins/ResourceActions';
import {
  deleteWebsite,
  environment,
  website,
  websiteColumns,
  saveWebsiteColumns,
  saveWebsite,
  websites,
  type SiteInput,
  type Website,
} from '../../api/website';
import SectionTabs from '../../components/SectionTabs';
import TaskDrawer from '../../components/TaskDrawer';
import BackupsModal from '../../components/BackupsModal';
import InlineRemark from '../../components/InlineRemark';
import WebsiteStatus from './WebsiteStatus';
import { LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';
import type { SiteDatabaseInput } from './SiteForm';
import { PhpAddModal, PhpSettingsModal } from './PhpProjectDialogs';
import { GoAddModal, GoSettingsModal } from './GoProjectDialogs';
import { NodeAddModal, NodeSettingsModal } from './NodeProjectDialogs';
import { PythonAddModal, PythonSettingsModal } from './PythonProjectDialogs';
import { ProxyAddModal, ProxySettingsModal } from './ProxyProjectDialogs';
import WebsiteAdvancedModal from './WebsiteAdvancedModal';
import ApplicationServiceControl from '../apps/ApplicationServiceControl';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import { formatSize } from '../../utils/format';
import './website.css';
const errorText = (e: unknown) => (e instanceof Error ? e.message : '请求失败');
const projectLabels = {
  php: 'PHP项目',
  proxy: '反向代理',
  go: 'Go项目',
  python: 'Python项目',
  nodejs: 'Node.js项目',
};

const runtimeNames: Partial<Record<Website['projectType'], string>> = {
  go: 'Go',
  python: 'Python',
  nodejs: 'Node.js',
};

const projectDialogs = {
  php: { add: PhpAddModal, settings: PhpSettingsModal },
  go: { add: GoAddModal, settings: GoSettingsModal },
  nodejs: { add: NodeAddModal, settings: NodeSettingsModal },
  python: { add: PythonAddModal, settings: PythonSettingsModal },
  proxy: { add: ProxyAddModal, settings: ProxySettingsModal },
};

export default function WebsitePage() {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const navigate = useNavigate();
  const pluginActions = useResourceActions('sites');
  const location = useLocation();
  const client = useQueryClient();
  const { message, modal } = App.useApp();
  const [searchParams] = useSearchParams();
  const { section, siteId, siteTab } = useParams();
  const route = useRoutedTab(
    'websites',
    '/websites',
    section,
    Object.keys(projectLabels),
    {
      keys: ['projectType'],
      tab: searchParams.get('projectType'),
    },
  );
  const projectType = route.tab as Website['projectType'];
  const addTitle = `添加${projectLabels[projectType]}`;
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [keyword, setKeyword] = useState('');
  const [adding, setAdding] = useState<Website['projectType']>();
  const [openedSettings, setSettings] = useState<{
    id: string;
    projectType: Website['projectType'];
    tab?: string;
  }>();
  const linkedSite = useQuery({
    queryKey: ['website', siteId],
    queryFn: () => website(siteId!),
    enabled: !!siteId && !route.invalid,
  });
  const settings = siteId
    ? {
        id: siteId,
        projectType: linkedSite.data?.projectType ?? projectType,
        tab: siteTab,
      }
    : openedSettings;
  useEffect(() => {
    const id = location.state?.openWebsiteSettings;
    if (typeof id !== 'string' || route.invalid || route.loading) return;
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setSettings({ id, projectType, tab: 'runtime' });
      // 消费跨页打开请求，避免刷新或浏览器返回时再次打开窗口。
      void navigate(location.pathname + location.search, {
        replace: true,
        state: null,
      });
    });
    return () => {
      active = false;
    };
  }, [location, navigate, projectType, route.invalid, route.loading]);
  const AddProjectModal = adding ? projectDialogs[adding].add : undefined;
  const ProjectSettingsModal = settings
    ? projectDialogs[settings.projectType].settings
    : undefined;
  const [deleting, setDeleting] = useState<Website>();
  const [backups, setBackups] = useState<Website>();
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(false);
  const [confirmName, setConfirmName] = useState('');
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const [task, setTask] = useState<{ id: string; title: string }>();
  const env = useQuery({
    queryKey: ['website-env'],
    queryFn: environment,
    staleTime: 60000,
  });
  const columnSettings = useQuery({
    queryKey: ['website-columns'],
    queryFn: websiteColumns,
    staleTime: Infinity,
  });
  const [columnDraft, setColumnDraft] = useState<string[]>();
  const [columnsOpen, setColumnsOpen] = useState(false);
  const [columnsSaving, setColumnsSaving] = useState(false);
  const columnOptions = [
    { value: 'name', label: '网站名' },
    { value: 'status', label: '状态' },
    { value: 'backups', label: '备份' },
    { value: 'root', label: '根目录' },
    { value: 'traffic', label: '日流量' },
    { value: 'remark', label: '备注' },
    { value: 'php', label: '运行环境' },
    { value: 'ssl', label: 'SSL证书' },
    { value: 'actions', label: '操作', disabled: true },
  ];
  const visible =
    columnSettings.data?.columns ?? columnOptions.map((item) => item.value);
  const list = useQuery({
    queryKey: ['websites', page, size, keyword, projectType],
    queryFn: ({ signal }) => websites(page, size, keyword, signal, projectType),
    enabled: !!env.data?.installed && !route.invalid && !route.loading,
  });
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['backups'] });
    void client.invalidateQueries({ queryKey: ['security-firewall'] });
    void client.invalidateQueries({ queryKey: ['software-catalog'] });
    void client.invalidateQueries({ queryKey: ['runtime-environments'] });
    void env.refetch();
    if (env.data?.installed) void list.refetch();
  };
  const install = () => {
    if (env.data?.activeTaskId)
      setTask({ id: env.data.activeTaskId, title: '安装 OpenResty' });
    else navigate('/apps?install=openresty');
  };
  const create = async (values: SiteInput, database?: SiteDatabaseInput) => {
    setPending(true);
    setError('');
    let ports = '';
    let notice = '';
    try {
      const site = await saveWebsite(undefined, values);
      ports = site.firewallPorts?.join('、') ?? '';
      notice = site.enabled
        ? '站点已添加'
        : '项目已添加，请在运行设置中保存启动配置';
    } catch (e) {
      setError(errorText(e));
      setPending(false);
      return;
    }
    // 站点已提交，后续数据库失败按部分成功处理，避免重新创建同一站点。
    if (database) {
      const { serverId, ...data } = database;
      try {
        await request(
          `/api/db/servers/${encodeURIComponent(serverId)}/databases`,
          {
            method: 'POST',
            data,
            silentError: true,
          },
        );
        void message.success(
          ports
            ? `站点和数据库已创建，已配置 TCP ${ports} 放行规则`
            : '站点和数据库已创建',
        );
      } catch (e) {
        modal.warning({
          title: '站点已创建，数据库创建失败',
          content: `${errorText(e)}。站点已保留，请在数据库页面继续创建，无需重新添加站点。`,
          okText: '知道了',
        });
      }
    } else {
      void message.success(
        ports ? `${notice}，已配置 TCP ${ports} 放行规则` : notice,
      );
    }
    setAdding(undefined);
    setPending(false);
    refresh();
  };
  const remove = async () => {
    if (!deleting) return;
    setPending(true);
    setError('');
    try {
      const deletedId = deleting.id;
      const result = await deleteWebsite(deletedId, deleteFiles, confirmName);
      setDeleting(undefined);
      client.setQueryData<{ list: Website[]; total: number }>(
        ['websites', page, size, keyword, projectType],
        (old) =>
          old
            ? {
                ...old,
                list: old.list.filter((item) => item.id !== deletedId),
                total: Math.max(0, old.total - 1),
              }
            : old,
      );
      if (result.taskId)
        setTask({ id: result.taskId, title: '删除站点与目录' });
      else {
        void message.success('站点配置已删除，目录已保留');
        void client.invalidateQueries({ queryKey: ['software-catalog'] });
        void client.invalidateQueries({ queryKey: ['runtime-environments'] });
      }
    } catch (e) {
      setError(errorText(e));
    } finally {
      setPending(false);
    }
  };
  const columns: TableColumnsType<Website> = [
    {
      key: 'name',
      title: '网站名',
      dataIndex: 'name',
      className: 'website-list-name',
      ellipsis: true,
      render: (_, row) => (
        <Tooltip title={row.domains.join('、')}>
          <Button
            type="link"
            style={{
              padding: 0,
              maxWidth: '100%',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
            }}
            onClick={() =>
              setSettings({ id: row.id, projectType: row.projectType })
            }
          >
            {row.name}
          </Button>
        </Tooltip>
      ),
    },
    {
      key: 'status',
      title: '状态',
      render: (_, row) => (
        <WebsiteStatus
          site={row}
          manageable={!!env.data?.manageable}
          onSettings={() =>
            setSettings({
              id: row.id,
              projectType: row.projectType,
              tab: 'runtime',
            })
          }
          onChanged={refresh}
        />
      ),
    },
    {
      key: 'backups',
      title: '备份',
      render: (_, row) => (
        <Button
          type="link"
          style={{ padding: 0 }}
          onClick={() => setBackups(row)}
        >
          {row.backupCount > 0 ? `${row.backupCount}份` : '无备份'}
        </Button>
      ),
    },
    {
      key: 'root',
      title: '根目录',
      className: 'website-list-root',
      ellipsis: true,
      render: (_, row) =>
        row.root ? (
          <Tooltip title={row.root}>
            <Typography.Link
              onClick={() =>
                navigate(`/files?path=${encodeURIComponent(row.root)}`)
              }
            >
              {row.root}
            </Typography.Link>
          </Tooltip>
        ) : (
          <Tooltip
            title={row.proxyRoutes
              .map((rule) => `${rule.path} → ${rule.target}`)
              .join('\n')}
          >
            —
          </Tooltip>
        ),
    },
    {
      key: 'traffic',
      title: '日流量',
      render: (_, row) => (
        <Tooltip
          title={
            row.trafficNotice ||
            '按面板时区统计现存访问日志中的当日响应体字节，每分钟更新缓存'
          }
        >
          {formatSize(row.dailyTraffic)}
        </Tooltip>
      ),
    },
    {
      key: 'remark',
      title: '备注',
      dataIndex: 'remark',
      className: 'website-list-remark',
      render: (_, row) => (
        <InlineRemark
          value={row.remark}
          label={`编辑${row.name}备注`}
          maxLength={512}
          onSave={async (remark) => {
            await request(
              `/api/websites/${encodeURIComponent(row.id)}/remark`,
              {
                method: 'PUT',
                data: { remark },
              },
            );
            await client.invalidateQueries({ queryKey: ['websites'] });
          }}
        />
      ),
    },
    {
      key: 'php',
      title: '运行环境',
      render: (_, row) => (
        <Typography.Link
          onClick={() =>
            setSettings({
              id: row.id,
              projectType: row.projectType,
              tab: row.runtimeVersion
                ? 'runtime'
                : row.type === 'proxy'
                  ? 'proxy'
                  : 'php',
            })
          }
        >
          {runtimeNames[row.projectType]
            ? row.runtimeVersion
              ? `${runtimeNames[row.projectType]} ${row.runtimeVersion}`
              : '—'
            : row.type === 'proxy'
              ? projectLabels[row.projectType]
              : row.phpVersion || '纯静态'}
        </Typography.Link>
      ),
    },
    {
      key: 'ssl',
      title: 'SSL证书',
      render: (_, row) => (
        <Tooltip
          title={
            row.sslExpireAt
              ? `到期：${new Date(row.sslExpireAt).toLocaleString()}`
              : ''
          }
        >
          <Typography.Link
            onClick={() =>
              setSettings({
                id: row.id,
                projectType: row.projectType,
                tab: 'ssl',
              })
            }
          >
            {row.sslEnabled ? '已部署' : '未部署'}
          </Typography.Link>
        </Tooltip>
      ),
    },
    {
      key: 'actions',
      title: '操作',
      fixed: fixedActions,
      render: (_, row) => (
        <Space size="small">
          <Button
            type="link"
            style={{ padding: 0 }}
            onClick={() =>
              setSettings({ id: row.id, projectType: row.projectType })
            }
          >
            设置
          </Button>
          <Typography.Text type="secondary">|</Typography.Text>
          <Button
            type="link"
            danger
            style={{ padding: 0 }}
            disabled={!env.data?.manageable}
            onClick={() => {
              setDeleting(row);
              setDeleteFiles(false);
              setConfirmName('');
              setError('');
            }}
          >
            删除
          </Button>
          <ResourceActions items={pluginActions(row.id, row.name)} />
        </Space>
      ),
    },
  ];
  const errorAlert = error && (
    <Alert
      type="error"
      showIcon
      closable
      onClose={() => setError('')}
      message="操作失败"
      description={
        <pre
          style={{
            fontFamily: MONO_FONT,
            whiteSpace: 'pre-wrap',
            maxHeight: LAYOUT.errorMaxHeight,
            overflow: 'auto',
          }}
        >
          {error}
        </pre>
      }
      style={{ marginBottom: SPACING.lg }}
    />
  );
  if (route.invalid) return <RouteNotFound back="/websites" />;
  if (route.loading) return <Skeleton active />;
  return (
    <>
      <SectionTabs
        activeKey={projectType}
        style={{ marginBottom: SPACING.lg }}
        onChange={(key) => {
          setSettings(undefined);
          void route.selectTab(key);
          setPage(1);
        }}
        items={Object.entries(projectLabels).map(([key, label]) => ({
          key,
          label,
        }))}
      />
      {env.isError && env.data && (
        <Alert
          type="warning"
          showIcon
          message="环境状态刷新失败，正在显示上次检测结果"
          description={errorText(env.error)}
          style={{ marginBottom: SPACING.lg }}
        />
      )}
      {env.isPending ? (
        <Skeleton active />
      ) : env.isError && !env.data ? (
        <Alert
          type="error"
          showIcon
          message="OpenResty 环境检测失败"
          description={errorText(env.error)}
          action={<Button onClick={() => void env.refetch()}>重新检测</Button>}
        />
      ) : !env.data?.installed ? (
        <ApplicationInstallGuide
          name="OpenResty"
          install={install}
          onDetect={() => void env.refetch()}
          installText={env.data?.activeTaskId ? '查看安装进度' : undefined}
        />
      ) : (
        <>
          {!env.data.manageable && (
            <Alert
              type="warning"
              showIcon
              message="当前 OpenResty 不可受控管理"
              description="检测到非本商店管理的安装或未知服务，请手工管理；面板不会覆盖现有安装。"
              style={{ marginBottom: SPACING.lg }}
            />
          )}
          {env.data.activeTaskId && (
            <Alert
              type="info"
              message="OpenResty 安装任务正在进行"
              action={
                <Button
                  onClick={() =>
                    setTask({
                      id: env.data.activeTaskId!,
                      title: '安装 OpenResty',
                    })
                  }
                >
                  查看进度
                </Button>
              }
              style={{ marginBottom: SPACING.lg }}
            />
          )}
          {list.isError && (
            <Alert
              type="error"
              message="站点列表刷新失败"
              description={errorText(list.error)}
              action={<Button onClick={() => void list.refetch()}>重试</Button>}
              style={{ marginBottom: SPACING.lg }}
            />
          )}
          {!adding && !deleting && errorAlert}
          <Card>
            <Flex
              justify="space-between"
              gap={SPACING.md}
              wrap
              style={{ marginBottom: SPACING.lg }}
            >
              <Space wrap>
                <Button
                  type="primary"
                  icon={<Plus size={16} strokeWidth={2} />}
                  disabled={!env.data.manageable}
                  onClick={() => {
                    setError('');
                    setAdding(projectType);
                  }}
                >
                  {addTitle}
                </Button>
                {projectType === 'php' && (
                  <Button
                    icon={<Settings size={16} strokeWidth={1.9} />}
                    disabled={!env.data.manageable}
                    onClick={() => setAdvancedOpen(true)}
                  >
                    高级设置
                  </Button>
                )}
                <ApplicationServiceControl
                  app={env.data.application}
                  disabled={!!env.data.activeTaskId}
                  onUninstall={() => navigate('/apps')}
                  onChanged={refresh}
                />
              </Space>
              <Space wrap>
                <Input.Search
                  placeholder="搜索名称或域名"
                  allowClear
                  onSearch={(value) => {
                    setKeyword(value);
                    setPage(1);
                  }}
                  style={{ width: LAYOUT.appSearchWidth, maxWidth: '100%' }}
                />
                <Button
                  aria-label="刷新站点"
                  icon={<RotateCw size={16} strokeWidth={1.9} />}
                  loading={list.isFetching}
                  onClick={refresh}
                />
                <Popover
                  trigger="click"
                  placement="bottomRight"
                  open={columnsOpen}
                  onOpenChange={(value) => {
                    setColumnsOpen(value);
                    if (value) setColumnDraft(visible);
                  }}
                  content={
                    <Space direction="vertical" style={{ width: 180 }}>
                      <Checkbox.Group
                        options={columnOptions}
                        value={columnDraft ?? visible}
                        onChange={(values) =>
                          setColumnDraft([
                            ...new Set([...(values as string[]), 'actions']),
                          ])
                        }
                        style={{
                          display: 'flex',
                          flexDirection: 'column',
                          gap: SPACING.sm,
                        }}
                      />
                      {columnSettings.isError && (
                        <Alert
                          type="error"
                          message={columnSettings.error.message}
                        />
                      )}
                      <Space>
                        <Button
                          size="small"
                          onClick={() =>
                            setColumnDraft(
                              columnOptions.map((item) => item.value),
                            )
                          }
                        >
                          全选
                        </Button>
                        <Button
                          size="small"
                          type="primary"
                          loading={columnsSaving}
                          onClick={async () => {
                            setColumnsSaving(true);
                            try {
                              const result = await saveWebsiteColumns([
                                ...new Set([
                                  ...(columnDraft ?? visible),
                                  'actions',
                                ]),
                              ]);
                              client.setQueryData(['website-columns'], result);
                              setColumnsOpen(false);
                            } catch (e) {
                              setError(errorText(e));
                            } finally {
                              setColumnsSaving(false);
                            }
                          }}
                        >
                          应用
                        </Button>
                      </Space>
                    </Space>
                  }
                >
                  <Button
                    aria-label="显示列"
                    title="显示列"
                    icon={<Settings size={16} strokeWidth={1.9} />}
                  />
                </Popover>
              </Space>
            </Flex>
            <ContentSizedTable<Website>
              fillColumn={
                projectType === 'proxy'
                  ? ['name', 'remark']
                  : ['root', 'name', 'remark']
              }
              growColumns={['name', 'remark']}
              rowKey="id"
              loading={list.isPending}
              dataSource={list.data?.list ?? []}
              locale={{
                emptyText: list.isError ? (
                  '列表加载失败，请重试'
                ) : (
                  <Empty description="尚未添加站点" />
                ),
              }}
              pagination={{
                current: page,
                pageSize: size,
                total: list.data?.total ?? 0,
                showSizeChanger: true,
                onChange: (next, count) => {
                  setPage(next);
                  setSize(count);
                },
              }}
              columns={columns.filter(
                (column) =>
                  column.key === 'actions' ||
                  visible.includes(String(column.key)),
              )}
            />
          </Card>
        </>
      )}
      {AddProjectModal && (
        <AddProjectModal
          pending={pending}
          error={errorAlert}
          onClose={() => {
            setAdding(undefined);
            setError('');
          }}
          onCreate={(values, database) => void create(values, database)}
        />
      )}
      <Modal
        title="删除站点"
        open={!!deleting}
        onCancel={() => {
          if (!pending) setDeleting(undefined);
        }}
        closable={!pending}
        keyboard={!pending}
        maskClosable={false}
        cancelButtonProps={{ disabled: pending }}
        onOk={() => void remove()}
        okButtonProps={{
          danger: true,
          disabled: confirmName !== deleting?.name,
        }}
        confirmLoading={pending}
        destroyOnHidden
      >
        {errorAlert}
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <Typography.Text>
            删除「{deleting?.name}」的 OpenResty 配置及证书。
          </Typography.Text>
          <Checkbox
            checked={deleteFiles}
            disabled={!deleting?.canDeleteFiles}
            onChange={(event) => setDeleteFiles(event.target.checked)}
          >
            删除站点目录
          </Checkbox>
          {!deleting?.canDeleteFiles && (
            <Typography.Text type="secondary">
              {deleting?.deleteFilesDisabledReason}
            </Typography.Text>
          )}
          {deleteFiles && (
            <Alert
              type="warning"
              message="目录会永久删除。配置提交后若目录清理失败，站点仍保持已删除。"
            />
          )}
          <Typography.Text>
            输入网站名「{deleting?.name}」确认删除
            {deleteFiles ? '站点及目录' : '站点配置（保留目录）'}。
          </Typography.Text>
          <Input
            aria-label="确认网站名"
            placeholder="输入完整网站名确认"
            value={confirmName}
            onChange={(event) => setConfirmName(event.target.value)}
          />
        </Space>
      </Modal>
      {settings && ProjectSettingsModal && (
        <ProjectSettingsModal
          key={`${settings.id}:${settings.tab ?? ''}`}
          id={settings.id}
          initialTab={settings.tab}
          manageable={!!env.data?.manageable}
          onClose={() => {
            setSettings(undefined);
            if (siteId) {
              void navigate(
                {
                  pathname: `/websites/${projectType}`,
                  search: location.search,
                },
                { replace: true },
              );
            }
          }}
          onChanged={refresh}
        />
      )}
      {backups && (
        <BackupsModal
          key={backups.id}
          title={`[${backups.name}] 网站备份`}
          scope={{ kind: 'site', websiteId: backups.id }}
          allowCreate={!!backups.root}
          createDisabledReason="反向代理站点没有本地网站目录可备份"
          onClose={() => setBackups(undefined)}
          onStarted={(id) => setTask({ id, title: '备份网站' })}
        />
      )}
      {advancedOpen && (
        <WebsiteAdvancedModal onClose={() => setAdvancedOpen(false)} />
      )}
      {task && (
        <TaskDrawer
          key={task.id}
          taskId={task.id}
          title={task.title}
          open
          onClose={() => setTask(undefined)}
          onSettled={refresh}
          onRetry={
            task.title === '安装 OpenResty'
              ? () => {
                  setTask(undefined);
                  install();
                }
              : undefined
          }
        />
      )}
    </>
  );
}
