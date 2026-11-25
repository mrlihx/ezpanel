import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Descriptions,
  Drawer,
  Input,
  Modal,
  Select,
  Segmented,
  Tag,
  Typography,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  clearLogs,
  downloadLogFile,
  listLogs,
  type Filters,
  type OpLog,
} from '../../api/oplog';
import { request } from '../../api/client';
import TaskDrawer from '../../components/TaskDrawer';
import { formatTime } from '../../utils/format';
import { moduleNames } from './shared';

const httpActions: Record<string, string> = {
  POST: '提交',
  PUT: '更新',
  PATCH: '修改',
  DELETE: '删除',
};

function operationResult(row: OpLog) {
  if (row.phase === 'submitted') return { text: '已提交', color: 'processing' };
  if (row.phase === 'skipped') return { text: '忙碌跳过', color: 'warning' };
  return row.success
    ? { text: '成功', color: 'success' }
    : { text: '失败', color: 'error' };
}

export default function AuditLogs({
  login = false,
  siteId,
}: {
  login?: boolean;
  siteId?: string;
}) {
  const { modal, message } = App.useApp();
  const [page, setPage] = useState(1),
    [size, setSize] = useState(10);
  const [filters, setFilters] = useState<Filters>({});
  const [selected, setSelected] = useState<OpLog>();
  const [task, setTask] = useState<string>();
  const [statistics, setStatistics] = useState(false);
  const current: Filters = {
    ...filters,
    kind: login ? 'login' : 'operation',
    ...(siteId ? { module: 'website', resourceId: siteId } : {}),
  };
  const query = useQuery({
    queryKey: ['oplogs', page, size, current],
    queryFn: () => listLogs(page, size, current),
    staleTime: 0,
    refetchOnMount: 'always',
  });
  const stats = useQuery({
    queryKey: ['oplog-stats', current],
    queryFn: () =>
      request<{ ip: string; count: number }[]>('/api/oplogs/stats', {
        params: current,
      }),
    enabled: statistics,
  });
  function filter(values: Partial<Filters>) {
    setFilters((previous) => ({ ...previous, ...values }));
    setPage(1);
  }
  function clear() {
    modal.confirm({
      title: '清空当前筛选范围的日志？',
      content: '删除后无法恢复。清理操作会另写入审计记录。',
      okText: '清空日志',
      okButtonProps: { danger: true },
      onOk: async () => {
        const result = await clearLogs(0, current);
        message.success(`已清理 ${result.deletedCount} 条记录`);
        setPage(1);
        void query.refetch();
      },
    });
  }
  return (
    <>
      <div className="logs-toolbar">
        <div className="logs-controls">
          <Button
            onClick={() => void query.refetch()}
            loading={query.isFetching}
          >
            刷新日志
          </Button>
          <Button
            onClick={() =>
              void downloadLogFile(
                '/api/oplogs/export',
                login ? '登录日志.csv' : '操作日志.csv',
                current,
              ).catch((error) => message.error(error.message))
            }
          >
            导出日志
          </Button>
          {!siteId && <Button onClick={clear}>清空日志</Button>}
          {!login && !siteId && (
            <Button onClick={() => setStatistics(true)}>IP操作统计</Button>
          )}
        </div>
        <div className="logs-controls">
          {login ? (
            <Segmented
              value={filters.success || 'all'}
              options={[
                { value: 'all', label: '全部' },
                { value: 'true', label: '登录成功' },
                { value: 'false', label: '登录失败' },
              ]}
              onChange={(value) =>
                filter({ success: value === 'all' ? undefined : String(value) })
              }
            />
          ) : (
            !siteId && (
              <Select
                aria-label="操作类型"
                placeholder="全部操作类型"
                allowClear
                style={{ width: 180 }}
                options={Object.entries(moduleNames).map(([value, label]) => ({
                  value,
                  label,
                }))}
                onChange={(module) => filter({ module })}
              />
            )
          )}
          <Input.Search
            className="logs-search"
            placeholder="请输入搜索关键字"
            allowClear
            onSearch={(keyword) => filter({ keyword })}
          />
        </div>
      </div>
      {query.error && (
        <Alert type="error" showIcon message={query.error.message} />
      )}
      <ContentSizedTable<OpLog>
        fillColumn={login ? 'userAgent' : 'target'}
        className={login ? undefined : 'audit-operation-table'}
        rowKey="id"
        size="middle"
        loading={query.isFetching}
        dataSource={query.data?.list}
        onRow={(row) => ({
          onClick: () => setSelected(row),
          style: { cursor: 'pointer' },
        })}
        pagination={{
          current: page,
          pageSize: size,
          total: query.data?.total,
          showSizeChanger: true,
          showTotal: (total) => `共 ${total} 条`,
          onChange: (p, s) => {
            setPage(p);
            setSize(s);
          },
        }}
        columns={
          login
            ? [
                {
                  title: 'IP地址（归属地）',
                  dataIndex: 'ip',
                  width: 220,
                  render: (value, row) =>
                    row.location ? `${value}（${row.location}）` : value || '—',
                },
                {
                  title: '用户代理',
                  dataIndex: 'userAgent',
                  ellipsis: true,
                  render: (value) => value || '—',
                },
                {
                  title: '登录状态',
                  dataIndex: 'success',
                  width: 130,
                  render: (value) => (
                    <Typography.Text
                      className="logs-status"
                      type={value ? 'success' : 'danger'}
                    >
                      {value ? '登录成功' : '登录失败'}
                    </Typography.Text>
                  ),
                },
                {
                  title: '时间',
                  dataIndex: 'createdAt',
                  width: 190,
                  render: (value) => (
                    <span className="logs-time">{formatTime(value)}</span>
                  ),
                },
              ]
            : [
                {
                  title: '操作时间',
                  dataIndex: 'createdAt',
                  render: (value) => (
                    <span className="logs-time">{formatTime(value)}</span>
                  ),
                },
                {
                  title: '用户 / 来源',
                  dataIndex: 'username',
                  width: 184,
                  render: (value, row) => (
                    <div className="audit-log-lines">
                      <span>{value || '未认证'}</span>
                      {row.ip && (
                        <Typography.Text type="secondary">
                          {row.ip}
                          {row.location ? `（${row.location}）` : ''}
                        </Typography.Text>
                      )}
                    </div>
                  ),
                },
                {
                  title: '操作类型',
                  dataIndex: 'module',
                  width: 144,
                  render: (value, row) => (
                    <div className="audit-log-lines">
                      <span>{moduleNames[value] || value}</span>
                      {siteId && (
                        <Typography.Text type="secondary">
                          {httpActions[row.action] || row.action}
                        </Typography.Text>
                      )}
                    </div>
                  ),
                },
                {
                  title: '详情',
                  dataIndex: 'target',
                  render: (target: string, row) => (
                    <div className="audit-log-lines">
                      <button
                        type="button"
                        className="audit-log-target"
                        onClick={(event) => {
                          event.stopPropagation();
                          setSelected(row);
                        }}
                        aria-label={
                          target ? `查看日志详情：${target}` : '查看日志详情'
                        }
                      >
                        {target?.split('\n').slice(0, 2).join(' · ') ||
                          '查看详情'}
                      </button>
                      {row.errorMsg && (
                        <Typography.Text
                          type="secondary"
                          className="audit-log-preview"
                        >
                          {row.errorMsg}
                        </Typography.Text>
                      )}
                    </div>
                  ),
                },
                {
                  title: '结果',
                  key: 'result',
                  render: (_, row) => {
                    const { text, color } = operationResult(row);
                    return (
                      <Tag color={color} className="audit-result">
                        {text}
                      </Tag>
                    );
                  },
                },
              ]
        }
      />
      <Drawer
        open={!!selected}
        onClose={() => setSelected(undefined)}
        title="日志详情"
        width="min(640px, 100vw)"
      >
        {selected && (
          <>
            <Descriptions
              column={1}
              styles={{
                content: {
                  overflowWrap: 'anywhere',
                  minWidth: 0,
                  whiteSpace: 'pre-wrap',
                },
              }}
              items={[
                {
                  key: 'user',
                  label: '用户',
                  children: selected.username || '未认证',
                },
                { key: 'ip', label: '来源IP', children: selected.ip || '—' },
                {
                  key: 'agent',
                  label: '用户代理',
                  children: selected.userAgent || '—',
                },
                {
                  key: 'action',
                  label: '操作类型',
                  children: moduleNames[selected.module] || selected.module,
                },
                {
                  key: 'target',
                  label: '详情',
                  children: selected.target || '—',
                },
                {
                  key: 'result',
                  label: '阶段 / 结果',
                  children: login
                    ? selected.success
                      ? '登录成功'
                      : '登录失败'
                    : [selected.phase, operationResult(selected).text]
                        .filter(Boolean)
                        .join(' · '),
                },
                {
                  key: 'error',
                  label: '错误摘要',
                  children: selected.errorMsg || '—',
                },
                {
                  key: 'time',
                  label: '时间',
                  children: formatTime(selected.createdAt),
                },
              ]}
            />
            {selected.taskId && (
              <Button onClick={() => setTask(selected.taskId!)}>
                查看关联任务
              </Button>
            )}
          </>
        )}
      </Drawer>
      <TaskDrawer
        taskId={task || null}
        open={!!task}
        title="关联任务"
        onClose={() => setTask(undefined)}
      />
      <Modal
        title="IP操作统计（当前筛选范围，前100项）"
        open={statistics}
        onCancel={() => setStatistics(false)}
        footer={null}
      >
        {stats.error && <Alert type="error" message={stats.error.message} />}
        <ContentSizedTable
          fillColumn="ip"
          rowKey="ip"
          size="small"
          loading={stats.isFetching}
          dataSource={stats.data}
          columns={[
            {
              title: 'IP地址',
              dataIndex: 'ip',
              render: (value) => value || '系统任务',
            },
            { title: '操作次数', dataIndex: 'count', width: 120 },
          ]}
          pagination={{ pageSize: 10 }}
        />
      </Modal>
    </>
  );
}
