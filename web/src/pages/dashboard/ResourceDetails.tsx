import type { ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  ConfigProvider,
  Descriptions,
  Popover,
  Space,
  Typography,
  type ThemeConfig,
} from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  processes,
  type Disk,
  type Overview,
  type Process,
} from '../../api/sysinfo';
import { SPACING } from '../../styles/tokens';
import { bytes, number } from './format';
import useProcessTermination from './useProcessTermination';

export type ResourceKind = 'load' | 'cpu' | 'memory' | 'disk';
type Field = [string, ReactNode];

const detailsTheme: ThemeConfig = {
  components: {
    Descriptions: {
      titleMarginBottom: SPACING.xs,
      paddingXS: SPACING.xs,
      fontSizeLG: 14,
    },
    Table: {
      cellPaddingBlockSM: SPACING.xs,
      cellPaddingInlineSM: 6,
      cellFontSizeSM: 13,
    },
  },
};

function fields(title: string, items: Field[]) {
  return (
    <Descriptions
      title={title || undefined}
      layout="vertical"
      size="small"
      column={{ xs: 2, sm: 3, lg: 4 }}
      styles={{
        content: {
          overflowWrap: 'anywhere',
          fontVariantNumeric: 'tabular-nums',
        },
      }}
      items={items.map(([label, value]) => ({
        key: label,
        label,
        children: value ?? '—',
      }))}
    />
  );
}

function TopProcesses({ sort, live }: { sort: 'cpu' | 'mem'; live: boolean }) {
  const list = useQuery({
    queryKey: ['processes', 'top', sort],
    queryFn: ({ signal }) => processes(sort, '', signal, 5),
    enabled: !live,
  });
  const terminateButton = useProcessTermination();
  return (
    <>
      <Typography.Text strong>
        {sort === 'cpu' ? 'CPU' : '内存'}占用率前 5 的进程
      </Typography.Text>
      {list.isError ? (
        <Alert type="error" message="进程信息读取失败" />
      ) : (
        <ContentSizedTable<Process>
          fillColumn="name"
          size="small"
          rowKey={(process) => `${process.pid}:${process.started}`}
          pagination={false}
          loading={list.isPending}
          dataSource={list.data}
          columns={[
            { title: '进程', dataIndex: 'name', ellipsis: true },
            ...(sort === 'mem'
              ? [
                  {
                    title: '内存',
                    width: 78,
                    render: (_: unknown, process: Process) =>
                      bytes(process.memoryBytes),
                  },
                ]
              : []),
            {
              title: '占比',
              width: 64,
              render: (_, process) => number(process[sort], '%'),
            },
            {
              title: '操作',
              width: 52,
              render: (_, process) => terminateButton(process),
            },
          ]}
        />
      )}
    </>
  );
}

function ResourceDetails({
  kind,
  data,
  root,
  live,
}: {
  kind: ResourceKind;
  data: Overview;
  root?: Disk;
  live: boolean;
}) {
  const { cpu, load, mem } = data;
  let content: ReactNode;
  switch (kind) {
    case 'load':
      content = fields('平均负载', [
        ['最近 1 分钟', number(load?.l1)],
        ['最近 5 分钟', number(load?.l5)],
        ['最近 15 分钟', number(load?.l15)],
      ]);
      break;
    case 'cpu': {
      const breakdown: [string, string][] = [
        ['用户态', 'user'],
        ['内核态', 'system'],
        ['Nice', 'nice'],
        ['空闲', 'idle'],
        ['I/O 等待', 'iowait'],
        ['硬中断', 'irq'],
        ['软中断', 'softirq'],
        ['虚拟机抢占', 'steal'],
      ];
      content = (
        <>
          {fields(cpu.model || 'CPU', [
            ['物理核心', cpu.physicalCores],
            ['逻辑核心', cpu.cores],
            [
              'CPU 频率',
              cpu.frequencyMHz > 0 ? number(cpu.frequencyMHz, ' MHz') : '—',
            ],
          ])}
          {cpu.perCore?.length
            ? fields(
                '核心使用率',
                cpu.perCore.map((core) => [
                  core.name.toUpperCase(),
                  number(core.usage, '%'),
                ]),
              )
            : null}
          {cpu.breakdown
            ? fields(
                'CPU 占用',
                breakdown.map(([label, key]) => [
                  label,
                  number(cpu.breakdown?.[key], '%'),
                ]),
              )
            : null}
        </>
      );
      break;
    }
    case 'memory':
      content = fields('系统内存', [
        ['总量', bytes(mem?.total)],
        ['已用', bytes(mem?.used)],
        ['空闲', bytes(mem?.free)],
        ['可用', bytes(mem?.available)],
        ['共享', bytes(mem?.shared)],
        ['缓存', bytes(mem?.cached)],
        ['使用率', number(mem?.usedPercent, '%')],
      ]);
      break;
    case 'disk':
      content = root ? (
        <>
          {fields('', [
            ['挂载点', root.mount],
            ['类型', root.fstype],
            ['文件系统', root.device || '—'],
          ])}
          {root.inodesTotal > 0 &&
            fields('Inode', [
              ['总数', root.inodesTotal.toLocaleString()],
              ['已用', root.inodesUsed.toLocaleString()],
              ['空闲', root.inodesFree.toLocaleString()],
              ['使用率', number(root.inodesUsedPercent, '%')],
            ])}
          {fields('磁盘容量', [
            ['总量', bytes(root.total)],
            ['已用', bytes(root.used)],
            ['空闲', bytes(root.free)],
            ['使用率', number(root.usedPercent, '%')],
          ])}
        </>
      ) : (
        <Typography.Text type="secondary">暂未取得根磁盘信息</Typography.Text>
      );
      break;
  }

  return (
    <Space
      direction="vertical"
      size={SPACING.sm}
      style={{
        width: 'min(352px, calc(100vw - 48px))',
        maxHeight: '52dvh',
        overflow: 'auto',
        overscrollBehavior: 'contain',
      }}
    >
      {content}
      {kind !== 'disk' && (
        <TopProcesses sort={kind === 'memory' ? 'mem' : 'cpu'} live={live} />
      )}
    </Space>
  );
}

export default function ResourcePopover({
  kind,
  title,
  data,
  root,
  live,
  children,
}: {
  kind: ResourceKind;
  title: string;
  data: Overview;
  root?: Disk;
  live: boolean;
  children: ReactNode;
}) {
  return (
    <Popover
      trigger={['hover', 'click']}
      placement="bottom"
      destroyOnHidden
      styles={{ body: { padding: SPACING.sm } }}
      content={
        <ConfigProvider theme={detailsTheme}>
          <ResourceDetails kind={kind} data={data} root={root} live={live} />
        </ConfigProvider>
      }
    >
      <div
        role="group"
        aria-label={`${title}详情`}
        tabIndex={0}
        className="overview-resource-trigger"
      >
        {children}
      </div>
    </Popover>
  );
}
