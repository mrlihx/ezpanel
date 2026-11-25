import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Descriptions,
  Modal,
  Space,
  Spin,
  Tag,
} from 'antd';
import {
  projectService,
  restartRuntime,
  toggleWebsite,
  type Website,
} from '../../api/website';
import CronForm from '../cron/CronForm';
import { LAYOUT } from '../../styles/tokens';
import { formatTime } from '../../utils/format';

const actions = { start: '启动', stop: '停止', restart: '重启' };

export default function ProjectService({
  site,
  manageable,
  onChanged,
}: {
  site: Website;
  manageable: boolean;
  onChanged: () => Promise<void>;
}) {
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const query = useQuery({
    queryKey: ['project-service', site.id],
    queryFn: ({ signal }) => projectService(site.id, signal),
  });
  const action = useMutation({
    mutationFn: async (action: keyof typeof actions) => {
      if (action === 'restart' || (action === 'start' && site.enabled)) {
        await restartRuntime(site.id);
      } else {
        await toggleWebsite(site.id, action === 'start');
      }
    },
    onSuccess: (_, action) => void message.success(`项目已${actions[action]}`),
    onSettled: async () => {
      await Promise.all([query.refetch(), onChanged()]);
    },
  });
  const data = query.data;
  if (query.isPending) return <Spin />;
  if (query.error)
    return (
      <Alert
        type="error"
        message={query.error.message}
        action={<Button onClick={() => void query.refetch()}>重试</Button>}
      />
    );
  if (!data) return null;
  const { container } = data;
  const ports = Object.entries(container.ports ?? {})
    .map(([port, host]) => `${port} → ${host}`)
    .join('，');
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {data.pending && (
        <Alert
          type="info"
          showIcon
          message="运行配置已保存，下次启动或重启时生效。"
        />
      )}
      <Descriptions
        column={{ xs: 1, md: 2 }}
        size="small"
        bordered
        items={[
          {
            key: 'state',
            label: '服务状态',
            children: (
              <Tag color={container.running ? 'green' : 'default'}>
                {container.running
                  ? '运行中'
                  : site.runtimeConfigured
                    ? '已停止'
                    : '待配置'}
              </Tag>
            ),
          },
          {
            key: 'port',
            label: '配置端口',
            children: data.configuredPort || '自动识别',
          },
          { key: 'target', label: '服务地址', children: data.target || '—' },
          { key: 'bindings', label: '端口映射', children: ports || '—' },
          {
            key: 'container',
            label: '容器名称',
            children: container.name || '—',
          },
          {
            key: 'id',
            label: '容器 ID',
            children: container.id.slice(0, 12) || '—',
          },
          { key: 'image', label: '容器镜像', children: container.image || '—' },
          { key: 'pid', label: 'PID', children: container.pid || '—' },
          {
            key: 'started',
            label: '启动时间',
            children:
              container.id &&
              container.startedAt &&
              !container.startedAt.startsWith('0001-')
                ? formatTime(container.startedAt)
                : '—',
          },
        ]}
      />
      <Space wrap>
        {(container.running
          ? (['stop', 'restart'] as const)
          : (['start'] as const)
        ).map((name) => (
          <Button
            key={name}
            disabled={
              !manageable || !site.runtimeConfigured || action.isPending
            }
            loading={action.isPending && action.variables === name}
            onClick={() =>
              modal.confirm({
                title: `${actions[name]}「${site.name}」？`,
                onOk: () => action.mutateAsync(name),
              })
            }
          >
            {actions[name]}
          </Button>
        ))}
        <Button
          disabled={!site.runtimeConfigured}
          onClick={() => setScheduleOpen(true)}
        >
          设置定时重启
        </Button>
        <Button loading={query.isFetching} onClick={() => void query.refetch()}>
          刷新
        </Button>
      </Space>
      <Modal
        title="设置定时重启"
        open={scheduleOpen}
        width={LAYOUT.logDrawerWidth}
        footer={null}
        destroyOnHidden
        onCancel={() => setScheduleOpen(false)}
      >
        <CronForm
          project={site}
          onCancel={() => setScheduleOpen(false)}
          onSaved={() => {
            setScheduleOpen(false);
            void client.invalidateQueries({ queryKey: ['crons'] });
            void message.success('项目重启计划已添加');
          }}
        />
      </Modal>
    </Space>
  );
}
