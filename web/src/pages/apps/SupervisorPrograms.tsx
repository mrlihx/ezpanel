import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Space,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { FolderOpen, Plus, RotateCw } from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  supervisorAction,
  supervisorPrograms,
  type SupervisorProgram,
} from '../../api/supervisor';
import SupervisorProgramEditor from './SupervisorProgramEditor';
import SupervisorLogs from './SupervisorLogs';
import './supervisor.css';

export default function SupervisorPrograms({
  busy,
  active,
}: {
  busy: boolean;
  active: boolean;
}) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const [editing, setEditing] = useState<{
    program?: SupervisorProgram;
    raw: boolean;
  }>();
  const [logging, setLogging] = useState<SupervisorProgram>();
  const query = useQuery({
    queryKey: ['supervisor-programs'],
    queryFn: ({ signal }) => supervisorPrograms(signal),
    enabled: active,
  });
  const action = useMutation({
    mutationFn: supervisorAction,
    onSuccess: async () => {
      void client.invalidateQueries({
        queryKey: ['application-log-sources', 'supervisor'],
      });
      message.success('进程操作已完成');
    },
    onSettled: () => query.refetch(),
  });
  const disabled = busy || action.isPending || !!query.data?.warning;
  const remove = (program: SupervisorProgram) => {
    let removeLogs = false;
    modal.confirm({
      title: `删除进程 ${program.name}？`,
      content: (
        <Space direction="vertical">
          <Typography.Text>将停止此进程组并移除对应配置。</Typography.Text>
          <Checkbox
            onChange={(event) => {
              removeLogs = event.target.checked;
            }}
          >
            同时删除面板管理的该进程日志
          </Checkbox>
        </Space>
      ),
      okButtonProps: { danger: true },
      onOk: () =>
        action.mutateAsync({
          name: program.name,
          revision: program.revision,
          action: 'delete',
          removeLogs,
        }),
    });
  };
  return (
    <div className="app-settings-stack">
      <div className="app-settings-toolbar">
        <Button
          type="primary"
          icon={<Plus size={16} />}
          disabled={disabled}
          onClick={() => setEditing({ raw: false })}
        >
          添加守护进程
        </Button>
        <Button
          icon={<RotateCw size={15} />}
          loading={query.isFetching}
          onClick={() => void query.refetch()}
        >
          刷新
        </Button>
      </div>
      {query.error && <Alert type="error" message={query.error.message} />}
      {query.data?.warning && (
        <Alert type="warning" message={query.data.warning} />
      )}
      <ContentSizedTable<SupervisorProgram>
        rowKey="name"
        size="small"
        fillColumn="remark"
        loading={query.isPending}
        dataSource={query.data?.list}
        scroll={{ x: 1150 }}
        pagination={{ pageSize: 10, hideOnSinglePage: true }}
        columns={[
          { title: '名称', dataIndex: 'name', width: 120 },
          {
            title: '启动命令',
            dataIndex: 'command',
            ellipsis: true,
            width: 150,
          },
          { title: '启动用户', dataIndex: 'user', width: 100 },
          {
            title: 'PID',
            key: 'pid',
            width: 100,
            render: (_, row) => (
              <Tooltip
                title={row.processes
                  .map(
                    (process) =>
                      `${process.name} · ${process.pid || '-'} · ${process.state} ${process.description}`,
                  )
                  .join('\n')}
              >
                <span>
                  {row.processes
                    .map((process) => process.pid)
                    .filter(Boolean)
                    .join(', ') || '-'}
                </span>
              </Tooltip>
            ),
          },
          { title: '进程数量', dataIndex: 'numprocs', width: 90 },
          { title: '优先级', dataIndex: 'priority', width: 80 },
          {
            title: '状态',
            key: 'state',
            width: 120,
            render: (_, row) => {
              const running = row.processes.filter(
                (process) => process.state === 'RUNNING',
              ).length;
              const failed = row.processes.some((process) =>
                ['FATAL', 'BACKOFF', 'EXITED'].includes(process.state),
              );
              return (
                <Tag
                  color={
                    running === row.numprocs
                      ? 'success'
                      : failed
                        ? 'error'
                        : 'default'
                  }
                >
                  {running ? '运行中' : (row.processes[0]?.state ?? 'UNKNOWN')}
                </Tag>
              );
            },
          },
          { title: '备注', dataIndex: 'remark', width: 140, ellipsis: true },
          {
            title: '位置',
            key: 'directory',
            width: 65,
            render: (_, row) =>
              row.directory && (
                <Tooltip title={row.directory}>
                  <Button
                    type="text"
                    aria-label="打开运行目录"
                    icon={<FolderOpen size={16} />}
                    href={`/files?path=${encodeURIComponent(row.directory)}`}
                    target="_blank"
                  />
                </Tooltip>
              ),
          },
          {
            title: '操作',
            key: 'actions',
            fixed: 'right',
            width: 300,
            render: (_, row) => {
              const running = row.processes.some((process) =>
                ['RUNNING', 'STARTING', 'BACKOFF'].includes(process.state),
              );
              return (
                <Space size={8} className="supervisor-actions">
                  <Button type="link" onClick={() => setLogging(row)}>
                    日志
                  </Button>
                  <Button
                    type="link"
                    disabled={disabled}
                    onClick={() =>
                      action.mutate({
                        name: row.name,
                        action: running ? 'stop' : 'start',
                      })
                    }
                  >
                    {running ? '停止' : '启动'}
                  </Button>
                  {running && (
                    <Button
                      type="link"
                      disabled={disabled}
                      onClick={() =>
                        action.mutate({ name: row.name, action: 'restart' })
                      }
                    >
                      重启
                    </Button>
                  )}
                  <Button
                    type="link"
                    disabled={disabled || !row.editable}
                    onClick={() => setEditing({ program: row, raw: false })}
                  >
                    修改
                  </Button>
                  <Button
                    type="link"
                    disabled={disabled || !row.editable}
                    onClick={() => setEditing({ program: row, raw: true })}
                  >
                    配置文件
                  </Button>
                  <Button
                    type="link"
                    danger
                    disabled={disabled || !row.editable}
                    onClick={() => remove(row)}
                  >
                    删除
                  </Button>
                </Space>
              );
            },
          },
        ]}
      />
      {query.data?.list.some((row) => !row.editable) && (
        <Typography.Text type="secondary">
          自定义进程组的配置请在「配置文件」中编辑。
        </Typography.Text>
      )}
      {editing && (
        <SupervisorProgramEditor
          {...editing}
          users={query.data?.users ?? []}
          onClose={() => setEditing(undefined)}
        />
      )}
      {logging && (
        <SupervisorLogs
          program={logging}
          onClose={() => setLogging(undefined)}
        />
      )}
    </div>
  );
}
