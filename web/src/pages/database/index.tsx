import { useState, type Key } from 'react';
import {
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
  Navigate,
} from 'react-router-dom';
import {
  Alert,
  App,
  Button,
  Card,
  Descriptions,
  Empty,
  Checkbox,
  Popover,
  Tooltip,
  type TableColumnsType,
  Form,
  Flex,
  Input,
  Modal,
  Select,
  Skeleton,
  Space,
  Tag,
  Typography,
  Grid,
} from 'antd';
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { RotateCw, Settings, Trash2 } from 'lucide-react';
import ContentSizedTable from '../../components/ContentSizedTable';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import SectionTabs from '../../components/SectionTabs';
import ApplicationServiceControl from '../apps/ApplicationServiceControl';
import RootPasswordModal from './RootPasswordModal';
import BackupsModal from '../../components/BackupsModal';
import RemoteDatabases from './RemoteDatabases';
import DeleteDatabaseModal from './DeleteDatabaseModal';
import EnvironmentOverlay from './EnvironmentOverlay';
import TrashModal from '../file/TrashModal';
import { request } from '../../api/client';
import {
  databaseList,
  environment,
  servers,
  userList,
  type Database,
  type DBUser,
  type Server,
} from '../../api/database';
import TaskDrawer from '../../components/TaskDrawer';
import { LAYOUT, SPACING } from '../../styles/tokens';
import ServerForm from './ServerForm';
import NoSQL from './NoSQL';
import PostgresPage from './PostgresPage';
import SqlitePage from './SqlitePage';
import DatabaseInstanceControl from './DatabaseInstanceControl';
import { useDatabaseSelection } from './useDatabaseSelection';
import DatabasePassword from './DatabasePassword';
import InlineRemark from '../../components/InlineRemark';
import CreateDatabaseModal from './CreateDatabaseModal';
import DatabaseAccessModal from './DatabaseAccessModal';
import DatabasePasswordModal from './DatabasePasswordModal';
import DatabaseImportModal from './DatabaseImportModal';
import PhpMyAdminButton from './PhpMyAdminButton';
import DatabaseBatchModal, {
  type DatabaseBatchAction,
} from './DatabaseBatchModal';
type Operation = { kind: 'database' | 'user' | 'password'; user?: DBUser };
export default function DatabasePage() {
  const { engine: requested, section } = useParams();
  const route = useRoutedTab(
    'databases',
    '/databases',
    requested,
    ['mysql', 'postgresql', 'mongodb', 'redis', 'sqlite'],
    { keys: ['instance'] },
  );
  const engine = route.tab;
  if (requested === 'memcached')
    return <Navigate to="/apps/database" replace />;
  if (
    route.invalid ||
    (section && (engine !== 'mysql' || !['users', 'backups'].includes(section)))
  )
    return <RouteNotFound back="/databases" />;
  if (route.loading) return <Skeleton active />;
  return (
    <SectionTabs
      activeKey={engine}
      onChange={route.selectTab}
      destroyOnHidden
      items={[
        { key: 'mysql', label: 'MySQL', children: <MySQLPage /> },
        {
          key: 'postgresql',
          label: 'PostgreSQL',
          children: <PostgresPage />,
        },
        {
          key: 'mongodb',
          label: 'MongoDB',
          children: <NoSQL engine="mongodb" />,
        },
        { key: 'redis', label: 'Redis', children: <NoSQL engine="redis" /> },
        { key: 'sqlite', label: 'SQLite', children: <SqlitePage /> },
      ]}
    />
  );
}

function MySQLPage() {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { message, modal } = App.useApp();
  const [keyword, setKeyword] = useState('');
  const [selectedDBs, setSelectedDBs] = useState<Key[]>([]);
  const [batchAction, setBatchAction] = useState<
    DatabaseBatchAction | 'backup'
  >('access');
  const [batchModal, setBatchModal] = useState<DatabaseBatchAction>();
  const [serverForm, setServerForm] = useState<{
    server?: Server;
  }>();
  const [credentials, setCredentials] = useState(false);
  const client = useQueryClient();
  const [remoteOpen, setRemoteOpen] = useState(false);
  const [trashOpen, setTrashOpen] = useState(false);
  const [deleting, setDeleting] = useState<{
    name: string;
    serverId: string;
  }>();
  const [backupServerId, setBackupServerId] = useState<string>();
  const [databaseSettings, setDatabaseSettings] = useState<{
    kind: 'access' | 'password';
    database: Database & { _serverId?: string };
  }>();
  const columnOptions = [
    { value: 'name', label: '数据库名' },
    { value: 'username', label: '用户名' },
    { value: 'password', label: '密码' },
    { value: 'backups', label: '备份' },
    { value: 'location', label: '数据库位置' },
    { value: 'remark', label: '备注' },
    { value: 'actions', label: '操作', disabled: true },
  ];
  const columns = useQuery({
    queryKey: ['database-columns'],
    queryFn: () => request<{ columns: string[] }>('/api/db/columns'),
  });
  const visible =
    columns.data?.columns ?? columnOptions.map((item) => item.value);
  const [columnsSaving, setColumnsSaving] = useState(false);
  const navigate = useNavigate();
  const location = useLocation();
  const { section } = useParams();
  const [params] = useSearchParams();
  const tab = section ?? 'databases';
  const backupFilter =
    tab === 'backups' ? (params.get('database') ?? undefined) : undefined;
  function setTab(next: string, database?: string) {
    const query = new URLSearchParams(location.search);
    if (database) query.set('database', database);
    else query.delete('database');
    const search = query.toString();
    const pathname =
      next === 'databases' ? '/databases/mysql' : `/databases/mysql/${next}`;
    if (
      `${pathname}${search ? `?${search}` : ''}` !==
      location.pathname + location.search
    )
      void navigate({ pathname, search });
  }
  const [importDatabase, setImportDatabase] = useState<{
    name: string;
    serverId: string;
    serverName: string;
  }>();
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [op, setOp] = useState<Operation>();
  const [form] = Form.useForm();
  const [confirmation, setConfirmation] = useState<{
    title: string;
    name: string;
    warning: string;
    run: () => Promise<unknown>;
  }>();
  const [confirmName, setConfirmName] = useState('');
  const [busy, setBusy] = useState(false);
  const [task, setTask] = useState<{ id: string; title: string }>();
  const env = useQuery({
    queryKey: ['db-env'],
    queryFn: environment,
    staleTime: 60000,
  });
  const list = useQuery({ queryKey: ['db-servers'], queryFn: servers });
  const { selected, setSid } = useDatabaseSelection('mysql', list.data);
  const [allMode, setAllMode] = useState(true);
  const mysqlLabel = (s: Server) =>
    s.local && !s.instanceId ? 'MySQL' : s.name.replace(/^本机\s+/, '');
  const localApplication = env.data?.application;
  const dockerInstance =
    selected?.instanceId ??
    (selected?.local &&
    localApplication?.installed &&
    localApplication.pkgManager === 'Docker'
      ? 'default'
      : undefined);
  const id = selected?.id;
  const targetServers = allMode
    ? (list.data ?? []).filter((s) => s.running !== false)
    : selected && selected.running !== false
      ? [selected]
      : [];
  const allDbQueries = useQueries({
    queries: targetServers.map((s) => ({
      queryKey: ['db-databases', s.id],
      queryFn: () => databaseList(s.id),
    })),
  });
  type DatabaseRow = Database & { _serverId?: string; _location?: string };
  const mergedDatabases: DatabaseRow[] = targetServers.flatMap((s, idx) =>
    (allDbQueries[idx]?.data ?? []).map((row) => ({
      ...row,
      _serverId: s.id,
      _location: mysqlLabel(s),
    })),
  );
  const databases = {
    data: mergedDatabases,
    isFetching: allDbQueries.some((q) => q.isFetching),
    error: allDbQueries.find((q) => q.error)?.error ?? null,
    dataUpdatedAt: allDbQueries.reduce(
      (max, q) => Math.max(max, q.dataUpdatedAt),
      0,
    ),
    refetch: () => client.invalidateQueries({ queryKey: ['db-databases'] }),
  };
  const [previousId, setPreviousId] = useState(id);
  if (previousId !== id) {
    setPreviousId(id);
    setDeleting(undefined);
    setDatabaseSettings(undefined);
    setBatchModal(undefined);
    setImportDatabase(undefined);
    setOp(undefined);
    setPage(1);
    setSelectedDBs([]);
  }
  const containerClient = selected?.containerClient ?? false;
  const canBackup = containerClient || !!env.data?.backupClientInstalled;
  const recyclable = containerClient || !!env.data?.clientInstalled;
  const users = useQuery({
    queryKey: ['db-users', id],
    queryFn: () => userList(id!),
    enabled: !!id && selected?.running !== false && tab === 'users',
  });
  const grants = useQuery({
    queryKey: ['db-grant-targets', id],
    queryFn: () => databaseList(id!),
    enabled: !!id && selected?.running !== false && op?.kind === 'user',
  });
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['database-trash'] });
    void list.refetch();
    void env.refetch();
    if (id && selected?.running !== false) void databases.refetch();
    if (id && selected?.running !== false && tab === 'users')
      void users.refetch();
    void client.invalidateQueries({ queryKey: ['backups'] });
  };
  const perform = async (job: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await job();
      refresh();
    } finally {
      setBusy(false);
    }
  };
  const confirm = (
    title: string,
    name: string,
    warning: string,
    run: () => Promise<unknown>,
  ) => {
    setConfirmName('');
    setConfirmation({ title, name, warning, run });
  };
  const submit = async () => {
    const values = await form.validateFields();
    await perform(async () => {
      await request(
        `/api/db/servers/${id}/users${op?.kind === 'password' ? '/password' : ''}`,
        {
          method: op?.kind === 'password' ? 'PUT' : 'POST',
          data: {
            ...values,
            ...(op?.user
              ? { username: op.user.username, host: op.user.host }
              : {}),
          },
        },
      );
      setOp(undefined);
    });
  };
  const openOperation = (operation: Operation) => {
    form.resetFields();
    form.setFieldsValue({
      host: 'localhost',
      privilege: 'readonly',
      ...operation.user,
    });
    setOp(operation);
  };
  const filteredDatabases =
    databases.data?.filter(
      (database) =>
        !database.system &&
        database.name.toLowerCase().includes(keyword.toLowerCase()),
    ) ?? [];
  const pageDatabases = filteredDatabases.slice((page - 1) * size, page * size);
  const selectedDatabases =
    databases.data?.filter(
      (database) => !database.system && selectedDBs.includes(database.name),
    ) ?? [];
  const pageSelected = pageDatabases.filter((database) =>
    selectedDBs.includes(database.name),
  ).length;
  const openBatch = () => {
    if (!selectedDatabases.length) return;
    if (selectedDatabases.length > 100) {
      message.error('每批最多操作100个数据库');
      return;
    }
    if (batchAction !== 'backup') {
      setBatchModal(batchAction);
      return;
    }
    modal.confirm({
      title: `备份选中的 ${selectedDatabases.length} 个数据库？`,
      content: '按顺序创建本地备份，完成后可在各数据库的备份列表查看。',
      onOk: () =>
        perform(async () => {
          const result = await request<{ taskId: string }>(
            `/api/db/servers/${id}/backups/batch`,
            {
              method: 'POST',
              data: {
                databases: selectedDatabases.map((database) => database.name),
              },
            },
          );
          setTask({ id: result.taskId, title: '批量备份数据库' });
          setSelectedDBs([]);
        }),
    });
  };
  const databaseColumns: TableColumnsType<Database> = [
    {
      key: 'name',
      title: '数据库名',
      dataIndex: 'name',
      width: 180,
      ellipsis: true,
    },
    {
      key: 'username',
      title: '用户名',
      dataIndex: 'username',
      width: 150,
      ellipsis: true,
      render: (value: string, row) =>
        value ? (
          <Typography.Text
            title={
              row.localAccess
                ? `${value}（本地服务器）`
                : `${value}@${row.userHost}`
            }
            copyable
          >
            {value}
          </Typography.Text>
        ) : (
          '未记录'
        ),
    },
    {
      key: 'password',
      title: '密码',
      width: 210,
      render: (_, row) => {
        const rowSid = (row as DatabaseRow)._serverId ?? id!;
        return (
          <DatabasePassword
            key={`${rowSid}:${row.name}:${databases.dataUpdatedAt}`}
            serverId={rowSid}
            name={row.name}
            recorded={row.hasPassword}
          />
        );
      },
    },
    {
      key: 'backups',
      title: '备份',
      width: 160,
      render: (_, row) => {
        const rowSid = (row as DatabaseRow)._serverId ?? id;
        const rowSrv =
          list.data?.find((item) => item.id === rowSid) ?? selected;
        return (
          <Space size={SPACING.xs}>
            <Button
              type="link"
              style={{ padding: 0 }}
              disabled={row.system}
              onClick={() => {
                setBackupServerId(rowSid);
                setTab('backups', row.name);
              }}
            >
              {row.backupCount > 0 ? `${row.backupCount}份备份` : '无备份'}
            </Button>
            {!row.system && rowSrv && (
              <>
                <Typography.Text type="secondary" aria-hidden="true">
                  |
                </Typography.Text>
                <Button
                  type="link"
                  style={{ padding: 0 }}
                  onClick={() =>
                    setImportDatabase({
                      name: row.name,
                      serverId: rowSrv.id,
                      serverName: rowSrv.name,
                    })
                  }
                >
                  导入
                </Button>
              </>
            )}
          </Space>
        );
      },
    },
    {
      key: 'location',
      title: '数据库位置',
      width: 190,
      ellipsis: true,
      render: (_, row) =>
        (row as Database & { _location?: string })._location ||
        (selected ? mysqlLabel(selected) : 'MySQL'),
    },
    {
      key: 'remark',
      title: '备注',
      width: 200,
      render: (_, row) => {
        const rowSid = (row as DatabaseRow)._serverId ?? id;
        return row.system ? (
          '系统数据库'
        ) : (
          <InlineRemark
            value={row.remark || ''}
            label={`编辑${row.name}备注`}
            maxLength={512}
            onSave={async (value) => {
              await request(
                `/api/db/servers/${rowSid}/databases/${encodeURIComponent(row.name)}/metadata`,
                {
                  method: 'PUT',
                  data: { remark: value },
                },
              );
              await client.invalidateQueries({
                queryKey: ['db-databases', rowSid],
              });
            }}
          />
        );
      },
    },
    {
      key: 'actions',
      title: '操作',
      width: 220,
      fixed: fixedActions,
      render: (_, row) => {
        const rowSid = (row as DatabaseRow)._serverId ?? id!;
        return (
          <Space size="small">
            <PhpMyAdminButton serverId={rowSid} database={row.name} />
            <Button
              type="link"
              style={{ padding: 0 }}
              disabled={!row.username || row.system}
              onClick={() =>
                setDatabaseSettings({ kind: 'access', database: row })
              }
            >
              权限
            </Button>
            <Button
              type="link"
              style={{ padding: 0 }}
              disabled={!row.username || row.system}
              onClick={() =>
                setDatabaseSettings({ kind: 'password', database: row })
              }
            >
              改密
            </Button>
            <Button
              type="link"
              danger
              style={{ padding: 0 }}
              disabled={row.system}
              onClick={() => setDeleting({ name: row.name, serverId: rowSid })}
            >
              删除
            </Button>
          </Space>
        );
      },
    },
  ];
  const queryError =
    list.error ??
    env.error ??
    (tab === 'users' ? users.error : databases.error);
  return (
    <>
      {queryError && (
        <Alert
          showIcon
          type="error"
          message={
            queryError instanceof Error ? queryError.message : '读取失败'
          }
          action={<Button onClick={refresh}>重试</Button>}
        />
      )}
      <div style={{ position: 'relative', minHeight: 500 }}>
        <Card style={{ marginBottom: SPACING.lg }}>
          <Flex justify="space-between" align="center" gap={SPACING.md} wrap>
            <Space wrap>
              <Button
                type="primary"
                disabled={!id || selected?.running === false}
                onClick={() => {
                  setTab('databases');
                  openOperation({ kind: 'database' });
                }}
              >
                添加数据库
              </Button>
              <Button
                disabled={
                  !id ||
                  selected?.running === false ||
                  ((!selected?.local || !!dockerInstance) &&
                    selected?.username !== 'root')
                }
                onClick={() => setCredentials(true)}
              >
                ROOT密码
              </Button>

              <Button onClick={() => setRemoteOpen(true)}>远程数据库</Button>
              <Button
                icon={<Trash2 size={15} strokeWidth={1.9} />}
                onClick={() => setTrashOpen(true)}
              >
                回收站
              </Button>
              {dockerInstance && selected ? (
                <DatabaseInstanceControl
                  key={id}
                  engine="mysql"
                  instanceId={dockerInstance}
                  name={selected.name}
                  version={
                    selected.version ?? localApplication?.version ?? undefined
                  }
                  running={selected.running ?? !!localApplication?.running}
                  onChanged={refresh}
                />
              ) : (
                (!selected || selected.local) &&
                env.data?.application && (
                  <ApplicationServiceControl
                    app={env.data.application}
                    onChanged={refresh}
                  />
                )
              )}
              {env.data?.activeTaskId && (
                <Button
                  onClick={() =>
                    setTask({
                      id: env.data!.activeTaskId!,
                      title: '数据库安装任务',
                    })
                  }
                >
                  查看安装任务
                </Button>
              )}
            </Space>
            <Space wrap>
              <Select
                placeholder="选择 MySQL 实例"
                style={{ width: LAYOUT.databaseSelectWidth, maxWidth: '100%' }}
                value={allMode ? '__all__' : id}
                options={[
                  { value: '__all__', label: '全部' },
                  ...(list.data?.map((s) => ({
                    value: s.id,
                    label: `${mysqlLabel(s)}${s.running === false ? '（已停止）' : ''}`,
                  })) ?? []),
                ]}
                onChange={(v) => {
                  if (v === '__all__') {
                    setAllMode(true);
                  } else {
                    setAllMode(false);
                    setSid(v);
                  }
                  setDeleting(undefined);
                  setDatabaseSettings(undefined);
                  setBatchModal(undefined);
                  setPage(1);
                  setSelectedDBs([]);
                }}
              />
              <Input.Search
                allowClear
                placeholder="请输入数据库名"
                onSearch={(value) => {
                  setKeyword(value);
                  setPage(1);
                  setSelectedDBs([]);
                }}
                style={{ width: LAYOUT.appSearchWidth, maxWidth: '100%' }}
              />
              <Tooltip title="刷新">
                <Button
                  aria-label="刷新"
                  icon={<RotateCw size={16} strokeWidth={1.9} />}
                  onClick={refresh}
                />
              </Tooltip>
              <Popover
                trigger="click"
                title="显示列"
                content={
                  <Space direction="vertical">
                    {columns.error && (
                      <Alert type="error" message={columns.error.message} />
                    )}
                    <Checkbox.Group
                      options={columnOptions}
                      value={visible}
                      disabled={
                        columnsSaving || columns.isPending || columns.isError
                      }
                      style={{
                        display: 'flex',
                        flexDirection: 'column',
                        gap: 8,
                      }}
                      onChange={async (values) => {
                        setColumnsSaving(true);
                        try {
                          const result = await request<{ columns: string[] }>(
                            '/api/db/columns',
                            {
                              method: 'PUT',
                              data: {
                                columns: Array.from(
                                  new Set([...values, 'actions']),
                                ),
                              },
                            },
                          );
                          client.setQueryData(['database-columns'], result);
                        } finally {
                          setColumnsSaving(false);
                        }
                      }}
                    />
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
        </Card>
        {!allMode && selected?.running === false ? (
          <Card>
            <Empty description="所选实例已停止" />
          </Card>
        ) : !selected && !allMode ? (
          <Card>
            <Empty
              description={
                env.data?.localInstalled
                  ? '正在识别本机数据库，若加载失败请刷新重试'
                  : '未检测到本机数据库，可安装MySQL或添加远程数据库'
              }
            >
              <Button type="primary" onClick={() => setServerForm({})}>
                添加远程数据库
              </Button>
            </Empty>
          </Card>
        ) : (
          <Card>
            <>
              <ContentSizedTable<Database>
                fillColumn={[
                  'name',
                  '数据库名',
                  'location',
                  '数据库位置',
                  'username',
                  'remark',
                ]}
                growColumns={['username', 'remark']}
                rowKey={(row) =>
                  `${(row as Database & { _serverId?: string })._serverId || id}:${row.name}`
                }
                rowSelection={{
                  selectedRowKeys: selectedDBs,
                  onChange: setSelectedDBs,
                  getCheckboxProps: (row) => ({ disabled: row.system }),
                }}
                pagination={{
                  current: page,
                  pageSize: size,
                  showSizeChanger: true,
                  onChange: (p, s) => {
                    setPage(p);
                    setSize(s);
                  },
                }}
                loading={databases.isFetching}
                dataSource={filteredDatabases}
                footer={() => (
                  <Flex align="center" gap={SPACING.md} wrap>
                    <Checkbox
                      checked={
                        pageDatabases.length > 0 &&
                        pageSelected === pageDatabases.length
                      }
                      indeterminate={
                        pageSelected > 0 && pageSelected < pageDatabases.length
                      }
                      disabled={!pageDatabases.length || busy}
                      onChange={(event) => {
                        const names = pageDatabases.map(
                          (database) => database.name,
                        );
                        setSelectedDBs(
                          event.target.checked
                            ? Array.from(new Set([...selectedDBs, ...names]))
                            : selectedDBs.filter(
                                (key) => !names.includes(String(key)),
                              ),
                        );
                      }}
                    >
                      全选
                    </Checkbox>
                    <Select
                      aria-label="批量操作类型"
                      value={batchAction}
                      style={{ width: 144, maxWidth: '100%' }}
                      onChange={setBatchAction}
                      options={[
                        { value: 'access', label: '设置权限' },
                        { value: 'password', label: '修改密码' },
                        {
                          value: 'backup',
                          label: '备份数据库',
                          disabled: !canBackup,
                        },
                        { value: 'delete', label: '删除数据库' },
                      ]}
                    />
                    <Button
                      disabled={
                        !selectedDatabases.length ||
                        busy ||
                        (batchAction === 'backup' && !canBackup)
                      }
                      onClick={openBatch}
                    >
                      批量操作（已选{selectedDatabases.length}项）
                    </Button>
                  </Flex>
                )}
                columns={databaseColumns.filter(
                  (column) =>
                    column.key === 'actions' ||
                    visible.includes(String(column.key)),
                )}
              />
            </>
          </Card>
        )}
        {!env.isPending &&
          !list.isPending &&
          !env.isError &&
          !list.isError &&
          !env.data?.localInstalled &&
          !selected && (
            <EnvironmentOverlay
              engine="mysql"
              label="MySQL"
              onRemote={() => setRemoteOpen(true)}
            />
          )}
      </div>
      <Modal
        open={tab === 'users' && !!id}
        title="数据库用户"
        width={LAYOUT.trashModalWidth}
        onCancel={() => setTab('databases')}
        footer={null}
      >
        <>
          <Button
            type="primary"
            onClick={() => openOperation({ kind: 'user' })}
          >
            创建用户
          </Button>
          <ContentSizedTable<DBUser>
            fillColumn="username"
            rowKey={(u) => `${u.username}@${u.host}`}
            pagination={false}
            dataSource={users.data}
            loading={users.isFetching}
            columns={[
              { title: '用户名', dataIndex: 'username' },
              { title: '主机', dataIndex: 'host' },
              {
                title: '授权',
                render: (_, u) => (
                  <Button
                    type="link"
                    onClick={() =>
                      modal.info({
                        title: `${u.username}@${u.host} 的授权`,
                        width: LAYOUT.logDrawerWidth,
                        content: (
                          <Descriptions
                            column={1}
                            items={u.grants.map((g, i) => ({
                              key: i,
                              children: (
                                <Typography.Text code>{g}</Typography.Text>
                              ),
                            }))}
                          />
                        ),
                      })
                    }
                  >
                    查看授权
                  </Button>
                ),
              },
              {
                title: '操作',
                render: (_, u) => (
                  <Space>
                    <Button
                      type="link"
                      disabled={!u.editable}
                      onClick={() =>
                        openOperation({ kind: 'password', user: u })
                      }
                    >
                      改密
                    </Button>
                    <Button
                      type="link"
                      danger
                      disabled={!u.editable}
                      onClick={() =>
                        modal.confirm({
                          title: `删除 ${u.username}@${u.host}？`,
                          onOk: () =>
                            perform(async () => {
                              await request(`/api/db/servers/${id}/users`, {
                                method: 'DELETE',
                                data: {
                                  username: u.username,
                                  host: u.host,
                                },
                              });
                              client.setQueryData<DBUser[]>(
                                ['db-users', id],
                                (rows) =>
                                  rows?.filter(
                                    (item) =>
                                      !(
                                        item.username === u.username &&
                                        item.host === u.host
                                      ),
                                  ),
                              );
                            }),
                        })
                      }
                    >
                      删除
                    </Button>
                    {!u.editable && <Tag>只读账号</Tag>}
                  </Space>
                ),
              },
            ]}
          />
        </>
      </Modal>
      {tab === 'backups' &&
        (backupServerId ?? id) &&
        (() => {
          const targetSid = backupServerId ?? id!;
          const targetSrv =
            list.data?.find((item) => item.id === targetSid) ?? selected;
          const targetCanBackup =
            (targetSrv?.containerClient ?? false) ||
            !!env.data?.backupClientInstalled;
          return (
            <BackupsModal
              key={`${targetSid}:${backupFilter ?? ''}`}
              title={
                backupFilter ? `[${backupFilter}] 数据库备份` : '数据库备份'
              }
              scope={{
                kind: 'database',
                serverId: targetSid,
                database: backupFilter,
              }}
              allowCreate={!!targetSid && !!backupFilter && targetCanBackup}
              createDisabledReason={
                backupFilter
                  ? '请先安装数据库备份客户端'
                  : '请从数据库备份列选择需要手动备份的数据库'
              }
              onClose={() => {
                setBackupServerId(undefined);
                setTab('databases');
              }}
              onStarted={(taskId) =>
                setTask({ id: taskId, title: '备份数据库' })
              }
            />
          );
        })()}
      {importDatabase && (
        <DatabaseImportModal
          serverId={importDatabase.serverId}
          serverName={importDatabase.serverName}
          database={importDatabase.name}
          onClose={() => setImportDatabase(undefined)}
          onTask={(id) => setTask({ id, title: '导入数据库' })}
        />
      )}
      {credentials && id && (
        <RootPasswordModal
          serverId={id}
          local={selected?.local && !dockerInstance}
          onTask={(id) => setTask({ id, title: '重置ROOT密码' })}
          onClose={() => setCredentials(false)}
          onSaved={refresh}
        />
      )}
      {remoteOpen && (
        <RemoteDatabases
          label="MySQL"
          rows={list.data?.filter((row) => !row.local) ?? []}
          onClose={() => setRemoteOpen(false)}
          onAdd={() => setServerForm({})}
          onEdit={(server) => setServerForm({ server })}
          onDelete={(server) =>
            confirm(
              '删除远程数据库',
              server.name,
              '仅移除连接记录，保留服务器数据。关联计划停用，回收站将无法自动恢复。',
              () =>
                perform(async () => {
                  await request(`/api/db/servers/${server.id}`, {
                    method: 'DELETE',
                  });
                  client.setQueryData<Server[]>(['db-servers'], (rows) =>
                    rows?.filter((row) => row.id !== server.id),
                  );
                  if (id === server.id) setSid(undefined);
                }),
            )
          }
        />
      )}
      {deleting &&
        (() => {
          const delSrv =
            list.data?.find((item) => item.id === deleting.serverId) ??
            selected;
          const delRow = databases.data?.find(
            (row) =>
              row.name === deleting.name &&
              (row._serverId ?? id) === deleting.serverId,
          );
          const delRecyclable =
            (delSrv?.containerClient ?? false) || !!env.data?.clientInstalled;
          return (
            <DeleteDatabaseModal
              name={deleting.name}
              size={delRow?.size}
              createdAt={delRow?.createdAt}
              location={
                delSrv?.local && !delSrv.instanceId
                  ? '本地数据库'
                  : `${delSrv?.name}（${delSrv?.host}:${delSrv?.port}）`
              }
              url={`/api/db/servers/${deleting.serverId}/databases/${encodeURIComponent(deleting.name)}`}
              recyclable={delRecyclable}
              onClose={() => setDeleting(undefined)}
              onDeleted={(result) => {
                const removed = deleting;
                client.setQueryData<Database[]>(
                  ['db-databases', removed.serverId],
                  (rows) => rows?.filter((row) => row.name !== removed.name),
                );
                setSelectedDBs((prev) =>
                  prev.filter((name) => name !== removed.name),
                );
                if (result.taskId)
                  setTask({ id: result.taskId, title: '删除数据库' });
              }}
            />
          );
        })()}
      {trashOpen && (
        <TrashModal
          open
          initialTab="database"
          onClose={() => setTrashOpen(false)}
          refresh={refresh}
          onTask={(id) => setTask({ id, title: '回收站任务' })}
        />
      )}
      {serverForm && (
        <ServerForm
          {...serverForm}
          onClose={() => setServerForm(undefined)}
          onSaved={(v) => {
            setSid(v);
            setServerForm(undefined);
            refresh();
            message.success('连接已保存');
          }}
        />
      )}
      {op?.kind === 'database' && (
        <CreateDatabaseModal
          servers={list.data || []}
          initialServer={id}
          onClose={() => setOp(undefined)}
          onRemote={() => setRemoteOpen(true)}
          onCreated={async (server) => {
            const queryKey = ['db-databases', server];
            await client.cancelQueries({ queryKey, exact: true });
            await client.invalidateQueries({ queryKey, exact: true });
            setSid(server);
            setPage(1);
            setSelectedDBs([]);
            void client.invalidateQueries({ queryKey: ['db-users', server] });
          }}
        />
      )}
      {databaseSettings?.kind === 'access' &&
        (databaseSettings.database._serverId ?? id) && (
          <DatabaseAccessModal
            serverId={(databaseSettings.database._serverId ?? id)!}
            database={databaseSettings.database}
            onClose={() => setDatabaseSettings(undefined)}
            onSaved={refresh}
          />
        )}
      {batchModal &&
        id &&
        (() => {
          const batchSid =
            (selectedDatabases[0] as DatabaseRow | undefined)?._serverId ?? id;
          return (
            <DatabaseBatchModal
              serverId={batchSid}
              action={batchModal}
              databases={selectedDatabases}
              recyclable={recyclable}
              onClose={() => setBatchModal(undefined)}
              onStarted={(taskId) => {
                if (batchModal === 'delete') {
                  const removed = new Set(
                    selectedDatabases.map((database) => database.name),
                  );
                  client.setQueryData<Database[]>(
                    ['db-databases', batchSid],
                    (rows) => rows?.filter((row) => !removed.has(row.name)),
                  );
                }
                setTask({ id: taskId, title: '数据库批量操作' });
                setSelectedDBs([]);
              }}
            />
          );
        })()}
      {databaseSettings?.kind === 'password' &&
        (databaseSettings.database._serverId ?? id) && (
          <DatabasePasswordModal
            serverId={(databaseSettings.database._serverId ?? id)!}
            database={databaseSettings.database}
            onClose={() => setDatabaseSettings(undefined)}
            onSaved={refresh}
          />
        )}
      <Modal
        title={
          op?.kind === 'database'
            ? '创建数据库'
            : op?.kind === 'password'
              ? '修改密码'
              : '创建数据库用户'
        }
        open={!!op && op.kind !== 'database'}
        onCancel={() => setOp(undefined)}
        confirmLoading={busy}
        onOk={() => void submit()}
        width={LAYOUT.databaseModalWidth}
      >
        <Form
          form={form}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
        >
          <>
            {op?.kind === 'user' && (
              <>
                <Form.Item
                  name="username"
                  label="用户名"
                  rules={[{ required: true, pattern: /^[a-zA-Z0-9_]{1,32}$/ }]}
                >
                  <Input />
                </Form.Item>
                <Form.Item
                  name="host"
                  label="访问权限"
                  extra="填写 localhost、主机名、IP 或 %"
                  rules={[{ required: true }]}
                >
                  <Input />
                </Form.Item>
              </>
            )}
            <Form.Item
              name="password"
              label="密码"
              rules={[{ required: true }]}
            >
              <Input.Password autoComplete="new-password" />
            </Form.Item>
            {op?.kind === 'user' && (
              <>
                <Form.Item
                  name="databases"
                  label="授权数据库"
                  rules={[{ required: true }]}
                >
                  <Select
                    mode="multiple"
                    loading={grants.isFetching}
                    options={grants.data
                      ?.filter((d) => !d.system)
                      .map((d) => ({ value: d.name }))}
                  />
                </Form.Item>
                <Form.Item name="privilege" label="权限">
                  <Select
                    options={[
                      {
                        value: 'readonly',
                        label: '只读：SELECT / SHOW VIEW',
                      },
                      {
                        value: 'all',
                        label: '指定库全部权限（无 GRANT OPTION）',
                      },
                    ]}
                  />
                </Form.Item>
              </>
            )}
          </>
        </Form>
      </Modal>
      <Modal
        open={!!confirmation}
        title={confirmation?.title}
        onCancel={() => setConfirmation(undefined)}
        confirmLoading={busy}
        okButtonProps={{
          danger: true,
          disabled: confirmName !== confirmation?.name,
        }}
        onOk={() =>
          void perform(async () => {
            await confirmation!.run();
            setConfirmation(undefined);
          })
        }
      >
        <Alert type="warning" message={confirmation?.warning} />
        <Typography.Paragraph>
          输入名称「{confirmation?.name}」确认
        </Typography.Paragraph>
        <Input
          value={confirmName}
          onChange={(e) => setConfirmName(e.target.value)}
        />
      </Modal>
      <TaskDrawer
        taskId={task?.id ?? null}
        title={task?.title ?? '数据库任务'}
        open={!!task}
        onClose={() => setTask(undefined)}
        onSettled={refresh}
      />
    </>
  );
}
