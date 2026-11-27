import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import {
  Alert,
  Badge,
  Button,
  Card,
  Col,
  Descriptions,
  Empty,
  Flex,
  Progress,
  Row,
  Segmented,
  Select,
  Skeleton,
  Space,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import {
  Activity,
  Boxes,
  ChevronRight,
  Code,
  Cpu,
  Database,
  Folder,
  Globe,
  HardDrive,
  MemoryStick,
} from 'lucide-react';
import ReactECharts from 'echarts-for-react/esm/core';
import * as echarts from 'echarts/core';
import { LineChart } from 'echarts/charts';
import {
  DataZoomSliderComponent,
  GridComponent,
  TooltipComponent,
} from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  historyPoints,
  overview,
  summary,
  type Point,
  type MonitorFrame,
  type MonitorNetworkFeed,
  type MonitorNetworkFilter,
  type MonitorProcessFilter,
  type Summary,
} from '../../api/sysinfo';
import { useWebSocket } from '../../hooks/useWebSocket';
import {
  CHART_COLORS,
  FONT_SIZE,
  LAYOUT,
  SPACING,
  getUsageLevel,
} from '../../styles/tokens';
import Processes from './Processes';
import NetworkDetails from './NetworkDetails';
import ResourcePopover, { type ResourceKind } from './ResourceDetails';
import SystemBar from './SystemBar';
import { bytes, number } from './format';
import './dashboard.css';
import PluginWidgets from './PluginWidgets';

echarts.use([
  LineChart,
  GridComponent,
  TooltipComponent,
  DataZoomSliderComponent,
  CanvasRenderer,
]);

const historySpan = 60 * 60 * 1000;
const initialSpan = 5 * 60 * 1000;
type TimeRange = { start: number; end: number };
type TimeSelection = TimeRange & { followLatest: boolean; history?: TimeRange };

export default function Dashboard() {
  const { token } = theme.useToken();
  const client = useQueryClient();
  const [processFilter, setProcessFilter] = useState<MonitorProcessFilter>();
  const [networkFilter, setNetworkFilter] = useState<MonitorNetworkFilter>();
  const [networkFeed, setNetworkFeed] = useState<MonitorNetworkFeed>();
  const [livePoints, setLivePoints] = useState<Point[]>([]);
  const [selection, setSelection] = useState<TimeSelection>();
  const [monitor, setMonitor] = useState('traffic');
  const [trafficView, setTrafficView] = useState('trend');
  const [diskView, setDiskView] = useState('io');
  const [network, setNetwork] = useState('');
  const [disk, setDisk] = useState('');
  const dragging = useRef(false);
  useEffect(() => {
    const stopDragging = () => {
      dragging.current = false;
    };
    window.addEventListener('pointerup', stopDragging, true);
    window.addEventListener('pointercancel', stopDragging, true);
    return () => {
      window.removeEventListener('pointerup', stopDragging, true);
      window.removeEventListener('pointercancel', stopDragging, true);
    };
  }, []);
  const receive = useCallback(
    (frame: MonitorFrame) => {
      const point = frame.point;
      if (point) {
        const ts = Date.parse(point.ts);
        const cutoff = ts - historySpan;
        setLivePoints((previous) => {
          const last = previous.at(-1);
          if (last && Date.parse(last.ts) >= ts) return previous;
          const first = previous.findIndex(
            (item) => Date.parse(item.ts) >= cutoff,
          );
          return first < 0 ? [point] : [...previous.slice(first), point];
        });
      }
      if (frame.overview) client.setQueryData(['sysinfo'], frame.overview);
      if (frame.top) {
        client.setQueryData(['processes', 'top', 'cpu'], frame.top.cpu);
        client.setQueryData(['processes', 'top', 'mem'], frame.top.mem);
      }
      if (frame.processes) {
        const { filter, list } = frame.processes;
        client.setQueryData(['processes', filter.sort, filter.keyword], list);
      }
      if (frame.network) {
        const { filter, list, detail } = frame.network;
        if (list)
          client.setQueryData(
            [
              'network-processes',
              filter.keyword,
              { page: filter.page, pageSize: filter.pageSize },
            ],
            list,
          );
        if (detail)
          client.setQueryData(
            [
              'network-process-detail',
              filter.pid,
              filter.started,
              { page: filter.detailPage, pageSize: filter.detailPageSize },
            ],
            detail,
          );
        setNetworkFeed(frame.network);
      }
    },
    [client],
  );
  const subscription = JSON.stringify({
    processes: monitor === 'process' ? processFilter : undefined,
    network:
      monitor === 'traffic' && trafficView === 'details'
        ? networkFilter
        : undefined,
  });
  const connection = useWebSocket<MonitorFrame>(
    '/ws/sysinfo?view=dashboard',
    receive,
    subscription,
  );
  const connected = connection === '实时连接';
  const info = useQuery({
    queryKey: ['sysinfo'],
    queryFn: ({ signal }) => overview(signal),
    enabled: !connected,
  });
  const counts = useQuery({
    queryKey: ['sysinfo-summary'],
    queryFn: ({ signal }) => summary(signal),
    refetchInterval: (query) => (query.state.error ? false : 60000),
  });
  const latest = livePoints.at(-1);
  const domainEnd = latest ? Date.parse(latest.ts) : 0;
  const domainStart = domainEnd - historySpan;
  const activeSelection =
    selection && (selection.followLatest || selection.end > domainStart)
      ? selection
      : undefined;
  const end =
    activeSelection && !activeSelection.followLatest
      ? activeSelection.end
      : domainEnd;
  const start = Math.max(
    domainStart,
    activeSelection && !activeSelection.followLatest
      ? activeSelection.start
      : end -
          (activeSelection
            ? activeSelection.end - activeSelection.start
            : initialSpan),
  );
  const historyRange = activeSelection?.history;
  const history = useQuery({
    queryKey: ['sysinfo-history', historyRange],
    queryFn: ({ signal }) => historyPoints(historyRange!, signal),
    enabled: !!historyRange,
  });
  const points = history.data?.length
    ? [...history.data, ...livePoints]
    : livePoints;
  const data = info.data;
  if (!data)
    return info.isError ? (
      <Alert
        type="error"
        message="服务器信息加载失败"
        action={<Button onClick={() => void info.refetch()}>重试</Button>}
      />
    ) : (
      <Skeleton active />
    );

  const live = connection === '实时连接' ? latest : undefined;
  const currentLoad = live?.load1 ?? data.load?.l1;
  const memoryPercent = live?.memPercent ?? data.mem?.usedPercent;
  const root = data.disks.find((disk) => disk.mount === '/');
  // 负载是排队任务数；圆环按核心数折算，原始负载仍单独展示。
  const loadPercent =
    currentLoad !== undefined && currentLoad !== null && data.cpu.cores
      ? (currentLoad / data.cpu.cores) * 100
      : null;
  const resources: {
    kind: ResourceKind;
    title: string;
    value: string;
    detail: string;
    progress?: number | null;
  }[] = [
    {
      kind: 'load',
      title: '系统负载',
      value: number(currentLoad),
      detail: `1 / 5 / 15 分钟：${number(currentLoad)} / ${number(data.load?.l5)} / ${number(data.load?.l15)}`,
      progress: loadPercent,
    },
    {
      kind: 'cpu',
      title: 'CPU',
      value: `${data.cpu.cores ?? '--'} 核心`,
      detail: data.cpu.model || 'CPU 型号不可用',
      progress: live?.cpu ?? data.cpu.usage,
    },
    {
      kind: 'memory',
      title: '内存',
      value: bytes(
        data.mem && memoryPercent != null
          ? Math.round((data.mem.total * memoryPercent) / 100)
          : data.mem?.used,
      ),
      detail: `总计 ${bytes(data.mem?.total)}`,
      progress: memoryPercent,
    },
    {
      kind: 'disk',
      title: '根磁盘 /',
      value: bytes(root?.used),
      detail: `总计 ${bytes(root?.total)}`,
      progress: root?.usedPercent,
    },
  ];
  const summaryItems: {
    key: keyof Summary;
    title: string;
    route?: string;
    note?: string;
  }[] = [
    { key: 'websites', title: '网站', route: '/websites' },
    {
      key: 'databases',
      title: '数据库',
      route: '/databases',
      note: '已登记数据库服务器的业务库总数，不含系统库',
    },
    { key: 'docker', title: 'Docker 容器', note: '包含运行中与已停止的容器' },
    { key: 'crons', title: '计划任务', route: '/crons' },
  ];
  const selectedNetwork = latest?.networks.find(
    (item) => item.name === network,
  );
  const traffic = network
    ? {
        up: selectedNetwork?.up,
        down: selectedNetwork?.down,
        sent: selectedNetwork?.sent,
        recv: selectedNetwork?.recv,
      }
    : {
        up: latest?.netUp,
        down: latest?.netDown,
        sent: latest?.netSent,
        recv: latest?.netRecv,
      };
  const selectedDisk = latest?.diskIO?.find((item) => item.name === disk);
  const diskMetrics = disk
    ? selectedDisk
    : {
        read: latest?.diskRead,
        write: latest?.diskWrite,
        readOps: latest?.diskReadOps,
        writeOps: latest?.diskWriteOps,
        latency: latest?.diskLatency,
        readLatency: latest?.diskReadLatency,
        writeLatency: latest?.diskWriteLatency,
      };
  const chartMetric =
    monitor === 'cpu'
      ? 'CPU'
      : monitor === 'memory'
        ? '内存'
        : monitor === 'disk'
          ? '磁盘 IO'
          : '网络';
  const series =
    chartMetric === 'CPU'
      ? [{ name: 'CPU', values: points.map((point) => point.cpu) }]
      : chartMetric === '内存'
        ? [{ name: '内存', values: points.map((point) => point.memPercent) }]
        : chartMetric === '磁盘 IO'
          ? [
              {
                name: '读取',
                values: points.map((point) =>
                  disk
                    ? (point.diskIO?.find((item) => item.name === disk)?.read ??
                      null)
                    : point.diskRead,
                ),
              },
              {
                name: '写入',
                values: points.map((point) =>
                  disk
                    ? (point.diskIO?.find((item) => item.name === disk)
                        ?.write ?? null)
                    : point.diskWrite,
                ),
              },
            ]
          : [
              {
                name: '上行',
                values: points.map((point) =>
                  network
                    ? (point.networks.find((item) => item.name === network)
                        ?.up ?? null)
                    : point.netUp,
                ),
              },
              {
                name: '下行',
                values: points.map((point) =>
                  network
                    ? (point.networks.find((item) => item.name === network)
                        ?.down ?? null)
                    : point.netDown,
                ),
              },
            ];
  const percentMetric = chartMetric === 'CPU' || chartMetric === '内存';
  const release =
    data.release ||
    [data.platformName || data.platform || data.os, data.platformVersion]
      .filter(Boolean)
      .join(' ') ||
    '--';

  return (
    <Space
      className="overview-content"
      direction="vertical"
      size={SPACING.lg}
      style={{ width: '100%' }}
    >
      <SystemBar data={data} release={release} />
      {info.isError && (
        <Alert type="warning" message="服务器信息更新失败，显示上次数据" />
      )}
      <Row gutter={[SPACING.lg, SPACING.lg]}>
        {resources.map((item) => {
          const iconMap = {
            load: {
              icon: <Activity size={18} strokeWidth={2} />,
              cls: 'apple-res-icon--blue',
            },
            cpu: {
              icon: <Cpu size={18} strokeWidth={2} />,
              cls: 'apple-res-icon--purple',
            },
            memory: {
              icon: <MemoryStick size={18} strokeWidth={2} />,
              cls: 'apple-res-icon--green',
            },
            disk: {
              icon: <HardDrive size={18} strokeWidth={2} />,
              cls: 'apple-res-icon--orange',
            },
          }[item.kind];
          return (
            <Col xs={24} sm={12} xl={6} key={item.kind}>
              <ResourcePopover
                kind={item.kind}
                title={item.title}
                data={data}
                root={root}
                live={connected}
              >
                <Card size="small" className="apple-resource-card">
                  <Flex
                    justify="space-between"
                    align="flex-end"
                    gap={SPACING.md}
                    style={{
                      minHeight: `var(--overview-resource-height, ${LAYOUT.dashboardResourceHeight}px)`,
                    }}
                  >
                    <Space
                      direction="vertical"
                      size={4}
                      style={{ minWidth: 0 }}
                    >
                      <Flex align="center" gap={8} style={{ marginBottom: 4 }}>
                        <span className={`apple-res-icon ${iconMap.cls}`}>
                          {iconMap.icon}
                        </span>
                        <Typography.Text
                          type="secondary"
                          style={{ fontSize: 13, fontWeight: 500 }}
                        >
                          {item.title}
                        </Typography.Text>
                      </Flex>
                      <Typography.Text
                        strong
                        style={{
                          fontSize: FONT_SIZE.statistic,
                          fontWeight: 700,
                          letterSpacing: '-0.03em',
                          lineHeight: 1.2,
                          fontVariantNumeric: 'tabular-nums',
                        }}
                      >
                        {item.value}
                      </Typography.Text>
                      <Typography.Text
                        type="secondary"
                        style={{
                          fontSize: FONT_SIZE.auxiliary,
                          color: token.colorTextTertiary,
                          overflowWrap: 'anywhere',
                        }}
                      >
                        {item.detail}
                      </Typography.Text>
                    </Space>
                    <Progress
                      type="circle"
                      size={LAYOUT.dashboardRingSize}
                      strokeWidth={9}
                      strokeLinecap="round"
                      percent={
                        item.progress == null ? 0 : Math.min(item.progress, 100)
                      }
                      strokeColor={
                        item.progress == null
                          ? token.colorTextQuaternary
                          : token[getUsageLevel(item.progress)]
                      }
                      trailColor={token.colorFillSecondary}
                      format={() => number(item.progress, '%')}
                      style={{ flexShrink: 0, alignSelf: 'center' }}
                    />
                  </Flex>
                </Card>
              </ResourcePopover>
            </Col>
          );
        })}
      </Row>
      <PluginWidgets />
      <Card title="概览" size="small">
        {counts.isError && (
          <Alert
            type="warning"
            message="概览数量更新失败"
            action={
              <Button size="small" onClick={() => void counts.refetch()}>
                重试
              </Button>
            }
            style={{ marginBottom: SPACING.lg }}
          />
        )}
        <Row gutter={[SPACING.xl, SPACING.lg]}>
          {summaryItems.map((item) => {
            const count = counts.data?.[item.key];
            const inner = (
              <Flex gap={10} align="baseline" className="apple-count-inner">
                <Typography.Text
                  strong
                  style={{
                    fontSize: 26,
                    fontWeight: 700,
                    letterSpacing: '-0.03em',
                    fontVariantNumeric: 'tabular-nums',
                    lineHeight: 1.1,
                  }}
                >
                  {counts.isPending ? '…' : (count?.value ?? '--')}
                </Typography.Text>
                <Typography.Text
                  type="secondary"
                  className="apple-count-title"
                  style={{ fontSize: 13.5, fontWeight: 500 }}
                >
                  {item.title}
                </Typography.Text>
                {item.route && (
                  <ChevronRight
                    size={14}
                    strokeWidth={2}
                    className="apple-count-arrow"
                  />
                )}
              </Flex>
            );
            return (
              <Col xs={12} md={6} key={item.key}>
                <Tooltip title={item.note}>
                  {item.route ? (
                    <Link
                      to={item.route}
                      className="apple-count-link"
                      aria-label={`打开${item.title}`}
                    >
                      {inner}
                    </Link>
                  ) : (
                    <div>{inner}</div>
                  )}
                </Tooltip>
                {count?.error && (
                  <Typography.Text
                    type="secondary"
                    style={{ fontSize: FONT_SIZE.auxiliary, display: 'block' }}
                  >
                    {count.error}
                  </Typography.Text>
                )}
              </Col>
            );
          })}
        </Row>
      </Card>
      <Row gutter={[SPACING.lg, SPACING.lg]}>
        <Col xs={24} xl={16}>
          <Card className="overview-monitor">
            <Tabs
              activeKey={monitor}
              onChange={setMonitor}
              items={[
                { key: 'traffic', label: '流量' },
                { key: 'cpu', label: 'CPU' },
                { key: 'memory', label: '内存' },
                { key: 'disk', label: '磁盘' },
                { key: 'process', label: '进程' },
              ]}
            />
            {monitor === 'traffic' && (
              <Flex
                justify="space-between"
                align="center"
                gap={SPACING.md}
                wrap
                style={{ marginBottom: SPACING.lg }}
              >
                <Segmented
                  options={[
                    { label: '流量趋势', value: 'trend' },
                    { label: '网络详情', value: 'details' },
                  ]}
                  value={trafficView}
                  onChange={setTrafficView}
                  aria-label="流量视图"
                />
                <Space size={SPACING.sm}>
                  <Typography.Text type="secondary">网卡</Typography.Text>
                  <Select
                    aria-label="选择监控网卡"
                    disabled={trafficView !== 'trend'}
                    value={network}
                    onChange={setNetwork}
                    style={{ width: LAYOUT.dashboardNetworkSelectWidth }}
                    options={[
                      { label: '所有网卡', value: '' },
                      ...(latest?.networks.map((item) => ({
                        label: item.name,
                        value: item.name,
                      })) ?? []),
                    ]}
                  />
                </Space>
              </Flex>
            )}
            {monitor === 'disk' && (
              <Flex
                justify="space-between"
                align="center"
                gap={SPACING.md}
                wrap
                style={{ marginBottom: SPACING.lg }}
              >
                <Segmented
                  options={[
                    { label: '磁盘IO', value: 'io' },
                    { label: '磁盘分区', value: 'partitions' },
                  ]}
                  value={diskView}
                  onChange={setDiskView}
                  aria-label="磁盘视图"
                />
                <Space size={SPACING.sm}>
                  <Typography.Text type="secondary">磁盘</Typography.Text>
                  <Select
                    aria-label="选择监控磁盘"
                    value={disk}
                    onChange={setDisk}
                    disabled={diskView !== 'io'}
                    style={{ width: LAYOUT.dashboardNetworkSelectWidth }}
                    options={[
                      { label: '所有', value: '' },
                      ...(latest?.diskIO?.map((item) => ({
                        label: item.name,
                        value: item.name,
                      })) || []),
                    ]}
                  />
                </Space>
              </Flex>
            )}
            {monitor === 'process' ? (
              <Processes live={connected} onFilter={setProcessFilter} />
            ) : monitor === 'traffic' && trafficView === 'details' ? (
              <NetworkDetails
                live={connected}
                onFilter={setNetworkFilter}
                feed={networkFeed}
              />
            ) : monitor === 'disk' && diskView === 'partitions' ? (
              <ContentSizedTable
                fillColumn="mount"
                size="small"
                rowKey="mount"
                pagination={false}
                dataSource={data.disks}
                columns={[
                  { title: '挂载点', dataIndex: 'mount' },
                  { title: '类型', dataIndex: 'fstype' },
                  {
                    title: '已用 / 总计',
                    align: 'right',
                    render: (_, disk) =>
                      `${bytes(disk.used)} / ${bytes(disk.total)}`,
                  },
                  {
                    title: '使用率',
                    dataIndex: 'usedPercent',
                    align: 'right',
                    render: (value: number) => number(value, '%'),
                  },
                ]}
              />
            ) : (
              <Space
                direction="vertical"
                size={SPACING.lg}
                style={{ width: '100%' }}
              >
                {(chartMetric === '网络' || chartMetric === '磁盘 IO') && (
                  <Row gutter={[SPACING.md, SPACING.sm]}>
                    {(chartMetric === '网络'
                      ? [
                          [
                            '上行',
                            `${bytes(traffic.up)}${traffic.up == null ? '' : '/s'}`,
                          ],
                          [
                            '下行',
                            `${bytes(traffic.down)}${traffic.down == null ? '' : '/s'}`,
                          ],
                          ['累计发送', bytes(traffic.sent)],
                          ['累计接收', bytes(traffic.recv)],
                        ]
                      : [
                          [
                            '读取',
                            `${bytes(diskMetrics?.read)}${diskMetrics?.read == null ? '' : '/s'}`,
                          ],
                          [
                            '写入',
                            `${bytes(diskMetrics?.write)}${diskMetrics?.write == null ? '' : '/s'}`,
                          ],
                          [
                            '每秒读/写次数',
                            `${diskMetrics?.readOps == null ? '--' : Math.round(diskMetrics.readOps)} / ${diskMetrics?.writeOps == null ? '--' : Math.round(diskMetrics.writeOps)} 次/s`,
                          ],
                          ['IO延迟', number(diskMetrics?.latency, ' ms')],
                        ]
                    ).map(([label, value]) => (
                      <Col xs={12} md={6} key={label}>
                        <Space direction="vertical" size={SPACING.xs}>
                          <Typography.Text type="secondary">
                            {label}
                          </Typography.Text>
                          <Typography.Text
                            strong
                            style={{ fontVariantNumeric: 'tabular-nums' }}
                          >
                            {value}
                          </Typography.Text>
                        </Space>
                      </Col>
                    ))}
                  </Row>
                )}
                {series.length > 1 && (
                  <Space size={SPACING.lg} wrap aria-label="图例">
                    {series.map((item, index) => (
                      <Badge
                        key={item.name}
                        color={CHART_COLORS[index]}
                        text={item.name}
                      />
                    ))}
                  </Space>
                )}
                {points.length === 0 ? (
                  <Flex
                    justify="center"
                    align="center"
                    style={{ height: LAYOUT.chartHeight }}
                  >
                    <Empty description="等待实时采样" />
                  </Flex>
                ) : (
                  <div
                    className="overview-chart"
                    onPointerDownCapture={() => {
                      dragging.current = true;
                    }}
                  >
                    <ReactECharts
                      notMerge
                      echarts={echarts}
                      shouldSetOption={() => !dragging.current}
                      style={{ height: LAYOUT.chartHeight }}
                      onEvents={{
                        datazoom: ({
                          start,
                          end,
                        }: {
                          start: number;
                          end: number;
                        }) => {
                          const from = Math.round(
                            domainStart + (historySpan * start) / 100,
                          );
                          const to = Math.round(
                            domainStart + (historySpan * end) / 100,
                          );
                          const historyEnd = Math.min(
                            to,
                            Date.parse(livePoints[0].ts) - 1,
                          );
                          setSelection({
                            start: from,
                            end: to,
                            followLatest: end >= 99.99,
                            history:
                              from <= historyEnd
                                ? { start: from, end: historyEnd }
                                : undefined,
                          });
                        },
                      }}
                      option={{
                        color: CHART_COLORS.slice(0, 2),
                        textStyle: {
                          color: token.colorText,
                          fontFamily: token.fontFamily,
                        },
                        tooltip: {
                          trigger: 'axis',
                          renderMode: 'richText',
                          ...(chartMetric === '磁盘 IO'
                            ? {
                                formatter: (
                                  items: {
                                    dataIndex: number;
                                  }[],
                                ) => {
                                  const point = points[items[0]?.dataIndex];
                                  if (!point) return '';
                                  const metrics = disk
                                    ? point.diskIO?.find(
                                        (item) => item.name === disk,
                                      )
                                    : {
                                        read: point.diskRead,
                                        write: point.diskWrite,
                                        readOps: point.diskReadOps,
                                        writeOps: point.diskWriteOps,
                                        readLatency: point.diskReadLatency,
                                        writeLatency: point.diskWriteLatency,
                                      };
                                  return [
                                    new Date(point.ts).toLocaleTimeString(),
                                    `读取：${bytes(metrics?.read)}/s`,
                                    `写入：${bytes(metrics?.write)}/s`,
                                    `读取次数：${metrics?.readOps == null ? '--' : Math.round(metrics.readOps)} 次/s`,
                                    `写入次数：${metrics?.writeOps == null ? '--' : Math.round(metrics.writeOps)} 次/s`,
                                    `读取延迟：${number(metrics?.readLatency, ' ms')}`,
                                    `写入延迟：${number(metrics?.writeLatency, ' ms')}`,
                                  ].join('\n');
                                },
                              }
                            : {}),
                          backgroundColor: token.colorBgContainer,
                          borderColor: token.colorBorder,
                          textStyle: { color: token.colorText },
                          valueFormatter: (
                            value: number | null | [number, number | null],
                          ) => {
                            const metric = Array.isArray(value)
                              ? value[1]
                              : value;
                            return percentMetric
                              ? number(metric, '%')
                              : metric == null
                                ? '--'
                                : `${bytes(metric)}/s`;
                          },
                        },
                        grid: {
                          left: SPACING.xs,
                          right: SPACING.lg,
                          top: SPACING.xxl,
                          bottom: 36,
                          containLabel: true,
                        },
                        xAxis: {
                          type: 'time',
                          min: domainStart,
                          max: domainEnd,
                          axisLabel: { color: token.colorTextSecondary },
                        },
                        dataZoom: {
                          type: 'slider',
                          xAxisIndex: 0,
                          startValue: start,
                          endValue: end,
                          minValueSpan: 10_000,
                          realtime: false,
                          height: 22,
                          bottom: 4,
                          brushSelect: false,
                          borderColor: token.colorBorder,
                          backgroundColor: token.colorFillQuaternary,
                          fillerColor: token.colorPrimaryBgHover,
                          textStyle: { color: token.colorTextSecondary },
                          labelFormatter: (value: number) =>
                            new Date(value).toLocaleTimeString(),
                        },
                        yAxis: {
                          type: 'value',
                          name: percentMetric ? '%' : 'B/s',
                          axisLabel: {
                            color: token.colorTextSecondary,
                            formatter: percentMetric
                              ? '{value}%'
                              : (value: number) => bytes(value),
                          },
                          splitLine: {
                            lineStyle: { color: token.colorBorderSecondary },
                          },
                        },
                        series: series.map((item, idx) => {
                          const color = CHART_COLORS[idx] || CHART_COLORS[0];
                          return {
                            name: item.name,
                            type: 'line',
                            smooth: 0.35,
                            lineStyle: { width: 2.2, color },
                            areaStyle: {
                              color: new echarts.graphic.LinearGradient(
                                0,
                                0,
                                0,
                                1,
                                [
                                  { offset: 0, color: `${color}30` },
                                  { offset: 1, color: `${color}02` },
                                ],
                              ),
                            },
                            showSymbol: points.length === 1,
                            connectNulls: false,
                            data: item.values.map((value, index) => [
                              Date.parse(points[index].ts),
                              value,
                            ]),
                          };
                        }),
                      }}
                    />
                  </div>
                )}
                {historyRange &&
                  (history.isError ? (
                    <Alert
                      type="warning"
                      message="历史采样加载失败"
                      action={
                        <Button
                          size="small"
                          onClick={() => void history.refetch()}
                        >
                          重试
                        </Button>
                      }
                    />
                  ) : history.isPending || history.data.length === 0 ? (
                    <Typography.Text type="secondary">
                      {history.isPending
                        ? '正在读取历史采样…'
                        : '所选历史区间暂无采样'}
                    </Typography.Text>
                  ) : null)}
                {chartMetric === '网络' && (
                  <Typography.Text
                    type="secondary"
                    style={{ fontSize: FONT_SIZE.auxiliary }}
                  >
                    累计流量来自网卡计数器，重启或网卡重建后会重置；所有网卡含虚拟网卡，可能重复统计转发流量。
                  </Typography.Text>
                )}
              </Space>
            )}
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Space
            direction="vertical"
            size={SPACING.lg}
            style={{ width: '100%' }}
          >
            <Card title="服务器信息" size="small">
              <Descriptions
                column={1}
                size="small"
                styles={{
                  label: { whiteSpace: 'nowrap' },
                  content: { overflowWrap: 'anywhere' },
                }}
                items={[
                  {
                    key: 'hostname',
                    label: '主机名称',
                    children: data.hostname || '--',
                  },
                  { key: 'release', label: '发行版本', children: release },
                  {
                    key: 'kernel',
                    label: '内核',
                    children: data.kernel || '--',
                  },
                  {
                    key: 'system-type',
                    label: '系统类型',
                    children:
                      [data.os === 'linux' ? 'Linux' : data.os, data.arch]
                        .filter(Boolean)
                        .join(' / ') || '--',
                  },
                  {
                    key: 'cpu',
                    label: 'CPU',
                    children: data.cpu.model || '--',
                  },
                  {
                    key: 'boot',
                    label: '启动时间',
                    children: data.bootTime
                      ? new Date(data.bootTime).toLocaleString()
                      : '--',
                  },
                  {
                    key: 'ips',
                    label: 'IP 地址',
                    children: data.ips.length ? (
                      <Flex wrap gap={SPACING.xs}>
                        {data.ips.map((ip) => (
                          <Tag
                            key={ip}
                            style={{
                              maxWidth: '100%',
                              whiteSpace: 'normal',
                              overflowWrap: 'anywhere',
                            }}
                          >
                            {ip}
                          </Tag>
                        ))}
                      </Flex>
                    ) : (
                      '--'
                    ),
                  },
                  {
                    key: 'swap',
                    label: 'Swap',
                    children: `${bytes(data.mem?.swapUsed)} / ${bytes(data.mem?.swapTotal)}`,
                  },
                ]}
              />
            </Card>
            <Card title="软件与工具" size="small">
              <Row gutter={[SPACING.sm, SPACING.sm]}>
                {[
                  {
                    name: '终端',
                    route: '/terminal',
                    icon: <Code size={16} strokeWidth={1.9} />,
                  },
                  {
                    name: '网站管理',
                    route: '/websites',
                    icon: <Globe size={16} strokeWidth={1.9} />,
                  },
                  {
                    name: '数据库',
                    route: '/databases',
                    icon: <Database size={16} strokeWidth={1.9} />,
                  },
                  {
                    name: '文件管理',
                    route: '/files',
                    icon: <Folder size={16} strokeWidth={1.9} />,
                  },
                  {
                    name: '应用商店',
                    route: '/apps',
                    icon: <Boxes size={16} strokeWidth={1.9} />,
                  },
                ].map((item) => (
                  <Col xs={12} key={item.route}>
                    <Link to={item.route} className="apple-tool-tile">
                      <span className="apple-tool-tile__icon">{item.icon}</span>
                      <span>{item.name}</span>
                    </Link>
                  </Col>
                ))}
              </Row>
            </Card>
          </Space>
        </Col>
      </Row>
    </Space>
  );
}
