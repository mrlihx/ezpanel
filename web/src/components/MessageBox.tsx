import { useEffect, useRef, useState } from 'react';
import {
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query';
import { Alert, Button, Empty, Flex, Grid, Modal, Spin, Tabs, Tag } from 'antd';
import { RotateCw } from 'lucide-react';
import ContentSizedTable from './ContentSizedTable';
import { listTasks, type TaskSummary } from '../api/task';
import { TaskContent } from './TaskDrawer';
import { useMessageBox } from '../stores/messageBox';
import { LAYOUT, SPACING } from '../styles/tokens';
import { formatTime } from '../utils/format';
import './MessageBox.css';
const actions: Record<string, string> = {
  install: '安装',
  update: '更新',
  uninstall: '卸载',
  switch: '切换版本',
  'rsync-send': 'Rsync 发送',
};
const applications: Record<string, string> = {
  nginx: 'Nginx',
  mysql: 'MySQL',
  mariadb: 'MariaDB',
  redis: 'Redis',
  docker: 'Docker',
  'mysql-client': 'MySQL客户端',
};
const statuses = {
  running: { label: '执行中', color: 'processing' },
  success: { label: '成功', color: 'success' },
  failed: { label: '失败', color: 'error' },
  interrupted: { label: '已中断', color: 'warning' },
};
export default function MessageBox({
  running,
}: {
  running: UseQueryResult<{ list: TaskSummary[]; total: number }, Error>;
}) {
  const box = useMessageBox();
  const client = useQueryClient();
  const { md } = Grid.useBreakpoint();
  const [page, setPage] = useState(1);
  const [logTaskId, setLogTaskId] = useState<string>();
  const [notified] = useState(() => new Set<string>());
  const previousRunning = useRef(new Set<string>());
  const messages = useQuery({
    queryKey: ['software-tasks', 'finished', page],
    queryFn: ({ signal }) =>
      listTasks({ module: 'software', status: 'finished', page }, signal),
    enabled: box.open,
    staleTime: 0,
  });
  const history = useQuery({
    queryKey: ['software-tasks', 'history'],
    queryFn: ({ signal }) =>
      listTasks(
        { module: 'software', status: 'finished', page_size: 1 },
        signal,
      ),
    enabled: box.open && box.tab === 'logs',
  });
  const refreshApplications = () => {
    for (const key of [
      'app-sources',
      'software-catalog',
      'sysinfo-summary',
      'website-env',
      'website-php-versions',
      'runtime-environments',
      'database-instances',
      'db-env',
      'db-servers',
      'nosql-env',
      'nosql-connections',
      'php-extensions',
      'php-config',
      'clamav-visual',
      'clamav-history',
      'clamav-quarantine',
    ])
      void client.invalidateQueries({ queryKey: [key] });
    void client.invalidateQueries({ queryKey: ['software-tasks'] });
  };
  useEffect(() => {
    if (!running.data) return;
    const current = new Set(running.data.list.map((task) => task.taskId));
    const finished = [...previousRunning.current].some(
      (id) => !current.has(id),
    );
    previousRunning.current = current;
    if (finished) {
      for (const key of [
        'app-sources',
        'software-catalog',
        'sysinfo-summary',
        'website-env',
        'website-php-versions',
        'runtime-environments',
        'database-instances',
        'db-env',
        'db-servers',
        'nosql-env',
        'nosql-connections',
        'php-extensions',
        'php-config',
        'clamav-visual',
        'clamav-history',
        'clamav-quarantine',
        'software-tasks',
      ])
        void client.invalidateQueries({ queryKey: [key] });
    }
  }, [running.data, client]);
  const columns = (showLog: (taskId: string) => void) => [
    {
      title: '应用',
      dataIndex: 'target',
      render: (value: string) => applications[value] ?? value,
    },
    {
      title: '操作',
      dataIndex: 'action',
      render: (value: string) => actions[value] ?? value,
    },
    {
      title: '状态',
      dataIndex: 'status',
      render: (value: TaskSummary['status']) => (
        <Tag color={statuses[value].color}>{statuses[value].label}</Tag>
      ),
    },
    {
      title: '开始时间',
      dataIndex: 'startedAt',
      render: (value: string) => formatTime(value),
    },
    {
      title: '耗时',
      dataIndex: 'durationMs',
      render: (value: number | null) => {
        if (value == null) return '—';
        if (value < 1000) return value + ' 毫秒';
        if (value < 60000) return (value / 1000).toFixed(1) + ' 秒';
        const seconds = Math.floor(value / 1000);
        return Math.floor(seconds / 60) + ' 分 ' + (seconds % 60) + ' 秒';
      },
    },
    {
      title: '操作',
      key: 'view',
      render: (_: unknown, row: TaskSummary) => (
        <Button type="link" onClick={() => showLog(row.taskId)}>
          执行日志
        </Button>
      ),
    },
  ];
  const [logRefresh, setLogRefresh] = useState(0);
  const taskId = box.taskId ?? history.data?.list[0]?.taskId;
  return (
    <>
      <Modal
        title="消息盒子"
        open={box.open}
        centered
        className="panel-message-box"
        onCancel={() => {
          setLogTaskId(undefined);
          box.close();
        }}
        footer={null}
        width={LAYOUT.messageBoxWidth}
        styles={{
          body: {
            height: `min(${LAYOUT.messageBoxHeight}, max(0px, calc(100dvh - 116px - env(safe-area-inset-top, 0px) - env(safe-area-inset-bottom, 0px))))`,
          },
        }}
        destroyOnHidden
      >
        <Tabs
          className="message-box-tabs"
          activeKey={box.tab}
          onChange={box.selectTab}
          tabPosition={md ? 'left' : 'top'}
          items={[
            {
              key: 'tasks',
              label: `任务列表(${running.data?.total ?? '—'})`,
              children: (
                <div className="message-box-list-pane">
                  {running.error && (
                    <Alert
                      type="error"
                      message={running.error.message}
                      action={
                        <Button onClick={() => void running.refetch()}>
                          重试
                        </Button>
                      }
                    />
                  )}
                  {running.data?.list.length ? (
                    <ContentSizedTable<TaskSummary>
                      fillColumn="target"
                      rowKey="taskId"
                      size="small"
                      columns={columns(box.show)}
                      dataSource={running.data.list}
                      pagination={false}
                    />
                  ) : (
                    !running.error && (
                      <Flex
                        align="center"
                        justify="center"
                        style={{ height: '100%' }}
                      >
                        {running.isPending ? (
                          <Spin />
                        ) : (
                          <Empty description="当前没有任务执行" />
                        )}
                      </Flex>
                    )
                  )}
                </div>
              ),
            },
            {
              key: 'messages',
              label: `消息列表(${messages.data?.total ?? '—'})`,
              children: (
                <div className="message-box-list-pane">
                  {messages.error && (
                    <Alert
                      type="error"
                      message={messages.error.message}
                      action={
                        <Button onClick={() => void messages.refetch()}>
                          重试
                        </Button>
                      }
                    />
                  )}
                  <ContentSizedTable<TaskSummary>
                    fillColumn="target"
                    rowKey="taskId"
                    size="small"
                    columns={columns(setLogTaskId)}
                    loading={messages.isPending}
                    dataSource={messages.data?.list}
                    pagination={{
                      current: page,
                      pageSize: 10,
                      total: messages.data?.total,
                      showSizeChanger: false,
                      onChange: setPage,
                    }}
                    locale={{
                      emptyText: <Empty description="暂无应用任务消息" />,
                    }}
                  />
                </div>
              ),
            },
            {
              key: 'logs',
              label: '执行日志',
              children: (
                <Flex
                  vertical
                  gap={SPACING.md}
                  className="message-box-log-pane"
                >
                  <Button
                    icon={<RotateCw size={15} strokeWidth={1.9} />}
                    loading={history.isFetching}
                    style={{ alignSelf: 'flex-start', flexShrink: 0 }}
                    onClick={() => {
                      box.selectTab('logs');
                      setLogRefresh((count) => count + 1);
                      void history.refetch();
                    }}
                  >
                    刷新日志
                  </Button>
                  {history.error && (
                    <Alert type="error" message={history.error.message} />
                  )}
                  {taskId ? (
                    <TaskContent
                      key={`${taskId}-${logRefresh}`}
                      taskId={taskId}
                      notified={notified}
                      onSettled={refreshApplications}
                      logsOnly
                      fit
                    />
                  ) : (
                    !history.isPending && <Empty description="暂无执行日志" />
                  )}
                </Flex>
              ),
            },
          ]}
        />
        <Modal
          title="执行日志"
          open={!!logTaskId}
          onCancel={() => setLogTaskId(undefined)}
          footer={null}
          width={LAYOUT.messageBoxWidth}
          centered
          destroyOnHidden
        >
          {logTaskId && (
            <Flex
              vertical
              style={{
                height: `min(${LAYOUT.messageBoxHeight}, calc(100dvh - 100px))`,
              }}
            >
              <TaskContent key={logTaskId} taskId={logTaskId} logsOnly fit />
            </Flex>
          )}
        </Modal>
      </Modal>
    </>
  );
}
