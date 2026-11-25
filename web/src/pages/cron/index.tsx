import { useEffect, useState, type Key } from 'react';
import {
  Alert,
  App,
  Button,
  Card,
  Dropdown,
  Flex,
  Modal,
  Select,
  Space,
  Tag,
  Checkbox,
  Popover,
  theme,
  type TableColumnsType,
  Tooltip,
  Typography,
  Grid,
} from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { subscribeTaskChanges } from '../../api/task';
import { useNavigate } from 'react-router-dom';
import { Input } from 'antd';
import { Pause, Play, RotateCw, Settings } from 'lucide-react';
import ContentSizedTable from '../../components/ContentSizedTable';
import { request } from '../../api/client';
import { cronTypes, getCron, listCrons, type Cron } from '../../api/cron';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';
import CronForm from './CronForm';
import LogsDrawer from './LogsDrawer';
import './cron.css';

type StatusFilter = 'enabled' | 'disabled' | 'failed';

export default function CronPage() {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const columnOptions = [
    { value: 'name', label: '任务名称' },
    { value: 'type', label: '类型' },
    { value: 'status', label: '状态' },
    { value: 'backupTarget', label: '备份到' },
    { value: 'backupPath', label: '备份路径' },
    { value: 'schedule', label: '执行周期' },
    { value: 'lastRun', label: '上次执行' },
    { value: 'actions', label: '操作', disabled: true },
  ];
  const columnsQuery = useQuery({
    queryKey: ['cron-columns'],
    queryFn: () => request<{ columns: string[] }>('/api/crons/columns'),
  });
  const visible =
    columnsQuery.data?.columns ?? columnOptions.map((item) => item.value);
  const [columnsSaving, setColumnsSaving] = useState(false);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [keyword, setKeyword] = useState('');
  const [keywordInput, setKeywordInput] = useState('');
  const [type, setType] = useState<string>();
  const [status, setStatus] = useState<StatusFilter>();
  const [selected, setSelected] = useState<Key[]>([]);
  const [editing, setEditing] = useState<Cron>();
  const [logs, setLogs] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [creating, setCreating] = useState(false);
  const query = useQuery({
    queryKey: ['crons', page, size, keyword, type, status],
    queryFn: () => listCrons(page, size, keyword, type, status),
  });
  useEffect(
    () =>
      subscribeTaskChanges('cron', () => {
        void client.invalidateQueries({ queryKey: ['crons'] });
      }),
    [client],
  );
  const refresh = () => {
    void query.refetch();
  };
  const action = async (id: string, kind: string, reload = true) => {
    if (kind === 'delete') {
      await request(`/api/crons/${id}`, { method: 'DELETE' });
      client.setQueryData<{ list: Cron[]; total: number }>(
        ['crons', page, size, keyword, type, status],
        (old) =>
          old
            ? {
                ...old,
                list: old.list.filter((item) => item.id !== id),
                total: Math.max(0, old.total - 1),
              }
            : old,
      );
      return;
    } else {
      await request<{ runId?: string }>(`/api/crons/${id}/${kind}`, {
        method: 'POST',
      });
      if (kind === 'run' && reload) setLogs(id);
    }
    if (reload) refresh();
  };
  const batch = async (kind: string) => {
    setBusy(true);
    let successes = 0;
    const failures: string[] = [];
    for (const key of selected) {
      try {
        await action(String(key), kind, false);
        successes++;
      } catch (e) {
        failures.push(
          `${query.data?.list.find((r) => r.id === key)?.name ?? key}：${e instanceof Error ? e.message : '失败'}`,
        );
      }
    }
    setBusy(false);
    setSelected([]);
    modal.info({
      title: `批量处理完成：成功 ${successes}，失败 ${failures.length}`,
      content: failures.map((text) => <div key={text}>{text}</div>),
    });
    refresh();
  };
  const remove = (r: Cron) =>
    modal.confirm({
      title: `删除计划「${r.name}」？`,
      content: '删除关联执行记录与脚本，已生成备份保留；运行中不可删除。',
      onOk: () => action(r.id, 'delete'),
    });
  const toggle = (row: Cron) =>
    modal.confirm({
      title: `${row.enabled ? '停用' : '启用'}计划「${row.name}」？`,
      content: row.enabled
        ? '停用后不再自动调度，正在执行的任务不受影响。'
        : '确认启用后，将按照执行周期自动运行。',
      onOk: () => action(row.id, row.enabled ? 'disable' : 'enable'),
    });
  const duplicate = async (row: Cron) => {
    const source = await getCron(row.id);
    setEditing({ ...source, id: '', name: `${source.name.slice(0, 61)}副本` });
  };
  const columns: TableColumnsType<Cron> = [
    {
      key: 'name',
      title: '任务名称',
      dataIndex: 'name',
      width: 220,
      ellipsis: true,
    },
    {
      key: 'type',
      title: '类型',
      width: 130,
      render: (_, r) => (
        <Tag>{cronTypes.find((t) => t.value === r.type)?.label ?? r.type}</Tag>
      ),
    },
    {
      key: 'status',
      title: '状态',
      width: 100,
      render: (_, row) => (
        <Tooltip
          title={row.disabledReason || `点击${row.enabled ? '停用' : '启用'}`}
        >
          <Button
            type="link"
            className="cron-status"
            style={{
              color: row.enabled ? token.colorSuccess : token.colorError,
            }}
            onClick={() => toggle(row)}
            iconPosition="end"
            icon={
              row.enabled ? (
                <Play size={12} strokeWidth={2.2} fill="currentColor" />
              ) : (
                <Pause size={12} strokeWidth={2.2} />
              )
            }
          >
            {row.scheduleInvalid ? '调度无效' : row.enabled ? '正常' : '停用'}
          </Button>
        </Tooltip>
      ),
    },
    {
      key: 'backupTarget',
      title: '备份到',
      dataIndex: 'backupTarget',
      render: (target: string) => target || '—',
    },
    {
      key: 'backupPath',
      title: '备份路径',
      dataIndex: 'backupPath',
      width: 240,
      ellipsis: true,
      render: (path: string) =>
        path ? (
          <Tooltip title={path}>
            <Typography.Link
              onClick={() =>
                navigate(`/files?path=${encodeURIComponent(path)}`)
              }
            >
              {path}
            </Typography.Link>
          </Tooltip>
        ) : (
          '—'
        ),
    },
    {
      key: 'schedule',
      title: '执行周期',
      width: 180,
      render: (_, r) => (
        <Tooltip title={`${r.schedule} · ${r.timezone}`}>
          <span>{r.scheduleDesc}</span>
        </Tooltip>
      ),
    },
    {
      key: 'lastRun',
      title: '上次执行',
      width: 240,
      render: (_, r) => (
        <>
          {r.lastRunAt ? formatTime(r.lastRunAt) : '尚未执行'}{' '}
          {r.lastStatus && (
            <Tag
              color={
                r.lastStatus === 'success'
                  ? 'success'
                  : r.lastStatus === 'running'
                    ? 'processing'
                    : 'error'
              }
            >
              {{
                success: '成功',
                running: '执行中',
                failed: '失败',
                interrupted: '中断',
              }[r.lastStatus] ?? r.lastStatus}
            </Tag>
          )}
        </>
      ),
    },
    {
      key: 'actions',
      title: '操作',
      width: 240,
      fixed: fixedActions,
      render: (_, row) => (
        <Space className="cron-row-actions" size={12}>
          <Button
            type="link"
            disabled={row.lastStatus === 'running'}
            onClick={() => void action(row.id, 'run').catch(() => {})}
          >
            执行
          </Button>
          <Button
            type="link"
            disabled={row.lastStatus === 'running'}
            onClick={() =>
              void getCron(row.id)
                .then(setEditing)
                .catch(() => {})
            }
          >
            编辑
          </Button>
          <Button type="link" onClick={() => setLogs(row.id)}>
            日志
          </Button>
          <Button
            type="link"
            onClick={() => void duplicate(row).catch(() => {})}
          >
            复制
          </Button>
          <Button
            type="link"
            danger
            disabled={row.lastStatus === 'running'}
            onClick={() => remove(row)}
          >
            删除
          </Button>
        </Space>
      ),
    },
  ];
  return (
    <>
      <Card className="cron-list-card" title="任务列表">
        <Flex
          className="cron-toolbar"
          justify="space-between"
          gap={SPACING.md}
          wrap
          style={{ marginBottom: SPACING.lg }}
        >
          <Space wrap>
            <Button type="primary" onClick={() => setCreating(true)}>
              添加任务
            </Button>
            <Dropdown
              disabled={!selected.length || busy}
              menu={{
                items: [
                  { key: 'enable', label: '启用选中任务' },
                  { key: 'disable', label: '停用选中任务' },
                  { key: 'run', label: '执行选中任务' },
                  { key: 'delete', label: '删除选中任务', danger: true },
                ],
                onClick: ({ key }) => {
                  if (key === 'delete')
                    modal.confirm({
                      title: `删除选中的 ${selected.length} 个任务？`,
                      content: '备份文件保留，运行中的任务不会删除。',
                      onOk: () => batch(key),
                    });
                  else void batch(key);
                },
              }}
            >
              <Button loading={busy} disabled={!selected.length}>
                批量操作{selected.length ? ` (${selected.length})` : ''}
              </Button>
            </Dropdown>
          </Space>
          <Space wrap>
            <Select
              allowClear
              placeholder="全部类型"
              style={{ width: 132, maxWidth: '100%' }}
              options={cronTypes}
              value={type}
              onChange={(value) => {
                setType(value);
                setPage(1);
              }}
            />
            <Select
              allowClear
              placeholder="全部状态"
              style={{ width: 120, maxWidth: '100%' }}
              options={[
                { value: 'enabled', label: '已启用' },
                { value: 'disabled', label: '已停用' },
                { value: 'failed', label: '上次失败' },
              ]}
              value={status}
              onChange={(value) => {
                setStatus(value);
                setPage(1);
              }}
            />
            <Input.Search
              placeholder="搜索任务名称"
              allowClear
              value={keywordInput}
              onChange={(event) => {
                setKeywordInput(event.target.value);
                if (!event.target.value) {
                  setKeyword('');
                  setPage(1);
                }
              }}
              onSearch={(value) => {
                setKeyword(value);
                setPage(1);
              }}
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
              placement="bottomRight"
              title="显示列"
              content={
                <Space direction="vertical">
                  {columnsQuery.error && (
                    <Alert type="error" message={columnsQuery.error.message} />
                  )}
                  <Checkbox.Group
                    value={visible}
                    options={columnOptions}
                    disabled={
                      columnsSaving ||
                      columnsQuery.isPending ||
                      columnsQuery.isError
                    }
                    style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
                    onChange={async (values) => {
                      setColumnsSaving(true);
                      try {
                        const result = await request<{ columns: string[] }>(
                          '/api/crons/columns',
                          {
                            method: 'PUT',
                            data: {
                              columns: Array.from(
                                new Set([...values, 'actions']),
                              ),
                            },
                          },
                        );
                        client.setQueryData(['cron-columns'], result);
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
        {query.error && (
          <Alert
            type="error"
            message={
              query.error instanceof Error ? query.error.message : '读取失败'
            }
          />
        )}
        <ContentSizedTable<Cron>
          fillColumn="name"
          growColumns={['backupPath']}
          rowKey="id"
          size="middle"
          loading={query.isFetching}
          dataSource={query.data?.list}
          rowSelection={{ selectedRowKeys: selected, onChange: setSelected }}
          pagination={{
            current: page,
            pageSize: size,
            total: query.data?.total,
            showSizeChanger: true,
            onChange: (p, s) => {
              setPage(p);
              setSize(s);
              setSelected([]);
            },
          }}
          columns={columns.filter(
            (column) =>
              column.key === 'actions' || visible.includes(String(column.key)),
          )}
        />
      </Card>
      <Modal
        open={creating || !!editing}
        title={
          editing?.id
            ? '编辑计划任务'
            : editing
              ? '复制计划任务'
              : '添加计划任务'
        }
        width={LAYOUT.logDrawerWidth}
        centered
        styles={{
          content: {
            display: 'flex',
            flexDirection: 'column',
            maxHeight: `calc(100dvh - ${SPACING.xl * 2}px)`,
          },
          header: { flexShrink: 0 },
          body: { display: 'flex', minHeight: 0, overflow: 'hidden' },
        }}
        footer={null}
        onCancel={() => {
          setEditing(undefined);
          setCreating(false);
        }}
        destroyOnHidden
      >
        {(creating || editing) && (
          <CronForm
            key={editing?.id || 'new'}
            initial={editing}
            onCancel={() => {
              setEditing(undefined);
              setCreating(false);
            }}
            onSaved={() => {
              setEditing(undefined);
              setCreating(false);
              refresh();
              message.success(editing?.id ? '后续调度已更新' : '任务已添加');
            }}
          />
        )}
      </Modal>
      {logs && (
        <LogsDrawer
          key={logs}
          id={logs}
          onRun={() => action(logs, 'run')}
          onClose={() => {
            setLogs(undefined);
            refresh();
          }}
        />
      )}
    </>
  );
}
