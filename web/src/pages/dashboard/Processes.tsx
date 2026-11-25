import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Descriptions,
  Drawer,
  Flex,
  Input,
  Segmented,
  Skeleton,
  Space,
  Tooltip,
  Typography,
  Grid,
} from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  processDetail,
  processes,
  type MonitorProcessFilter,
  type Process,
} from '../../api/sysinfo';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { bytes, number } from './format';
import useProcessTermination from './useProcessTermination';

export default function Processes({
  live,
  onFilter,
}: {
  live: boolean;
  onFilter: (filter: MonitorProcessFilter) => void;
}) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const [sort, setSort] = useState<'cpu' | 'mem'>('cpu');
  const [keyword, setKeyword] = useState('');
  const [selected, setSelected] = useState<Process | null>(null);
  useEffect(() => {
    onFilter({ sort, keyword });
  }, [sort, keyword, onFilter]);
  const list = useQuery({
    queryKey: ['processes', sort, keyword],
    queryFn: ({ signal }) => processes(sort, keyword, signal),
    enabled: !live,
  });
  const detail = useQuery({
    queryKey: ['process-detail', selected?.pid, selected?.started],
    queryFn: ({ signal }) =>
      processDetail(selected!.pid, selected!.started, signal),
    enabled: selected !== null,
  });
  const terminateButton = useProcessTermination(() => setSelected(null));
  const data = detail.data;
  return (
    <Space direction="vertical" size={SPACING.lg} style={{ width: '100%' }}>
      <Flex justify="space-between" gap={SPACING.md} wrap>
        <Input.Search
          placeholder="搜索 PID、名称或用户"
          aria-label="搜索进程"
          allowClear
          maxLength={256}
          onChange={(event) => {
            if (!event.target.value) setKeyword('');
          }}
          onSearch={(value) => setKeyword(value.trim())}
          style={{
            width: LAYOUT.dashboardProcessSearchWidth,
            maxWidth: '100%',
          }}
        />
        <Space size={SPACING.sm} wrap>
          <Segmented
            options={[
              { label: 'CPU 排序', value: 'cpu' },
              { label: '内存排序', value: 'mem' },
            ]}
            value={sort}
            onChange={setSort}
          />
          <Button loading={list.isFetching} onClick={() => void list.refetch()}>
            刷新
          </Button>
        </Space>
      </Flex>
      {list.isError && (
        <Alert
          type="error"
          message="进程列表更新失败"
          description={list.error.message}
          action={<Button onClick={() => void list.refetch()}>重试</Button>}
        />
      )}
      <ContentSizedTable<Process>
        fillColumn="name"
        size="small"
        rowKey={(process) => `${process.pid}:${process.started}`}
        loading={list.isPending}
        pagination={false}
        scroll={{ y: LAYOUT.taskLogHeight }}
        dataSource={list.data}
        columns={[
          { title: 'PID', dataIndex: 'pid', align: 'right' },
          {
            title: '名称',
            dataIndex: 'name',
            ellipsis: true,
            render: (value: string) => value || '--',
          },
          {
            title: '用户',
            dataIndex: 'user',
            render: (value: string) => value || '--',
          },
          {
            title: 'CPU',
            dataIndex: 'cpu',
            align: 'right',
            render: (value: number | null) => number(value, '%'),
          },
          {
            title: '占用内存',
            dataIndex: 'memoryBytes',
            align: 'right',
            render: (value: number | null) => bytes(value),
          },
          {
            title: '内存占比',
            dataIndex: 'mem',
            align: 'right',
            render: (value: number | null) => number(value, '%'),
          },
          {
            title: '状态',
            dataIndex: 'status',
            render: (value: string[] | null, process) => (
              <Space direction="vertical" size={0}>
                <Typography.Text>{value?.join(', ') || '--'}</Typography.Text>
                {process.protected && (
                  <Tooltip title={process.protectedReason}>
                    <Typography.Text type="secondary">受保护</Typography.Text>
                  </Tooltip>
                )}
              </Space>
            ),
          },
          {
            title: '操作',
            fixed: fixedActions,
            render: (_, process) => (
              <Space size={0}>
                <Button
                  type="link"
                  size="small"
                  onClick={() => setSelected(process)}
                >
                  详情
                </Button>
                {terminateButton(process)}
              </Space>
            ),
          },
        ]}
      />
      <Typography.Text type="secondary">
        按所选指标降序显示，搜索后最多返回 100 个进程。受保护进程不可终止。
      </Typography.Text>
      <Drawer
        title={selected ? `进程详情 · PID ${selected.pid}` : '进程详情'}
        open={selected !== null}
        onClose={() => setSelected(null)}
        width={LAYOUT.drawerWidth}
        extra={data && terminateButton(data)}
      >
        {detail.isPending ? (
          <Skeleton active />
        ) : detail.isError ? (
          <Alert
            type="error"
            message="进程详情加载失败"
            description={detail.error.message}
            action={<Button onClick={() => void detail.refetch()}>重试</Button>}
          />
        ) : data ? (
          <Descriptions
            column={1}
            size="small"
            styles={{
              content: { overflowWrap: 'anywhere', whiteSpace: 'pre-wrap' },
            }}
            items={[
              ['名称', data.name || '--'],
              ['PID / 父 PID', `${data.pid} / ${data.ppid}`],
              ['用户', data.user || '--'],
              ['CPU', number(data.cpu, '%')],
              ['内存', `${bytes(data.memoryBytes)} · ${number(data.mem, '%')}`],
              ['状态', data.status?.join(', ') || '--'],
              [
                '启动时间',
                data.startedAt
                  ? new Date(data.startedAt).toLocaleString()
                  : '--',
              ],
              ['线程数', data.threads ?? '--'],
              ['可执行文件', data.exe || '--'],
              ['启动命令', data.command || '--'],
              ['工作目录', data.cwd || '--'],
              [
                '终止保护',
                data.protected
                  ? data.protectedReason || '关键进程，禁止终止'
                  : data.started <= 0
                    ? '启动时间不可用，禁止终止'
                    : '可发送 SIGTERM',
              ],
            ].map(([label, children]) => ({
              key: String(label),
              label,
              children,
            }))}
          />
        ) : null}
      </Drawer>
    </Space>
  );
}
