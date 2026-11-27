import ResourceActions, {
  useResourceActions,
} from '../../plugins/ResourceActions';
import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Card,
  Empty,
  Flex,
  Form,
  Grid,
  Input,
  Modal,
  Select,
  Skeleton,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { RotateCw } from 'lucide-react';
import {
  pgBackups,
  pgCreateBackup,
  pgCreateDatabase,
  pgDatabases,
  pgDeleteBackup,
  pgDownloadBackupUrl,
  pgDropDatabase,
  pgInstances,
  pgQuery,
  pgRevealPasswordUrl,
  pgSaveDatabaseRemark,
  pgSetUserPassword,
  pgUsers,
  pgTableRows,
  pgTables,
  type PGDatabase,
  type PGQueryResult,
  type PGTable,
} from '../../api/postgres';
import { formatSize } from '../../utils/format';
import { LAYOUT, SPACING } from '../../styles/tokens';
import DatabaseInstanceControl from './DatabaseInstanceControl';
import DatabaseInstancesStatus from './DatabaseInstancesStatus';
import DatabasePassword from './DatabasePassword';
import ContentSizedTable from '../../components/ContentSizedTable';
import ApplicationInstallGuide from '../../components/ApplicationInstallGuide';
import InlineRemark from '../../components/InlineRemark';
import LocalBackupsModal from '../../components/LocalBackupsModal';
import { useRememberedTab } from '../../hooks/useRememberedTab';
import { useDatabaseBatchActions } from './useDatabaseBatchActions';

function generatePassword(length = 16): string {
  const chars =
    'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%&*';
  const buf = new Uint32Array(length);
  crypto.getRandomValues(buf);
  return Array.from(buf, (n) => chars[n % chars.length]).join('');
}

function formatCell(val: unknown): string {
  if (val === null || val === undefined) return 'NULL';
  if (typeof val === 'object') return JSON.stringify(val);
  return String(val);
}

function PGExplorerModal({
  instance,
  database,
  onClose,
}: {
  instance: string;
  database: string;
  onClose: () => void;
}) {
  const isDesktop = Grid.useBreakpoint().md;
  const [selected, setSelected] = useState<PGTable | null>(null);
  const [page, setPage] = useState(1);
  const [sqlText, setSqlText] = useState('SELECT now();');
  const [queryResult, setQueryResult] = useState<PGQueryResult | null>(null);

  const tablesQuery = useQuery({
    queryKey: ['pg-tables', instance, database],
    queryFn: ({ signal }) => pgTables(instance, database, signal),
  });

  const activeTable = selected ?? tablesQuery.data?.list[0] ?? null;

  const rowsQuery = useQuery({
    queryKey: [
      'pg-rows',
      instance,
      database,
      activeTable?.schema,
      activeTable?.name,
      page,
    ],
    enabled: Boolean(activeTable),
    queryFn: ({ signal }) =>
      pgTableRows(
        instance,
        database,
        activeTable!.schema,
        activeTable!.name,
        page,
        50,
        signal,
      ),
  });

  const runSQL = useMutation({
    mutationFn: (sql: string) => pgQuery(instance, database, sql),
    onSuccess: (res) => {
      setQueryResult(res);
      void tablesQuery.refetch();
      if (activeTable) void rowsQuery.refetch();
    },
  });

  const tables = tablesQuery.data?.list ?? [];

  return (
    <Modal
      open
      centered
      title={`PostgreSQL 管理 · ${database}`}
      width="min(1100px, calc(100vw - 24px))"
      footer={null}
      onCancel={onClose}
      destroyOnClose
    >
      <Tabs
        items={[
          {
            key: 'browse',
            label: '数据表浏览',
            children: (
              <Flex
                vertical={!isDesktop}
                gap={16}
                style={{ minHeight: isDesktop ? 440 : undefined }}
              >
                {isDesktop ? (
                  <div
                    style={{
                      width: 220,
                      flexShrink: 0,
                      borderRight: '1px solid var(--ez-border-light)',
                      paddingRight: 12,
                      maxHeight: 480,
                      overflowY: 'auto',
                    }}
                  >
                    <Flex
                      justify="space-between"
                      align="center"
                      style={{ marginBottom: 8 }}
                    >
                      <Typography.Text strong>数据表</Typography.Text>
                      <Button
                        size="small"
                        type="text"
                        icon={<RotateCw size={13} />}
                        onClick={() => void tablesQuery.refetch()}
                      />
                    </Flex>
                    {tablesQuery.isPending ? (
                      <Skeleton active paragraph={{ rows: 5 }} />
                    ) : !tables.length ? (
                      <Empty
                        image={Empty.PRESENTED_IMAGE_SIMPLE}
                        description="暂无数据表"
                      />
                    ) : (
                      <Space
                        direction="vertical"
                        size={4}
                        style={{ width: '100%' }}
                      >
                        {tables.map((t) => {
                          const isActive =
                            activeTable?.schema === t.schema &&
                            activeTable?.name === t.name;
                          return (
                            <Button
                              key={`${t.schema}.${t.name}`}
                              type={isActive ? 'primary' : 'text'}
                              block
                              style={{
                                justifyContent: 'space-between',
                                textAlign: 'left',
                              }}
                              onClick={() => {
                                setSelected(t);
                                setPage(1);
                              }}
                            >
                              <span
                                style={{
                                  overflow: 'hidden',
                                  textOverflow: 'ellipsis',
                                }}
                              >
                                {t.schema === 'public'
                                  ? t.name
                                  : `${t.schema}.${t.name}`}
                              </span>
                              <Tag style={{ marginInlineEnd: 0 }}>
                                {t.rowEstimate}
                              </Tag>
                            </Button>
                          );
                        })}
                      </Space>
                    )}
                  </div>
                ) : (
                  <Flex gap={8} align="center">
                    <Select
                      style={{ flex: 1 }}
                      placeholder="选择数据表"
                      value={
                        activeTable
                          ? `${activeTable.schema}.${activeTable.name}`
                          : undefined
                      }
                      options={tables.map((t) => ({
                        value: `${t.schema}.${t.name}`,
                        label: `${t.schema === 'public' ? t.name : `${t.schema}.${t.name}`} (${t.rowEstimate})`,
                      }))}
                      onChange={(val) => {
                        const found = tables.find(
                          (t) => `${t.schema}.${t.name}` === val,
                        );
                        if (found) {
                          setSelected(found);
                          setPage(1);
                        }
                      }}
                    />
                    <Button
                      icon={<RotateCw size={14} />}
                      onClick={() => void tablesQuery.refetch()}
                    />
                  </Flex>
                )}

                <div style={{ flex: 1, minWidth: 0 }}>
                  {activeTable ? (
                    <>
                      <Flex
                        justify="space-between"
                        align="center"
                        wrap
                        gap={8}
                        style={{ marginBottom: 10 }}
                      >
                        <Space wrap>
                          <Typography.Text strong>
                            {activeTable.schema}.{activeTable.name}
                          </Typography.Text>
                          <Tag>{formatSize(activeTable.sizeBytes)}</Tag>
                        </Space>
                        <Button
                          size="small"
                          icon={<RotateCw size={13} />}
                          onClick={() => void rowsQuery.refetch()}
                        >
                          刷新
                        </Button>
                      </Flex>
                      <Table
                        size="small"
                        loading={rowsQuery.isFetching}
                        scroll={{ x: 'max-content', y: 360 }}
                        dataSource={(rowsQuery.data?.rows ?? []).map(
                          (r, i) => ({
                            ...r,
                            __key: `${page}-${i}`,
                          }),
                        )}
                        rowKey="__key"
                        columns={(rowsQuery.data?.columns ?? []).map((col) => ({
                          title: (
                            <Space size={4}>
                              <span>{col.name}</span>
                              {col.primaryKey && <Tag color="blue">PK</Tag>}
                            </Space>
                          ),
                          dataIndex: col.name,
                          ellipsis: true,
                          width: 160,
                          render: (v: unknown) => (
                            <Typography.Text
                              type={
                                v === null || v === undefined
                                  ? 'secondary'
                                  : undefined
                              }
                            >
                              {formatCell(v)}
                            </Typography.Text>
                          ),
                        }))}
                        pagination={{
                          current: page,
                          pageSize: 50,
                          total: rowsQuery.data?.total ?? 0,
                          onChange: (p) => setPage(p),
                          showTotal: (t) => `共 ${t} 条`,
                        }}
                      />
                    </>
                  ) : (
                    <Empty
                      image={Empty.PRESENTED_IMAGE_SIMPLE}
                      description="请选择左侧数据表"
                    />
                  )}
                </div>
              </Flex>
            ),
          },
          {
            key: 'sql',
            label: 'SQL 查询',
            children: (
              <Space
                direction="vertical"
                size="middle"
                style={{ width: '100%' }}
              >
                <Input.TextArea
                  rows={5}
                  value={sqlText}
                  onChange={(e) => setSqlText(e.target.value)}
                  placeholder="输入 PostgreSQL SQL 语句..."
                  style={{ fontFamily: 'monospace' }}
                />
                <Flex justify="space-between" align="center" wrap gap={8}>
                  <Button
                    type="primary"
                    loading={runSQL.isPending}
                    onClick={() => runSQL.mutate(sqlText)}
                  >
                    执行 SQL
                  </Button>
                  {queryResult?.message && (
                    <Typography.Text type="secondary">
                      {queryResult.message}
                    </Typography.Text>
                  )}
                </Flex>
                {queryResult && queryResult.columns.length > 0 && (
                  <Table
                    size="small"
                    scroll={{ x: 'max-content', y: 320 }}
                    dataSource={queryResult.rows.map((r, i) => ({
                      ...r,
                      __key: i,
                    }))}
                    rowKey="__key"
                    columns={queryResult.columns.map((col) => ({
                      title: col,
                      dataIndex: col,
                      ellipsis: true,
                      width: 160,
                      render: (v: unknown) => formatCell(v),
                    }))}
                    pagination={{ pageSize: 50 }}
                  />
                )}
              </Space>
            ),
          },
        ]}
      />
    </Modal>
  );
}

type PGDatabaseRow = PGDatabase & {
  _instanceId: string;
  _instanceLabel: string;
  _key: string;
};

function pgInstanceLabel(item: { id: string; name: string }) {
  return item.id === 'default'
    ? 'PostgreSQL'
    : item.name.replace(/^本机\s+/, '');
}

export default function PostgresPage() {
  const pluginActions = useResourceActions('databases');
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const [requestedInstance, setInstanceId] = useState<string>();
  const [keyword, setKeyword] = useState('');
  const [createDBOpen, setCreateDBOpen] = useState(false);
  const [customUser, setCustomUser] = useState(false);
  const [pwdDB, setPwdDB] = useState<PGDatabaseRow | null>(null);
  const [explorerDB, setExplorerDB] = useState<{
    instanceId: string;
    name: string;
  } | null>(null);
  const [backupDB, setBackupDB] = useState<{
    instanceId: string;
    name: string;
  } | null>(null);
  const [dbForm] = Form.useForm();
  const [pwdForm] = Form.useForm();

  const instancesQuery = useQuery({
    queryKey: ['pg-instances'],
    queryFn: ({ signal }) => pgInstances(signal),
  });

  const instances = instancesQuery.data?.list ?? [];
  const { tab: instanceId, loading: selectionLoading } = useRememberedTab(
    'postgresqlInstance',
    requestedInstance,
    ['__all__', ...instances.map((item) => item.id)],
  );
  const reuseRole = Form.useWatch('reuseRole', dbForm);
  const roleInstance = Form.useWatch('instanceId', dbForm);
  const roles = useQuery({
    queryKey: ['pg-users', roleInstance],
    queryFn: ({ signal }) => pgUsers(roleInstance, signal),
    enabled: createDBOpen && Boolean(reuseRole && roleInstance),
  });
  const isAll = instanceId === '__all__';
  const activeInstance = isAll
    ? (instances.find((i) => i.running) ?? instances[0])
    : (instances.find((i) => i.id === instanceId) ?? instances[0]);
  const targetInstances = selectionLoading
    ? []
    : isAll
      ? instances.filter((i) => i.running)
      : activeInstance && activeInstance.running
        ? [activeInstance]
        : [];

  const dbQueries = useQueries({
    queries: targetInstances.map((inst) => ({
      queryKey: ['pg-databases', inst.id],
      queryFn: ({ signal }: { signal?: AbortSignal }) =>
        pgDatabases(inst.id, signal),
    })),
  });

  const allDatabaseRows: PGDatabaseRow[] = targetInstances.flatMap(
    (inst, idx) => {
      const label = pgInstanceLabel(inst);
      return (dbQueries[idx]?.data?.list ?? []).map((db) => ({
        ...db,
        _instanceId: inst.id,
        _instanceLabel: label,
        _key: `${inst.id}:${db.name}`,
      }));
    },
  );
  const databasesFetching = dbQueries.some((q) => q.isFetching);
  const databaseErrors = targetInstances.flatMap((instance, index) => {
    const query = dbQueries[index];
    return query?.error
      ? [{ instance, error: query.error, retry: query.refetch }]
      : [];
  });
  const databasesUpdatedAt = dbQueries.reduce(
    (max, q) => Math.max(max, q.dataUpdatedAt),
    0,
  );

  const createDB = useMutation({
    mutationFn: (values: {
      instanceId: string;
      name: string;
      username?: string;
      password?: string;
      encoding?: string;
      reuseRole?: boolean;
    }) =>
      pgCreateDatabase(values.instanceId || activeInstance?.id || '', {
        name: values.name,
        username: values.username,
        password: values.reuseRole ? undefined : values.password,
        reuseRole: values.reuseRole,
        encoding: values.encoding,
      }),
    onSuccess: (_data, values) => {
      setCreateDBOpen(false);
      dbForm.resetFields();
      void client.invalidateQueries({
        queryKey: ['pg-databases', values.instanceId || activeInstance?.id],
      });
      void client.invalidateQueries({
        queryKey: ['pg-users', values.instanceId || activeInstance?.id],
      });
      void message.success('数据库创建成功');
    },
  });

  const changePwd = useMutation({
    mutationFn: (values: { password: string }) =>
      pgSetUserPassword(pwdDB!._instanceId, pwdDB!.owner, values.password),
    onSuccess: () => {
      const targetInst = pwdDB?._instanceId;
      setPwdDB(null);
      pwdForm.resetFields();
      if (targetInst) {
        void client.invalidateQueries({
          queryKey: ['pg-databases', targetInst],
        });
      }
      void message.success('密码已修改');
    },
  });

  const refresh = () => {
    void instancesQuery.refetch();
    void client.invalidateQueries({ queryKey: ['pg-databases'] });
  };
  const filteredDatabases = allDatabaseRows.filter((row) =>
    row.name.toLowerCase().includes(keyword.trim().toLowerCase()),
  );
  const batch = useDatabaseBatchActions({
    rows: filteredDatabases,
    rowKey: (row) => row._key,
    describe: (row) => `${row.name}（${row._instanceLabel}）`,
    deleteNotice: '数据库及其数据将永久删除，请核对库名和所属实例。',
    operate: (row, action) =>
      action === 'backup'
        ? pgCreateBackup(row._instanceId, row.name, true)
        : pgDropDatabase(row._instanceId, row.name, true),
    onChanged: () => {
      void client.invalidateQueries({ queryKey: ['pg-databases'] });
      void client.invalidateQueries({ queryKey: ['pg-backups'] });
    },
  });

  if (instancesQuery.isPending || selectionLoading) {
    return (
      <Card>
        <Skeleton active paragraph={{ rows: 5 }} />
      </Card>
    );
  }

  if (instancesQuery.isError) {
    return (
      <Card>
        <Alert
          type="error"
          showIcon
          message={
            instancesQuery.error?.message || '读取 PostgreSQL 实例列表失败'
          }
          action={
            <Button size="small" onClick={() => void instancesQuery.refetch()}>
              重试
            </Button>
          }
        />
      </Card>
    );
  }

  if (!instances.length) {
    return (
      <ApplicationInstallGuide
        name="PostgreSQL"
        install="postgresql"
        onDetect={refresh}
      />
    );
  }

  const anyRunning = instances.some((i) => i.running);

  return (
    <div style={{ position: 'relative', minHeight: 500 }}>
      <Card style={{ marginBottom: SPACING.lg }}>
        <Flex justify="space-between" align="center" gap={SPACING.md} wrap>
          <Space wrap>
            <Button
              type="primary"
              disabled={!anyRunning}
              onClick={() => {
                setCustomUser(false);
                dbForm.setFieldsValue({
                  instanceId:
                    !isAll && activeInstance?.running
                      ? activeInstance.id
                      : (instances.find((i) => i.running)?.id ?? ''),
                  name: '',
                  username: '',
                  password: generatePassword(),
                  encoding: 'UTF8',
                  reuseRole: false,
                });
                setCreateDBOpen(true);
              }}
            >
              添加数据库
            </Button>
            {isAll && instances.length > 1 ? (
              <DatabaseInstancesStatus
                engine="postgresql"
                instances={instances.map((instance) => ({
                  ...instance,
                  instanceId: instance.id,
                  name: pgInstanceLabel(instance),
                }))}
                onChanged={refresh}
              />
            ) : (
              activeInstance && (
                <DatabaseInstanceControl
                  key={activeInstance.id}
                  engine="postgresql"
                  instanceId={activeInstance.id}
                  name={pgInstanceLabel(activeInstance)}
                  version={activeInstance.version}
                  running={activeInstance.running}
                  onChanged={refresh}
                />
              )
            )}
          </Space>
          <Space wrap>
            <Select
              placeholder="选择 PostgreSQL 实例"
              style={{ width: LAYOUT.databaseSelectWidth, maxWidth: '100%' }}
              value={instanceId}
              options={[
                { value: '__all__', label: '全部' },
                ...instances.map((item) => ({
                  value: item.id,
                  label: `${pgInstanceLabel(item)}${item.running ? '' : '（已停止）'}`,
                })),
              ]}
              onChange={(v) => {
                setInstanceId(v);
                setExplorerDB(null);
                setBackupDB(null);
                batch.reset();
              }}
            />
            <Input.Search
              allowClear
              placeholder="请输入数据库名"
              onSearch={(value) => {
                setKeyword(value);
                batch.reset();
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
          </Space>
        </Flex>
      </Card>

      <Card>
        {!!databaseErrors.length && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: SPACING.md }}
            message="部分 PostgreSQL 实例的数据库列表读取失败"
            description={databaseErrors.map(({ instance, error }) => (
              <div key={instance.id}>
                {pgInstanceLabel(instance)}：{error.message}
              </div>
            ))}
            action={
              <Button
                size="small"
                loading={databasesFetching}
                onClick={() => {
                  for (const { retry } of databaseErrors) void retry();
                }}
              >
                重试失败项
              </Button>
            }
          />
        )}
        {!isAll && !activeInstance?.running ? (
          <Alert
            type="warning"
            showIcon
            message="当前 PostgreSQL 实例未运行，请先启动服务。"
          />
        ) : (
          <ContentSizedTable<PGDatabaseRow>
            rowKey="_key"
            fillColumn="name"
            loading={databasesFetching}
            dataSource={filteredDatabases}
            rowSelection={batch.rowSelection}
            pagination={batch.pagination}
            footer={batch.footer}
            locale={{
              emptyText: (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={
                    databaseErrors.length ? '未能获取完整列表' : '暂无数据库'
                  }
                />
              ),
            }}
            columns={[
              {
                key: 'name',
                title: '数据库名',
                dataIndex: 'name',
                minWidth: 160,
                width: 180,
                ellipsis: true,
              },
              {
                key: 'username',
                title: '用户名',
                dataIndex: 'username',
                minWidth: 132,
                width: 150,
                ellipsis: true,
                render: (value: string | undefined, row) => {
                  const user = value || row.owner;
                  return user ? (
                    <Typography.Text copyable>{user}</Typography.Text>
                  ) : (
                    '未记录'
                  );
                },
              },
              {
                key: 'password',
                title: '密码',
                minWidth: 120,
                width: 210,
                render: (_, row) => (
                  <DatabasePassword
                    key={`${row._instanceId}:${row.name}:${databasesUpdatedAt}`}
                    serverId={row._instanceId}
                    name={row.name}
                    recorded={Boolean(row.hasPassword)}
                    revealUrl={pgRevealPasswordUrl(row._instanceId, row.name)}
                  />
                ),
              },
              {
                key: 'backups',
                title: '备份',
                dataIndex: 'backupCount',
                minWidth: 102,
                width: 120,
                render: (v: number | undefined, row) => (
                  <Button
                    type="link"
                    style={{ padding: 0 }}
                    onClick={() =>
                      setBackupDB({
                        instanceId: row._instanceId,
                        name: row.name,
                      })
                    }
                  >
                    {v && v > 0 ? `${v}份备份` : '无备份'}
                  </Button>
                ),
              },
              {
                key: 'location',
                title: '数据库位置',
                dataIndex: '_instanceLabel',
                minWidth: 132,
                width: 160,
                ellipsis: true,
              },
              {
                key: 'remark',
                title: '备注',
                minWidth: 136,
                width: 200,
                render: (_, row) => (
                  <InlineRemark
                    value={row.remark || ''}
                    label={`编辑${row.name}备注`}
                    maxLength={512}
                    onSave={async (value) => {
                      await pgSaveDatabaseRemark(
                        row._instanceId,
                        row.name,
                        value,
                      );
                      await client.invalidateQueries({
                        queryKey: ['pg-databases', row._instanceId],
                      });
                    }}
                  />
                ),
              },
              {
                key: 'actions',
                title: '操作',
                minWidth: 128,
                width: 180,
                fixed: fixedActions,
                render: (_, row) => (
                  <Space size="small">
                    <Button
                      type="link"
                      style={{ padding: 0 }}
                      onClick={() =>
                        setExplorerDB({
                          instanceId: row._instanceId,
                          name: row.name,
                        })
                      }
                    >
                      管理
                    </Button>
                    <Button
                      type="link"
                      style={{ padding: 0 }}
                      onClick={() => {
                        pwdForm.setFieldsValue({
                          password: generatePassword(),
                        });
                        setPwdDB(row);
                      }}
                    >
                      改密
                    </Button>
                    <Button
                      type="link"
                      danger
                      style={{ padding: 0 }}
                      onClick={() =>
                        modal.confirm({
                          title: `删除数据库 ${row.name}？`,
                          content: '删除后数据将无法恢复，请确认。',
                          okButtonProps: { danger: true },
                          onOk: async () => {
                            await pgDropDatabase(row._instanceId, row.name);
                            client.setQueryData<{ list: PGDatabase[] }>(
                              ['pg-databases', row._instanceId],
                              (data) =>
                                data && {
                                  list: data.list.filter(
                                    (item) => item.name !== row.name,
                                  ),
                                },
                            );
                            void message.success('数据库已删除');
                          },
                        })
                      }
                    >
                      删除
                    </Button>
                    <ResourceActions
                      items={pluginActions(
                        row._instanceId,
                        row.name,
                        'postgresql',
                      )}
                    />
                  </Space>
                ),
              },
            ]}
          />
        )}
      </Card>

      {explorerDB && (
        <PGExplorerModal
          instance={explorerDB.instanceId}
          database={explorerDB.name}
          onClose={() => setExplorerDB(null)}
        />
      )}

      {backupDB && (
        <LocalBackupsModal
          title={`[${backupDB.name}] 数据库备份`}
          queryKey={['pg-backups', backupDB.instanceId, backupDB.name]}
          allowCreate
          fetchList={(signal) =>
            pgBackups(backupDB.instanceId, backupDB.name, signal)
          }
          onCreate={() => pgCreateBackup(backupDB.instanceId, backupDB.name)}
          onDelete={(fileName) =>
            pgDeleteBackup(backupDB.instanceId, backupDB.name, fileName)
          }
          downloadUrl={(fileName) =>
            pgDownloadBackupUrl(backupDB.instanceId, backupDB.name, fileName)
          }
          onChanged={() =>
            void client.invalidateQueries({
              queryKey: ['pg-databases', backupDB.instanceId],
            })
          }
          onClose={() => setBackupDB(null)}
        />
      )}

      <Modal
        open={createDBOpen}
        centered
        title="添加数据库"
        onCancel={() => setCreateDBOpen(false)}
        onOk={() => dbForm.submit()}
        confirmLoading={createDB.isPending}
      >
        <Form
          form={dbForm}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
          onValuesChange={(changed) => {
            if ('reuseRole' in changed) {
              setCustomUser(false);
              dbForm.setFieldValue(
                'username',
                changed.reuseRole ? undefined : dbForm.getFieldValue('name'),
              );
              return;
            }
            if ('username' in changed) {
              setCustomUser(changed.username !== dbForm.getFieldValue('name'));
            }
            if ('name' in changed && !customUser && !reuseRole) {
              dbForm.setFieldValue(
                'username',
                String(changed.name || '').slice(0, 63),
              );
            }
          }}
          onFinish={(v) => createDB.mutate(v)}
        >
          <Form.Item label="数据库名" required>
            <Space.Compact style={{ width: '100%' }}>
              <Form.Item
                name="name"
                noStyle
                rules={[{ required: true, message: '请输入数据库名' }]}
              >
                <Input placeholder="例如 app_db" />
              </Form.Item>
              <Form.Item name="encoding" noStyle>
                <Select
                  style={{ width: 120 }}
                  options={[
                    { value: 'UTF8', label: 'UTF8' },
                    { value: 'LATIN1', label: 'LATIN1' },
                    { value: 'SQL_ASCII', label: 'SQL_ASCII' },
                  ]}
                />
              </Form.Item>
            </Space.Compact>
          </Form.Item>
          <Form.Item name="reuseRole" label="账号方式">
            <Select
              options={[
                { value: false, label: '创建新角色' },
                { value: true, label: '使用已有角色' },
              ]}
            />
          </Form.Item>
          <Form.Item
            name="username"
            label="用户名"
            rules={[{ required: true, message: '请输入用户名' }]}
          >
            {reuseRole ? (
              <Select
                loading={roles.isFetching}
                options={roles.data?.list.map((role) => ({
                  label: role.name,
                  value: role.name,
                }))}
              />
            ) : (
              <Input placeholder="用户名" />
            )}
          </Form.Item>
          {!reuseRole && (
            <Form.Item label="密码" required>
              <Space.Compact style={{ width: '100%' }}>
                <Form.Item
                  name="password"
                  noStyle
                  rules={[
                    { required: true, message: '请输入密码' },
                    { min: 6, message: '密码至少 6 位' },
                  ]}
                >
                  <Input.Password placeholder="至少 6 位" />
                </Form.Item>
                <Button
                  aria-label="随机生成密码"
                  icon={<RotateCw size={14} />}
                  onClick={() =>
                    dbForm.setFieldValue('password', generatePassword())
                  }
                />
              </Space.Compact>
            </Form.Item>
          )}
          <Form.Item
            name="instanceId"
            label="添加至"
            rules={[{ required: true, message: '请选择 PostgreSQL 实例' }]}
          >
            <Select
              onChange={() => {
                if (reuseRole) dbForm.setFieldValue('username', undefined);
              }}
              options={instances.map((item) => ({
                value: item.id,
                label: `${pgInstanceLabel(item)}${item.running ? '' : '（已停止）'}`,
                disabled: !item.running,
              }))}
            />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={Boolean(pwdDB)}
        centered
        title={`修改角色密码 · ${pwdDB?.owner ?? ''}`}
        onCancel={() => setPwdDB(null)}
        onOk={() => pwdForm.submit()}
        confirmLoading={changePwd.isPending}
      >
        <Form
          form={pwdForm}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
          onFinish={(v) => changePwd.mutate(v)}
        >
          <Form.Item
            label="角色"
            extra="新密码对该实例内使用此角色的所有数据库生效。"
          >
            <Input value={pwdDB?.owner || ''} readOnly />
          </Form.Item>
          <Form.Item label="新密码" required>
            <Space.Compact style={{ width: '100%' }}>
              <Form.Item
                name="password"
                noStyle
                rules={[
                  { required: true, message: '请输入新密码' },
                  { min: 6, message: '密码至少 6 位' },
                ]}
              >
                <Input.Password placeholder="至少 6 位" />
              </Form.Item>
              <Button
                aria-label="随机生成密码"
                icon={<RotateCw size={14} />}
                onClick={() =>
                  pwdForm.setFieldValue('password', generatePassword())
                }
              />
            </Space.Compact>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
