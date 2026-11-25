import { useState } from 'react';
import { Alert, Button, Descriptions, Select, Space, Typography } from 'antd';
import { useQuery } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  runtimeLoad,
  type RuntimeConnection,
  type RuntimeOpenFile,
} from '../../api/website';
import { formatSize, formatTime } from '../../utils/format';

export default function ProjectLoad({ id }: { id: string }) {
  const [pid, setPid] = useState<number>();
  const query = useQuery({
    queryKey: ['project-load', id, pid],
    queryFn: ({ signal }) => runtimeLoad(id, pid, signal),
    refetchInterval: 5000,
  });
  const data = query.data;
  const process = data?.process;
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Space wrap>
        <Typography.Text strong>进程</Typography.Text>
        <Select
          aria-label="选择进程"
          value={process?.pid}
          style={{ minWidth: 180 }}
          placeholder="当前无进程"
          options={data?.processes?.map(({ pid, name }) => ({
            value: pid,
            label: `${pid} · ${name}`,
          }))}
          loading={query.isPending}
          disabled={!data?.processes?.length}
          onChange={setPid}
        />
        <Button loading={query.isFetching} onClick={() => void query.refetch()}>
          刷新
        </Button>
      </Space>
      {query.error && <Alert type="error" message={query.error.message} />}
      {data && !process && (
        <Alert type="info" message="当前没有正在运行的项目进程" />
      )}
      {process && (
        <>
          <Descriptions
            title="基础信息"
            size="small"
            bordered
            column={{ xs: 1, sm: 2, lg: 3 }}
            items={[
              { key: 'name', label: '名称', children: process.name },
              {
                key: 'status',
                label: '状态',
                children: process.status?.join('、') || '—',
              },
              { key: 'user', label: '用户', children: process.user || '—' },
              {
                key: 'started',
                label: '启动时间',
                children: formatTime(new Date(process.started).toISOString()),
              },
              { key: 'pid', label: 'PID', children: process.pid },
              { key: 'ppid', label: 'PPID', children: process.parent },
              { key: 'threads', label: '线程', children: process.threads },
              { key: 'sockets', label: 'Socket', children: process.sockets },
              {
                key: 'cpu',
                label: 'CPU（运行期平均）',
                children: `${process.cpu.toFixed(2)}%`,
              },
              { key: 'rss', label: '内存', children: formatSize(process.rss) },
              {
                key: 'read',
                label: 'I/O 读取',
                children: formatSize(process.read),
              },
              {
                key: 'write',
                label: 'I/O 写入',
                children: formatSize(process.write),
              },
              {
                key: 'command',
                label: '执行命令',
                span: 'filled',
                children: (
                  <Typography.Text style={{ overflowWrap: 'anywhere' }}>
                    {process.command}
                  </Typography.Text>
                ),
              },
            ]}
          />
          <Typography.Text strong>网络</Typography.Text>
          {data?.connectionError && (
            <Alert type="warning" message={data.connectionError} />
          )}
          <ContentSizedTable<RuntimeConnection>
            fillColumn="remoteIP"
            rowKey={(row) =>
              `${row.fd}-${row.family}-${row.localaddr.port}-${row.remoteaddr.port}`
            }
            size="small"
            pagination={{ defaultPageSize: 10, showSizeChanger: true }}
            dataSource={data?.connections ?? []}
            locale={{
              emptyText: data?.connectionError
                ? '暂时无法读取网络连接'
                : '当前进程没有网络连接',
            }}
            columns={[
              {
                title: '远端地址',
                key: 'remoteIP',
                render: (_, row) => row.remoteaddr.ip || '—',
              },
              {
                title: '远端端口',
                render: (_, row) => row.remoteaddr.port || '—',
              },
              {
                title: '协议',
                render: (_, row) =>
                  row.family === 1 ? 'UNIX' : row.type === 1 ? 'TCP' : 'UDP',
              },
              { title: 'FD', dataIndex: 'fd' },
              {
                title: '本地地址',
                render: (_, row) => row.localaddr.ip || '—',
              },
              {
                title: '本地端口',
                render: (_, row) => row.localaddr.port || '—',
              },
              { title: '状态', dataIndex: 'status' },
            ]}
          />
          <Typography.Text strong>打开的文件</Typography.Text>
          <ContentSizedTable<RuntimeOpenFile>
            fillColumn="path"
            rowKey="fd"
            size="small"
            pagination={{ defaultPageSize: 10, showSizeChanger: true }}
            dataSource={data?.openFiles ?? []}
            columns={[
              {
                title: '文件',
                dataIndex: 'path',
                render: (path) => (
                  <Typography.Text style={{ overflowWrap: 'anywhere' }}>
                    {path}
                  </Typography.Text>
                ),
              },
              { title: '模式', dataIndex: 'mode' },
              { title: '位置', dataIndex: 'position' },
              { title: '标记', dataIndex: 'flags' },
              { title: 'FD', dataIndex: 'fd' },
            ]}
          />
        </>
      )}
    </Space>
  );
}
