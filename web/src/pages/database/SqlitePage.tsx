import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Alert,
  App,
  Badge,
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
import { FolderOpenOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Database as DatabaseIcon, Plus, RotateCw } from 'lucide-react';
import {
  sqliteAddFile,
  sqliteBackups,
  sqliteCreateBackup,
  sqliteDeleteBackup,
  sqliteDeleteRow,
  sqliteDownloadBackupUrl,
  sqliteFiles,
  sqliteInsertRow,
  sqliteQuery,
  sqliteRemoveFile,
  sqliteTableRows,
  sqliteTables,
  sqliteUpdateRow,
  type SQLiteRow,
  type SQLiteFile,
  type SQLiteQueryResult,
  type SQLiteTable,
} from '../../api/sqlite';
import { formatSize } from '../../utils/format';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { useAuth } from '../../stores/auth';
import ContentSizedTable from '../../components/ContentSizedTable';
import LocalBackupsModal from '../../components/LocalBackupsModal';
import FilePicker from '../file/FilePicker';

function parentDir(filePath: string): string {
  const idx = filePath.lastIndexOf('/');
  return idx > 0 ? filePath.slice(0, idx) : '/';
}

function formatCell(val: unknown): string {
  if (val === null || val === undefined) return 'NULL';
  if (typeof val === 'object') return JSON.stringify(val);
  return String(val);
}

function SQLiteExplorerModal({
  file,
  onClose,
}: {
  file: SQLiteFile;
  onClose: () => void;
}) {
  const { message, modal } = App.useApp();
  const isDesktop = Grid.useBreakpoint().md;
  const [selected, setSelected] = useState<SQLiteTable | null>(null);
  const [page, setPage] = useState(1);
  const [editingRow, setEditingRow] = useState<SQLiteRow | null>(null);
  const [insertOpen, setInsertOpen] = useState(false);
  const [rowForm] = Form.useForm();
  const [sqlText, setSqlText] = useState(
    "SELECT name, type FROM sqlite_master WHERE type IN ('table', 'view');",
  );
  const [queryResult, setQueryResult] = useState<SQLiteQueryResult | null>(
    null,
  );

  const tablesQuery = useQuery({
    queryKey: ['sqlite-tables', file.id],
    queryFn: ({ signal }) => sqliteTables(file.id, signal),
  });

  const activeTable = selected ?? tablesQuery.data?.list[0] ?? null;

  const rowsQuery = useQuery({
    queryKey: ['sqlite-rows', file.id, activeTable?.name, page],
    enabled: Boolean(activeTable),
    queryFn: ({ signal }) =>
      sqliteTableRows(file.id, activeTable!.name, page, 50, signal),
  });

  const columns = rowsQuery.data?.columns ?? [];

  const insertMutation = useMutation({
    mutationFn: (values: Record<string, unknown>) =>
      sqliteInsertRow(file.id, activeTable!.name, values),
    onSuccess: () => {
      setInsertOpen(false);
      rowForm.resetFields();
      void rowsQuery.refetch();
      void tablesQuery.refetch();
      void message.success('已新增记录');
    },
  });

  const updateMutation = useMutation({
    mutationFn: (values: Record<string, unknown>) =>
      sqliteUpdateRow(file.id, activeTable!.name, editingRow!.key, values),
    onSuccess: () => {
      setEditingRow(null);
      rowForm.resetFields();
      void rowsQuery.refetch();
      void message.success('记录已更新');
    },
  });

  const runSQL = useMutation({
    mutationFn: (sql: string) => sqliteQuery(file.id, sql),
    onSuccess: (res) => {
      setQueryResult(res);
      void tablesQuery.refetch();
      if (activeTable) void rowsQuery.refetch();
    },
  });

  const openEdit = (row: SQLiteRow) => {
    setEditingRow(row);
    const initial: Record<string, string> = {};
    for (const col of columns) {
      const val = row.values[col.name];
      initial[col.name] = val === null || val === undefined ? '' : String(val);
    }
    rowForm.setFieldsValue(initial);
  };

  const openInsert = () => {
    setInsertOpen(true);
    rowForm.resetFields();
  };

  const tables = tablesQuery.data?.list ?? [];

  return (
    <Modal
      open
      centered
      width="min(1080px, calc(100vw - 24px))"
      title={`SQLite 管理 · ${file.name}`}
      footer={null}
      onCancel={onClose}
    >
      <Typography.Text
        code
        ellipsis
        style={{
          display: 'inline-block',
          maxWidth: '100%',
          marginBottom: 12,
        }}
      >
        {file.path}
      </Typography.Text>
      <Tabs
        items={[
          {
            key: 'data',
            label: '数据浏览',
            children: (
              <Flex
                vertical={!isDesktop}
                gap={16}
                style={{ minHeight: isDesktop ? 460 : undefined }}
              >
                {isDesktop ? (
                  <div
                    style={{
                      width: 220,
                      flexShrink: 0,
                      borderRight: '1px solid var(--ez-border-light)',
                      paddingRight: 12,
                      maxHeight: 500,
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
                          const isActive = activeTable?.name === t.name;
                          return (
                            <Button
                              key={t.name}
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
                                {t.name}
                              </span>
                              <Typography.Text
                                type={isActive ? undefined : 'secondary'}
                                style={{
                                  fontSize: 12,
                                  color: isActive ? '#fff' : undefined,
                                }}
                              >
                                {t.rowCount}
                              </Typography.Text>
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
                      value={activeTable?.name}
                      options={tables.map((t) => ({
                        value: t.name,
                        label: `${t.name} (${t.rowCount})`,
                      }))}
                      onChange={(val) => {
                        const found = tables.find((t) => t.name === val);
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
                    <Space
                      direction="vertical"
                      size="small"
                      style={{ width: '100%' }}
                    >
                      <Flex justify="space-between" align="center">
                        <Space>
                          <Typography.Text strong>
                            {activeTable.name}
                          </Typography.Text>
                          <Tag>{activeTable.type}</Tag>
                        </Space>
                        <Space>
                          {activeTable.type === 'table' && (
                            <Button
                              size="small"
                              type="primary"
                              icon={<Plus size={13} />}
                              onClick={openInsert}
                            >
                              新增行
                            </Button>
                          )}
                          <Button
                            size="small"
                            onClick={() => void rowsQuery.refetch()}
                          >
                            刷新
                          </Button>
                        </Space>
                      </Flex>
                      <Table
                        size="small"
                        loading={rowsQuery.isFetching}
                        scroll={{ x: 'max-content', y: 390 }}
                        dataSource={(rowsQuery.data?.rows ?? []).map(
                          (row, index) => ({
                            ...row,
                            displayKey: row.key || String(index),
                          }),
                        )}
                        rowKey="displayKey"
                        columns={[
                          ...columns.map((col) => ({
                            title: (
                              <Space size={4}>
                                <span>{col.name}</span>
                                {col.primaryKey && (
                                  <Tag color="gold" style={{ margin: 0 }}>
                                    PK
                                  </Tag>
                                )}
                              </Space>
                            ),
                            dataIndex: ['values', col.name],
                            ellipsis: true,
                            width: 150,
                            render: (v: unknown) => (
                              <Typography.Text
                                type={v === null ? 'secondary' : undefined}
                              >
                                {formatCell(v)}
                              </Typography.Text>
                            ),
                          })),
                          ...(activeTable.type === 'table'
                            ? [
                                {
                                  title: '操作',
                                  fixed: 'right' as const,
                                  width: 110,
                                  render: (_: unknown, row: SQLiteRow) => (
                                    <Space size={8}>
                                      <Button
                                        type="link"
                                        size="small"
                                        style={{ padding: 0 }}
                                        disabled={!row.key}
                                        onClick={() => openEdit(row)}
                                      >
                                        编辑
                                      </Button>
                                      <Button
                                        type="link"
                                        size="small"
                                        danger
                                        style={{ padding: 0 }}
                                        disabled={!row.key}
                                        onClick={() =>
                                          modal.confirm({
                                            title: '删除该行记录？',
                                            okButtonProps: { danger: true },
                                            onOk: async () => {
                                              await sqliteDeleteRow(
                                                file.id,
                                                activeTable.name,
                                                row.key,
                                              );
                                              await rowsQuery.refetch();
                                              await tablesQuery.refetch();
                                              void message.success(
                                                '记录已删除',
                                              );
                                            },
                                          })
                                        }
                                      >
                                        删除
                                      </Button>
                                    </Space>
                                  ),
                                },
                              ]
                            : []),
                        ]}
                        pagination={{
                          current: page,
                          pageSize: 50,
                          total: rowsQuery.data?.total ?? 0,
                          showSizeChanger: false,
                          onChange: setPage,
                        }}
                      />
                    </Space>
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
            key: 'schema',
            label: '表结构',
            children: activeTable ? (
              <Space
                direction="vertical"
                size="middle"
                style={{ width: '100%' }}
              >
                <Table
                  size="small"
                  rowKey="name"
                  pagination={false}
                  dataSource={columns}
                  columns={[
                    { title: '字段名', dataIndex: 'name' },
                    { title: '类型', dataIndex: 'type', width: 140 },
                    {
                      title: '主键',
                      dataIndex: 'primaryKey',
                      width: 80,
                      render: (v: boolean) =>
                        v ? <Tag color="gold">PK</Tag> : '—',
                    },
                    {
                      title: '非空',
                      dataIndex: 'notNull',
                      width: 80,
                      render: (v: boolean) => (v ? '是' : '否'),
                    },
                    {
                      title: '默认值',
                      dataIndex: 'defaultValue',
                      render: (v: string) => v || '—',
                    },
                  ]}
                />
                {activeTable.sql && (
                  <pre
                    style={{
                      margin: 0,
                      padding: 12,
                      borderRadius: 8,
                      background: 'var(--ez-bg-subtle)',
                      overflowX: 'auto',
                      fontSize: 12,
                    }}
                  >
                    {activeTable.sql}
                  </pre>
                )}
              </Space>
            ) : (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="请先选择数据表"
              />
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
                  placeholder="输入 SQLite SQL 语句..."
                  style={{ fontFamily: 'monospace' }}
                />
                <Flex justify="space-between" align="center">
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

      <Modal
        open={insertOpen || Boolean(editingRow)}
        centered
        width="min(560px, calc(100vw - 24px))"
        title={
          insertOpen
            ? `新增行 · ${activeTable?.name}`
            : `编辑行 · ${activeTable?.name}`
        }
        onCancel={() => {
          setInsertOpen(false);
          setEditingRow(null);
        }}
        onOk={() => rowForm.submit()}
        confirmLoading={insertMutation.isPending || updateMutation.isPending}
      >
        <Form
          form={rowForm}
          layout="horizontal"
          labelCol={{ flex: isDesktop ? '140px' : '100px' }}
          wrapperCol={{ flex: 1 }}
          colon={false}
          onFinish={(values) => {
            if (insertOpen) {
              insertMutation.mutate(values);
              return;
            }
            if (!editingRow) return;
            const changed = Object.fromEntries(
              Object.entries(values).filter(
                ([name, value]) =>
                  value !== String(editingRow.values[name] ?? ''),
              ),
            );
            if (Object.keys(changed).length === 0) {
              setEditingRow(null);
              return;
            }
            updateMutation.mutate(changed);
          }}
        >
          {columns.map((col) => (
            <Form.Item
              key={col.name}
              name={col.name}
              label={`${col.name}${col.type ? ` (${col.type})` : ''}`}
            >
              <Input placeholder={col.defaultValue || '留空'} />
            </Form.Item>
          ))}
        </Form>
      </Modal>
    </Modal>
  );
}

export default function SqlitePage() {
  const navigate = useNavigate();
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const siteRoot = useAuth((state) => state.user?.siteRoot) || '/www/wwwroot';
  const [keyword, setKeyword] = useState('');
  const [addOpen, setAddOpen] = useState(false);
  const [pickingFile, setPickingFile] = useState(false);
  const [explorer, setExplorer] = useState<SQLiteFile | null>(null);
  const [backupTarget, setBackupTarget] = useState<SQLiteFile | null>(null);
  const [form] = Form.useForm();

  const listQuery = useQuery({
    queryKey: ['sqlite-files'],
    queryFn: ({ signal }) => sqliteFiles(signal),
  });

  const addMutation = useMutation({
    mutationFn: (values: { name?: string; path: string }) =>
      sqliteAddFile(values),
    onSuccess: () => {
      setAddOpen(false);
      form.resetFields();
      void client.invalidateQueries({ queryKey: ['sqlite-files'] });
      void message.success('SQLite 数据库已添加');
    },
  });

  const pickerInitialDir = () => {
    const current = String(form.getFieldValue('path') || '').trim();
    if (current.startsWith('/')) {
      const idx = current.lastIndexOf('/');
      return idx > 0 ? current.slice(0, idx) : '/';
    }
    return siteRoot;
  };

  const filteredFiles = (listQuery.data?.list ?? []).filter((row) => {
    const q = keyword.trim().toLowerCase();
    if (!q) return true;
    return (
      row.name.toLowerCase().includes(q) || row.path.toLowerCase().includes(q)
    );
  });

  return (
    <div style={{ position: 'relative', minHeight: 500 }}>
      <Card style={{ marginBottom: SPACING.lg }}>
        <Flex justify="space-between" align="center" gap={SPACING.md} wrap>
          <Space wrap>
            <Button type="primary" onClick={() => setAddOpen(true)}>
              添加数据库文件
            </Button>
          </Space>
          <Space wrap>
            <Input.Search
              allowClear
              placeholder="请输入数据库名或路径"
              onSearch={setKeyword}
              style={{ width: LAYOUT.appSearchWidth, maxWidth: '100%' }}
            />
            <Tooltip title="刷新">
              <Button
                aria-label="刷新"
                icon={<RotateCw size={16} strokeWidth={1.9} />}
                onClick={() => void listQuery.refetch()}
              />
            </Tooltip>
          </Space>
        </Flex>
      </Card>

      <Card>
        {listQuery.isError ? (
          <Alert
            type="error"
            showIcon
            message={listQuery.error?.message || '读取 SQLite 列表失败'}
            action={
              <Button size="small" onClick={() => void listQuery.refetch()}>
                重试
              </Button>
            }
          />
        ) : (
          <ContentSizedTable<SQLiteFile>
            rowKey="id"
            fillColumn="path"
            loading={listQuery.isFetching}
            dataSource={filteredFiles}
            locale={{
              emptyText: (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description="尚未添加本地 SQLite 数据库文件"
                />
              ),
            }}
            columns={[
              {
                title: '名称',
                dataIndex: 'name',
                width: 180,
                render: (v: string) => (
                  <Space>
                    <DatabaseIcon size={14} />
                    <Typography.Text strong>{v}</Typography.Text>
                  </Space>
                ),
              },
              {
                title: '路径',
                dataIndex: 'path',
                ellipsis: true,
                render: (v: string) => (
                  <Tooltip title={v}>
                    <Typography.Link
                      onClick={() =>
                        navigate(
                          `/files?path=${encodeURIComponent(parentDir(v))}`,
                        )
                      }
                    >
                      {v}
                    </Typography.Link>
                  </Tooltip>
                ),
              },
              {
                title: '备份',
                dataIndex: 'backupCount',
                width: 120,
                render: (v: number | undefined, row) => (
                  <Button
                    type="link"
                    style={{ padding: 0 }}
                    onClick={() => setBackupTarget(row)}
                  >
                    {v && v > 0 ? `${v}份备份` : '无备份'}
                  </Button>
                ),
              },
              {
                title: '状态',
                width: 90,
                render: (_, row) => (
                  <Badge
                    status={row.exists ? 'success' : 'error'}
                    text={row.exists ? '正常' : '缺失'}
                  />
                ),
              },
              {
                title: '大小',
                dataIndex: 'size',
                width: 105,
                render: (v: number, row) => (row.exists ? formatSize(v) : '—'),
              },
              {
                title: '操作',
                width: 130,
                render: (_, row) => (
                  <Space>
                    <Button
                      type="link"
                      style={{ padding: 0 }}
                      disabled={!row.exists}
                      onClick={() => setExplorer(row)}
                    >
                      管理
                    </Button>
                    <Button
                      type="link"
                      danger
                      style={{ padding: 0 }}
                      onClick={() =>
                        modal.confirm({
                          title: `移除 ${row.name}？`,
                          content:
                            '仅从面板列表中移除记录，不会删除服务器上的数据库文件。',
                          onOk: async () => {
                            await sqliteRemoveFile(row.id);
                            await listQuery.refetch();
                            void message.success('已从列表移除');
                          },
                        })
                      }
                    >
                      移除
                    </Button>
                  </Space>
                ),
              },
            ]}
          />
        )}
      </Card>

      {explorer && (
        <SQLiteExplorerModal
          file={explorer}
          onClose={() => setExplorer(null)}
        />
      )}

      {backupTarget && (
        <LocalBackupsModal
          title={`[${backupTarget.name}] 数据库备份`}
          queryKey={['sqlite-backups', backupTarget.id]}
          allowCreate={backupTarget.exists}
          createDisabledReason="本地数据库文件不存在，无法备份"
          fetchList={(signal) => sqliteBackups(backupTarget.id, signal)}
          onCreate={() => sqliteCreateBackup(backupTarget.id)}
          onDelete={(fileName) => sqliteDeleteBackup(backupTarget.id, fileName)}
          downloadUrl={(fileName) =>
            sqliteDownloadBackupUrl(backupTarget.id, fileName)
          }
          onChanged={() => void listQuery.refetch()}
          onClose={() => setBackupTarget(null)}
        />
      )}

      <Modal
        open={addOpen}
        centered
        title="添加SQLite数据库"
        onCancel={() => setAddOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={addMutation.isPending}
      >
        <Form
          form={form}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
          onFinish={(v) => addMutation.mutate(v)}
        >
          <Form.Item
            label="路径"
            required
            extra="仅支持服务器本地已存在的 SQLite 3 数据库文件（受保护系统目录除外）。"
          >
            <Space.Compact style={{ width: '100%' }}>
              <Form.Item
                name="path"
                noStyle
                rules={[
                  {
                    required: true,
                    message: '请选择或输入本地 SQLite 文件路径',
                  },
                ]}
              >
                <Input placeholder="请选择或输入本地 SQLite 文件路径" />
              </Form.Item>
              <Button
                aria-label="选择文件"
                icon={<FolderOpenOutlined />}
                onClick={() => setPickingFile(true)}
              />
            </Space.Compact>
          </Form.Item>
          <Form.Item name="name" label="名称">
            <Input placeholder="留空默认使用文件名" />
          </Form.Item>
        </Form>
      </Modal>

      {pickingFile && (
        <FilePicker
          title="选择 SQLite 数据库文件"
          initialPath={pickerInitialDir()}
          onSelect={(selectedPath) => {
            form.setFieldValue('path', selectedPath);
            setPickingFile(false);
          }}
          onClose={() => setPickingFile(false)}
        />
      )}
    </div>
  );
}
