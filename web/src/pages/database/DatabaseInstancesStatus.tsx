import { App, Badge, Button, Flex, Popover, Space, Typography } from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { applicationService, databaseInstanceAction } from '../../api/software';
import DatabaseInstanceControl from './DatabaseInstanceControl';

type Instance = {
  id: string;
  name: string;
  running?: boolean;
  instanceId?: string;
  native?: boolean;
  version?: string;
};

const actions = { start: '启动', stop: '停止', restart: '重启' };

export default function DatabaseInstancesStatus({
  engine,
  instances,
  onChanged,
}: {
  engine: string;
  instances: Instance[];
  onChanged: () => void;
}) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const control = useMutation({
    mutationFn: ({
      instance,
      action,
    }: {
      instance: Instance;
      action: keyof typeof actions;
    }) =>
      instance.instanceId
        ? databaseInstanceAction(engine, instance.instanceId, action)
        : applicationService(engine, action),
    onSuccess: (_, { instance, action }) => {
      if (instance.instanceId)
        void client.invalidateQueries({
          queryKey: ['database-instance', engine, instance.instanceId],
        });
      onChanged();
      void message.success(`${instance.name}已${actions[action]}`);
    },
  });
  const running = instances.filter((instance) => instance.running).length;
  const perform = (instance: Instance, action: keyof typeof actions) => {
    if (action === 'start') control.mutate({ instance, action });
    else
      modal.confirm({
        title: `${actions[action]}「${instance.name}」？`,
        onOk: () => control.mutateAsync({ instance, action }),
      });
  };
  return (
    <Popover
      trigger={['hover', 'click']}
      title="数据库实例"
      content={
        <Flex
          vertical
          gap={8}
          style={{
            maxHeight: 320,
            overflowY: 'auto',
            maxWidth: 'calc(100vw - 48px)',
          }}
        >
          {instances.map((instance) => (
            <Flex
              key={instance.id}
              justify="space-between"
              align="center"
              gap={16}
              wrap
            >
              <Space>
                <Typography.Text>{instance.name}</Typography.Text>
                <Badge
                  status={instance.running ? 'success' : 'default'}
                  text={
                    instance.running === undefined
                      ? '远程连接'
                      : instance.running
                        ? '运行中'
                        : '已停止'
                  }
                />
              </Space>
              {(instance.instanceId || instance.native) && (
                <Space size={8}>
                  <Button
                    size="small"
                    disabled={control.isPending}
                    onClick={() =>
                      perform(instance, instance.running ? 'stop' : 'start')
                    }
                  >
                    {instance.running ? '停止' : '启动'}
                  </Button>
                  <Button
                    size="small"
                    disabled={!instance.running || control.isPending}
                    onClick={() => perform(instance, 'restart')}
                  >
                    重启
                  </Button>
                  {instance.instanceId && (
                    <DatabaseInstanceControl
                      link
                      engine={engine}
                      instanceId={instance.instanceId}
                      name={instance.name}
                      version={instance.version}
                      running={!!instance.running}
                      onChanged={onChanged}
                    />
                  )}
                </Space>
              )}
            </Flex>
          ))}
        </Flex>
      }
    >
      <Button loading={control.isPending}>
        <Badge
          status={running ? 'success' : 'default'}
          text={`${instances.length} 个实例 · ${running} 个运行中`}
        />
      </Button>
    </Popover>
  );
}
