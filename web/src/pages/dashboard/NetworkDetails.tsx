import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Descriptions,
  Drawer,
  Flex,
  Input,
  Skeleton,
  Space,
  Tooltip,
  Typography,
  Grid,
} from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  networkProcessDetail,
  networkProcesses,
  type MonitorNetworkFeed,
  type MonitorNetworkFilter,
  type NetworkConnection,
  type NetworkProcess,
} from '../../api/sysinfo';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { bytes } from './format';

const rate = (value: number | null) =>
  value == null ? '--' : `${bytes(value)}/s`;

export default function NetworkDetails({
  live,
  onFilter,
  feed,
}: {
  live: boolean;
  onFilter: (filter: MonitorNetworkFilter) => void;
  feed?: MonitorNetworkFeed;
}) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const [keyword, setKeyword] = useState('');
  const [pagination, setPagination] = useState({ page: 1, pageSize: 20 });
  const [selected, setSelected] = useState<NetworkProcess | null>(null);
  const [detailPagination, setDetailPagination] = useState({
    page: 1,
    pageSize: 50,
  });
  const pid = selected?.pid ?? 0;
  const started = selected?.started ?? 0;
  useEffect(() => {
    onFilter({
      keyword,
      page: pagination.page,
      pageSize: pagination.pageSize,
      pid,
      started,
      detailPage: detailPagination.page,
      detailPageSize: detailPagination.pageSize,
    });
  }, [onFilter, keyword, pagination, pid, started, detailPagination]);
  const list = useQuery({
    queryKey: ['network-processes', keyword, pagination],
    queryFn: ({ signal }) =>
      networkProcesses(keyword, pagination.page, pagination.pageSize, signal),
    enabled: !live,
  });
  const detail = useQuery({
    queryKey: [
      'network-process-detail',
      selected?.pid,
      selected?.started,
      detailPagination,
    ],
    queryFn: ({ signal }) =>
      networkProcessDetail(
        selected!.pid,
        selected!.started,
        detailPagination.page,
        detailPagination.pageSize,
        signal,
      ),
    enabled: selected !== null && !live,
  });
  const search = (value: string) => {
    setKeyword(value.trim());
    setPagination((previous) => ({ ...previous, page: 1 }));
  };
  const data = detail.data;
  const matches =
    live &&
    feed &&
    feed.filter.keyword === keyword &&
    feed.filter.page === pagination.page &&
    feed.filter.pageSize === pagination.pageSize;
  const matchDetail =
    matches &&
    feed.filter.pid === pid &&
    feed.filter.started === started &&
    feed.filter.detailPage === detailPagination.page &&
    feed.filter.detailPageSize === detailPagination.pageSize;
  const listError =
    (matches && feed.error) || (live ? undefined : list.error?.message);
  const detailError =
    (matchDetail && (feed.detailError || feed.error)) ||
    (live ? undefined : detail.error?.message);

  return (
    <Space direction="vertical" size={SPACING.lg} style={{ width: '100%' }}>
      <Flex justify="space-between" align="center" gap={SPACING.md} wrap>
        <Input.Search
          placeholder="搜索 PID、进程名称、IP 或端口"
          aria-label="搜索网络进程"
          allowClear
          maxLength={256}
          onSearch={search}
          onChange={(event) => {
            if (!event.target.value) search('');
          }}
          style={{
            width: LAYOUT.dashboardProcessSearchWidth,
            maxWidth: '100%',
          }}
        />
        <Button loading={list.isFetching} onClick={() => void list.refetch()}>
          刷新
        </Button>
      </Flex>
      <Typography.Text type="secondary">
        连接数与 IP 数来自当前可见连接；速率仅统计可连续采样的 TCP 连接，单位
        B/s（按 KiB、MiB 换算）。UDP、首次采样或无法读取计数时显示 --。
      </Typography.Text>
      {list.data?.notice && <Alert type="info" message={list.data.notice} />}
      {listError && (
        <Alert
          type="error"
          message={
            list.data ? '网络详情更新失败，显示上次数据' : '网络详情加载失败'
          }
          description={listError}
          action={<Button onClick={() => void list.refetch()}>重试</Button>}
        />
      )}
      <ContentSizedTable<NetworkProcess>
        fillColumn="name"
        growColumns={['listenIPs']}
        size="small"
        rowKey={(process) => `${process.pid}:${process.started}`}
        loading={list.isPending && !listError}
        dataSource={list.data?.list}
        scroll={{ y: LAYOUT.taskLogHeight }}
        pagination={
          list.data
            ? {
                current: pagination.page,
                pageSize: pagination.pageSize,
                total: list.data?.total,
                showSizeChanger: true,
                pageSizeOptions: [20, 50, 100],
                showTotal: (total) => `共 ${total} 个进程`,
                onChange: (page, pageSize) => setPagination({ page, pageSize }),
              }
            : false
        }
        columns={[
          {
            title: 'PID',
            dataIndex: 'pid',
            align: 'right',
            render: (value: number) => (
              <Tooltip title={value > 0 ? undefined : '无法读取所属进程 PID'}>
                {value > 0 ? value : '--'}
              </Tooltip>
            ),
          },
          {
            title: '名称',
            dataIndex: 'name',
            ellipsis: true,
            className: 'network-process-name',
            render: (value: string) => (
              <Tooltip title={value || '进程名称不可读取'}>
                {value || '--'}
              </Tooltip>
            ),
          },
          {
            title: '监听 IP',
            dataIndex: 'listenIPs',
            ellipsis: true,
            className: 'network-process-ips',
            render: (value: string[]) => (
              <Tooltip
                title={value.length ? value.join(', ') : '未发现监听地址'}
              >
                {value.join(', ') || '--'}
              </Tooltip>
            ),
          },
          {
            title: '端口',
            dataIndex: 'ports',
            render: (value: string[]) => (
              <Tooltip title={value.length ? undefined : '未发现可见端口'}>
                {value.join(', ') || '--'}
              </Tooltip>
            ),
          },
          { title: 'IP 数', dataIndex: 'ipCount', align: 'right' },
          { title: '连接数', dataIndex: 'connectionCount', align: 'right' },
          {
            title: '上传速率',
            dataIndex: 'upload',
            align: 'right',
            render: (value: number | null, process) => (
              <Tooltip title={process.rateNote}>{rate(value)}</Tooltip>
            ),
          },
          {
            title: '下载速率',
            dataIndex: 'download',
            align: 'right',
            render: (value: number | null, process) => (
              <Tooltip title={process.rateNote}>{rate(value)}</Tooltip>
            ),
          },
          {
            title: '操作',
            fixed: fixedActions,
            render: (_, process) => (
              <Tooltip
                title={
                  process.pid > 0 && process.started > 0
                    ? undefined
                    : '无法确认 PID 或启动时间，不能查看进程详情'
                }
              >
                <span>
                  <Button
                    type="link"
                    size="small"
                    disabled={process.pid <= 0 || process.started <= 0}
                    onClick={() => {
                      setDetailPagination({ page: 1, pageSize: 50 });
                      setSelected(process);
                    }}
                  >
                    详情
                  </Button>
                </span>
              </Tooltip>
            ),
          },
        ]}
      />
      {list.data?.sampledAt && (
        <Typography.Text type="secondary">
          采样时间：{new Date(list.data.sampledAt).toLocaleString()}
        </Typography.Text>
      )}
      <Drawer
        title={selected ? `网络详情 · PID ${selected.pid}` : '网络详情'}
        open={selected !== null}
        onClose={() => setSelected(null)}
        width={LAYOUT.logDrawerWidth}
        extra={
          <Button
            loading={detail.isFetching}
            onClick={() => void detail.refetch()}
          >
            刷新
          </Button>
        }
      >
        <Space direction="vertical" size={SPACING.lg} style={{ width: '100%' }}>
          {detailError && (
            <Alert
              type="error"
              message={
                data ? '连接信息更新失败，显示上次数据' : '连接信息加载失败'
              }
              description={detailError}
              action={
                <Button onClick={() => void detail.refetch()}>重试</Button>
              }
            />
          )}
          {detail.isPending && !detailError ? (
            <Skeleton active />
          ) : data ? (
            <>
              <Descriptions
                column={1}
                size="small"
                styles={{ content: { overflowWrap: 'anywhere' } }}
                items={[
                  {
                    key: 'name',
                    label: '名称',
                    children: data.process.name || '--（进程名称不可读取）',
                  },
                  { key: 'pid', label: 'PID', children: data.process.pid },
                  {
                    key: 'ips',
                    label: '监听 IP',
                    children:
                      data.process.listenIPs.join(', ') ||
                      '--（未发现监听地址）',
                  },
                  {
                    key: 'ports',
                    label: '端口',
                    children:
                      data.process.ports.join(', ') || '--（未发现可见端口）',
                  },
                  {
                    key: 'rate',
                    label: '上传 / 下载',
                    children: `${rate(data.process.upload)} / ${rate(data.process.download)}`,
                  },
                  {
                    key: 'scope',
                    label: '采样范围',
                    children: data.process.rateNote,
                  },
                  {
                    key: 'sampledAt',
                    label: '采样时间',
                    children: new Date(data.sampledAt).toLocaleString(),
                  },
                ]}
              />
              <Typography.Text type="secondary">
                速率仅来自可连续采样的 TCP 连接，单位 B/s（按 KiB、MiB
                换算）；UDP、首次采样及无法读取计数的连接显示 --。
              </Typography.Text>
              {data.notice && <Alert type="info" message={data.notice} />}
              <ContentSizedTable<NetworkConnection>
                fillColumn="remoteIP"
                size="small"
                rowKey={(_, index) => String(index)}
                loading={detail.isFetching}
                dataSource={data.connections}
                pagination={{
                  current: detailPagination.page,
                  pageSize: detailPagination.pageSize,
                  total: data.total,
                  showSizeChanger: true,
                  pageSizeOptions: [20, 50, 100],
                  showTotal: (total) => `共 ${total} 条连接及监听记录`,
                  onChange: (page, pageSize) =>
                    setDetailPagination({ page, pageSize }),
                }}
                columns={[
                  { title: '协议', dataIndex: 'protocol' },
                  {
                    title: '本地 IP',
                    dataIndex: 'localIP',
                    render: (value: string) => (
                      <Tooltip title={value ? undefined : '本地地址不可读取'}>
                        {value || '--'}
                      </Tooltip>
                    ),
                  },
                  { title: '本地端口', dataIndex: 'localPort', align: 'right' },
                  {
                    title: '连接 IP',
                    dataIndex: 'remoteIP',
                    render: (value: string, connection) => (
                      <Tooltip
                        title={
                          connection.remotePort > 0
                            ? undefined
                            : '监听或未连接远端'
                        }
                      >
                        {connection.remotePort > 0 ? value || '--' : '--'}
                      </Tooltip>
                    ),
                  },
                  {
                    title: '远端端口',
                    dataIndex: 'remotePort',
                    align: 'right',
                    render: (value: number) => (
                      <Tooltip
                        title={value > 0 ? undefined : '监听或未连接远端'}
                      >
                        {value > 0 ? value : '--'}
                      </Tooltip>
                    ),
                  },
                  {
                    title: '状态',
                    dataIndex: 'state',
                    render: (value: string) => (
                      <Tooltip
                        title={value ? undefined : '无连接状态或状态不可读取'}
                      >
                        {value || '--'}
                      </Tooltip>
                    ),
                  },
                  {
                    title: '上传速率',
                    dataIndex: 'upload',
                    align: 'right',
                    render: rate,
                  },
                  {
                    title: '下载速率',
                    dataIndex: 'download',
                    align: 'right',
                    render: rate,
                  },
                ]}
              />
            </>
          ) : null}
        </Space>
      </Drawer>
    </Space>
  );
}
