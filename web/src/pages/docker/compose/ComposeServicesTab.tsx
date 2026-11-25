import { useState } from 'react';
import {
  Button,
  Dropdown,
  Flex,
  Popover,
  Space,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { useMutation, useQueries } from '@tanstack/react-query';
import { Ellipsis } from 'lucide-react';
import { request } from '../../../api/client';
import type { ComposeContainer } from '../../../api/composeProjects';
import type { DockerStats } from '../../../api/docker';
import ContentSizedTable from '../../../components/ContentSizedTable';
import { formatSize } from '../../../utils/format';

const states: Record<string, string> = {
  running: '运行中',
  exited: '已停止',
  paused: '已暂停',
  created: '已创建',
  restarting: '正在重启',
  dead: '异常退出',
  removing: '正在移除',
};

export function composeServiceName(row: ComposeContainer) {
  return row.Labels?.['com.docker.compose.service'] || '';
}

export function containerName(row: Pick<ComposeContainer, 'Id' | 'Names'>) {
  return row.Names?.[0]?.replace(/^\//, '') || row.Id.slice(0, 12);
}

function resourceUsage(stats?: DockerStats) {
  const cpu = stats?.cpu_stats;
  const previous = stats?.precpu_stats;
  const memory = stats?.memory_stats;
  const cores = cpu?.online_cpus || cpu?.cpu_usage?.percpu_usage?.length;
  const total = cpu?.cpu_usage?.total_usage;
  const previousTotal = previous?.cpu_usage?.total_usage;
  const cpuDelta = (total ?? 0) - (previousTotal ?? 0);
  const systemDelta =
    (cpu?.system_cpu_usage ?? 0) - (previous?.system_cpu_usage ?? 0);
  const cache =
    memory?.stats?.total_inactive_file ??
    memory?.stats?.inactive_file ??
    memory?.stats?.cache ??
    memory?.stats?.file ??
    0;
  const bytes = (size?: number) =>
    size !== undefined && size >= 0 ? formatSize(size) : '—';
  const used =
    memory?.usage === undefined ? undefined : Math.max(0, memory.usage - cache);
  return {
    cpu:
      previousTotal !== undefined && cpuDelta >= 0 && systemDelta > 0
        ? `${((cpuDelta / systemDelta) * (cores ?? 1) * 100).toFixed(1)}%`
        : '—',
    total: total === undefined ? '—' : `${(total / 1e9).toFixed(1)} 秒`,
    cpuTime:
      previousTotal === undefined || total === undefined || cpuDelta < 0
        ? '—'
        : cpuDelta < 1e6
          ? `${(cpuDelta / 1e3).toFixed(1)} µs`
          : `${(cpuDelta / 1e6).toFixed(1)} ms`,
    cores: cores ?? '—',
    memory: bytes(used),
    memoryPercent:
      used === undefined || !memory?.limit || memory.limit < 0
        ? '—'
        : `${((used / memory.limit) * 100).toFixed(2)}%`,
    cache: bytes(memory?.usage === undefined ? undefined : cache),
    limit: bytes(memory?.limit),
  };
}

function ContainerUsage({
  id,
  name,
  stats,
}: {
  id: string;
  name: string;
  stats?: DockerStats;
}) {
  const usage = resourceUsage(stats);
  const size = useMutation({
    mutationFn: () =>
      request<{ SizeRw?: number; SizeRootFs?: number }>(
        `/api/docker/containers/${encodeURIComponent(id)}`,
        { params: { size: true } },
      ),
  });
  const info = [
    ['CPU 使用', usage.cpuTime],
    ['CPU 总计', usage.total],
    ['核心数', usage.cores],
    ['内存使用', usage.memory],
    ['缓存使用', usage.cache],
    ['内存限额', usage.limit],
  ];
  const sizeInfo = size.data
    ? [
        ['可写层', size.data.SizeRw],
        ['根文件系统', size.data.SizeRootFs],
      ].map(([label, bytes]) => [
        label,
        typeof bytes === 'number' && bytes >= 0 ? formatSize(bytes) : '—',
      ])
    : [];
  return (
    <Flex gap={6} align="center">
      <div className="compose-container-usage">
        <span>
          CPU <strong>{usage.cpu}</strong>
        </span>
        <span>
          内存 <strong>{usage.memoryPercent}</strong>
        </span>
      </div>
      <Popover
        title={`资源用量 · ${name}`}
        trigger="click"
        placement="bottomRight"
        content={
          <Flex vertical gap={12} className="compose-resource-popover">
            <dl className="compose-resource-metrics">
              {[...info, ...sizeInfo].map(([label, value]) => (
                <div key={label}>
                  <dt>{label}</dt>
                  <dd>{value}</dd>
                </div>
              ))}
            </dl>
            <Button
              size="small"
              loading={size.isPending}
              onClick={() => size.mutate()}
            >
              获取容器大小
            </Button>
          </Flex>
        }
      >
        <Button
          type="text"
          size="small"
          icon={<Ellipsis size={16} />}
          aria-label={`查看 ${name} 的 CPU、内存和容器大小`}
        />
      </Popover>
    </Flex>
  );
}

export default function ComposeServicesTab({
  containers,
  active,
  onChanged,
  onContainer,
  onTerminal,
}: {
  containers: ComposeContainer[];
  active: boolean;
  onChanged: () => void;
  onContainer: (id: string, name: string, tab: string) => void;
  onTerminal: (id: string) => void;
}) {
  const [busy, setBusy] = useState<string>();
  const running = containers.filter((row) => row.State === 'running');
  const stats = useQueries({
    queries: running.map((row) => ({
      queryKey: ['compose-container-usage', row.Id],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        request<DockerStats>(
          `/api/docker/containers/${encodeURIComponent(row.Id)}/stats`,
          {
            signal,
            silentError: true,
          },
        ),
      enabled: active,
      refetchInterval: active ? 5000 : (false as const),
      retry: false,
    })),
  });
  const usage = new Map(running.map((row, i) => [row.Id, stats[i].data]));

  async function run(id: string, action: string) {
    setBusy(id);
    try {
      await request(`/api/docker/containers/${encodeURIComponent(id)}/action`, {
        method: 'POST',
        data: { action },
      });
      onChanged();
    } catch {
      // 请求错误由统一 API 提示展示。
    } finally {
      setBusy(undefined);
    }
  }

  return (
    <ContentSizedTable
      fillColumn="name"
      rowKey="Id"
      dataSource={containers}
      pagination={false}
      scroll={{ y: 236 }}
      size="small"
      locale={{ emptyText: '项目尚无容器' }}
      columns={[
        {
          key: 'name',
          title: '容器名称',
          render: (_, row) => {
            const name = containerName(row);
            const service = composeServiceName(row);
            const details = [service, row.Image].filter(Boolean).join(' · ');
            const ports = row.Ports?.map(
              ({ PublicPort, PrivatePort, Type }) =>
                `${PublicPort ? `${PublicPort} → ` : ''}${PrivatePort}/${Type}`,
            ).join('、');
            return (
              <div className="compose-service-name">
                <Button
                  type="link"
                  size="small"
                  onClick={() => onContainer(row.Id, name, 'status')}
                >
                  {name}
                </Button>
                {details && (
                  <Tooltip title={[details, ports].filter(Boolean).join(' · ')}>
                    <Typography.Text type="secondary" ellipsis>
                      {details}
                    </Typography.Text>
                  </Tooltip>
                )}
              </div>
            );
          },
        },
        {
          key: 'state',
          title: '状态',
          render: (_, row) => (
            <Tag
              color={
                row.State === 'running'
                  ? 'success'
                  : row.State === 'dead'
                    ? 'error'
                    : undefined
              }
              title={row.Status}
            >
              {states[row.State] || row.Status}
            </Tag>
          ),
        },
        {
          key: 'usage',
          title: '资源用量',
          render: (_, row) => (
            <ContainerUsage
              id={row.Id}
              name={containerName(row)}
              stats={usage.get(row.Id)}
            />
          ),
        },
        {
          key: 'actions',
          title: '操作',
          render: (_, row) => {
            const name = containerName(row);
            const started = ['running', 'paused', 'restarting'].includes(
              row.State,
            );
            const action = started ? 'stop' : 'start';
            return (
              <Space size={4}>
                <Button
                  type="link"
                  size="small"
                  disabled={!active || row.State !== 'running'}
                  onClick={() => onTerminal(row.Id)}
                >
                  终端
                </Button>
                <Button
                  type="link"
                  size="small"
                  onClick={() => onContainer(row.Id, name, 'logs')}
                >
                  日志
                </Button>
                <Dropdown
                  trigger={['click']}
                  menu={{
                    items: [
                      {
                        key: action,
                        label: started ? '停止' : '启动',
                        disabled: !active || !!busy,
                      },
                      {
                        key: 'restart',
                        label: '重启',
                        disabled: !active || !!busy,
                      },
                      { key: 'stats', label: '实时监控' },
                    ],
                    onClick: ({ key }) => {
                      if (key === 'stats') onContainer(row.Id, name, key);
                      else void run(row.Id, key);
                    },
                  }}
                >
                  <Button
                    type="text"
                    size="small"
                    loading={busy === row.Id}
                    icon={<Ellipsis size={16} />}
                    aria-label={`更多容器操作 · ${name}`}
                  />
                </Dropdown>
              </Space>
            );
          },
        },
      ]}
    />
  );
}
