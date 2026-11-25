import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Card, Col, Row, Space, theme, Typography } from 'antd';
import ReactECharts from 'echarts-for-react/esm/core';
import * as echarts from 'echarts/core';
import { LineChart } from 'echarts/charts';
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import { request } from '../../api/client';
import type { DockerStats } from '../../api/docker';
import { formatSize, formatTime } from '../../utils/format';

echarts.use([
  LineChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  CanvasRenderer,
]);

const mib = 1024 ** 2;
type Sample = {
  time: number;
  cpu: number | null;
  memory: number;
  cache: number;
  read: number;
  write: number;
  sent: number;
  received: number;
};
export default function ContainerMonitor({
  id,
  active,
}: {
  id: string;
  active: boolean;
}) {
  const { token } = theme.useToken();
  const [history, setHistory] = useState<{
    id: string;
    updatedAt: number;
    samples: Sample[];
  }>({ id, updatedAt: 0, samples: [] });
  const stats = useQuery<DockerStats>({
    queryKey: ['docker-stats', id],
    enabled: active,
    refetchInterval: (query) => (query.state.error ? false : 3000),
    queryFn: ({ signal }) =>
      request<DockerStats>(`/api/docker/containers/${id}/stats`, {
        signal,
        silentError: true,
      }),
  });
  let samples = history.id === id ? history.samples : [];
  if (history.id !== id) {
    setHistory({ id, updatedAt: 0, samples: [] });
  } else if (stats.data && stats.dataUpdatedAt > history.updatedAt) {
    const data = stats.data;
    const cpuDelta =
      data.cpu_stats.cpu_usage.total_usage -
      data.precpu_stats.cpu_usage.total_usage;
    const systemDelta =
      (data.cpu_stats.system_cpu_usage ?? 0) -
      (data.precpu_stats.system_cpu_usage ?? 0);
    const mem = data.memory_stats;
    const cache =
      mem.stats?.cache ??
      mem.stats?.file ??
      mem.stats?.total_inactive_file ??
      mem.stats?.inactive_file ??
      0;
    const io = data.blkio_stats?.io_service_bytes_recursive ?? [];
    const networks = Object.values(data.networks ?? {});
    const sample: Sample = {
      time: stats.dataUpdatedAt,
      cpu:
        systemDelta > 0 && cpuDelta >= 0
          ? ((100 * cpuDelta) / systemDelta) *
            (data.cpu_stats.online_cpus ??
              data.cpu_stats.cpu_usage.percpu_usage?.length ??
              1)
          : null,
      memory: Math.max(0, (mem.usage ?? 0) - cache) / mib,
      cache: cache / mib,
      read: io
        .filter((value) => value.op.toLowerCase() === 'read')
        .reduce((total, value) => total + value.value, 0),
      write: io
        .filter((value) => value.op.toLowerCase() === 'write')
        .reduce((total, value) => total + value.value, 0),
      sent: networks.reduce((total, value) => total + value.tx_bytes, 0),
      received: networks.reduce((total, value) => total + value.rx_bytes, 0),
    };
    samples = [...samples.slice(-59), sample];
    setHistory({ id, updatedAt: stats.dataUpdatedAt, samples });
  }
  if (!active) return <Alert type="info" message="启动容器后可查看实时监控" />;
  const latest = samples.at(-1);
  const rate = (key: 'read' | 'write' | 'sent' | 'received') =>
    samples.map((sample, index) => {
      const previous = samples[index - 1];
      return previous && sample.time > previous.time
        ? Math.max(
            0,
            ((sample[key] - previous[key]) * 1000) /
              (sample.time - previous.time) /
              mib,
          )
        : null;
    });
  const charts = [
    {
      title: 'CPU',
      unit: '%',
      lines: [{ name: '使用率', data: samples.map((s) => s.cpu) }],
    },
    {
      title: '内存',
      unit: 'MiB',
      lines: [
        { name: '内存', data: samples.map((s) => s.memory) },
        { name: '缓存', data: samples.map((s) => s.cache) },
      ],
    },
    {
      title: '磁盘 I/O',
      unit: 'MiB/s',
      lines: [
        { name: '读取', data: rate('read') },
        { name: '写入', data: rate('write') },
      ],
    },
    {
      title: '网络 I/O',
      unit: 'MiB/s',
      lines: [
        { name: '上行', data: rate('sent') },
        { name: '下行', data: rate('received') },
      ],
    },
  ];
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {stats.error && (
        <Alert
          type="error"
          message={stats.error.message}
          action={<a onClick={() => void stats.refetch()}>重试</a>}
        />
      )}
      <Space wrap size="large">
        <Typography.Text>
          内存限额{' '}
          {stats.data?.memory_stats.limit
            ? formatSize(stats.data.memory_stats.limit)
            : '—'}
        </Typography.Text>
        <Typography.Text>
          累计上行 {latest ? formatSize(latest.sent) : '—'}
        </Typography.Text>
        <Typography.Text>
          累计下行 {latest ? formatSize(latest.received) : '—'}
        </Typography.Text>
      </Space>
      <Row gutter={[12, 12]}>
        {charts.map(({ title, unit, lines }) => (
          <Col xs={24} xl={12} key={title}>
            <Card size="small" title={title}>
              <ReactECharts
                echarts={echarts}
                style={{ height: 200 }}
                option={{
                  animation: false,
                  color: [token.colorPrimary, token.colorSuccess],
                  grid: { top: 32, bottom: 28, left: 54, right: 16 },
                  legend: {
                    top: 0,
                    textStyle: { color: token.colorTextSecondary },
                  },
                  tooltip: {
                    trigger: 'axis',
                    valueFormatter: (v: unknown) =>
                      typeof v === 'number' ? `${v.toFixed(2)} ${unit}` : '—',
                  },
                  xAxis: {
                    type: 'category',
                    data: samples.map((s) =>
                      formatTime(new Date(s.time).toISOString()).slice(-8),
                    ),
                    axisLabel: { color: token.colorTextSecondary },
                  },
                  yAxis: {
                    type: 'value',
                    min: 0,
                    axisLabel: {
                      color: token.colorTextSecondary,
                      formatter: (v: number) =>
                        `${v.toFixed(v < 1 && v > 0 ? 2 : 0)}`,
                    },
                    splitLine: {
                      lineStyle: { color: token.colorBorderSecondary },
                    },
                  },
                  series: lines.map((line) => ({
                    ...line,
                    type: 'line',
                    showSymbol: lines[0].data.length < 3,
                  })),
                }}
              />
            </Card>
          </Col>
        ))}
      </Row>
    </Space>
  );
}
