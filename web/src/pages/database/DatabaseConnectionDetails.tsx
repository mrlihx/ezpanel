import { App, Descriptions, Space, Switch, Tag, Typography } from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import {
  saveDatabaseInstanceRemoteAccess,
  type DatabaseInstance,
} from '../../api/software';

function externalHost() {
  if (typeof window === 'undefined') return '<服务器IP>';
  const host = window.location.hostname;
  if (!host || host === '127.0.0.1' || host === 'localhost' || host === '::1') {
    return '<服务器IP>';
  }
  return host.includes(':') ? `[${host}]` : host;
}

export default function DatabaseConnectionDetails({
  instance,
  engine,
  showRemoteToggle = false,
}: {
  instance: DatabaseInstance;
  engine: string;
  showRemoteToggle?: boolean;
}) {
  const host = externalHost();
  const externalAddr = instance.port ? `${host}:${instance.port}` : '';
  const externalCopyable =
    Boolean(instance.remoteAccess) &&
    Boolean(instance.port) &&
    host !== '<服务器IP>';

  return (
    <Descriptions
      size="small"
      column={1}
      styles={{ label: { width: 112, color: 'var(--ez-text-secondary)' } }}
      items={[
        {
          key: 'container',
          label: '容器连接地址',
          children: (
            <Space direction="vertical" size={2}>
              <Space size={8} wrap>
                <Typography.Text code copyable={instance.networkReady}>
                  {`${instance.containerHost}:${instance.containerPort}`}
                </Typography.Text>
                <Tag color={instance.networkReady ? 'blue' : 'default'}>
                  {instance.networkReady ? 'ezpanel-network' : '未接入内网'}
                </Tag>
              </Space>
              {instance.networkReady && engine === 'mysql' && (
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  供同机容器使用，本面板创建的数据库账号默认支持容器内网连接。
                </Typography.Text>
              )}
            </Space>
          ),
        },
        {
          key: 'host',
          label: '宿主机连接地址',
          children: instance.port ? (
            <Typography.Text code copyable>
              {`127.0.0.1:${instance.port}`}
            </Typography.Text>
          ) : (
            <Typography.Text type="secondary">未映射端口</Typography.Text>
          ),
        },
        {
          key: 'external',
          label: '外部连接地址',
          children:
            instance.remoteAccess && instance.port ? (
              <Space direction="vertical" size={2}>
                <Space size={8} wrap>
                  <Typography.Text code copyable={externalCopyable}>
                    {externalAddr}
                  </Typography.Text>
                  <Tag color="success">已开放</Tag>
                </Space>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  已映射到 0.0.0.0:{instance.port}
                  ，访问范围由当前防火墙和网络配置决定。
                </Typography.Text>
              </Space>
            ) : (
              <Space size={8} wrap>
                <Tag>未开放</Tag>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {instance.port
                    ? engine === 'memcached'
                      ? 'Memcached 仅支持本机与容器内网访问。'
                      : '仅监听本机 127.0.0.1。'
                    : '未映射端口。'}
                </Typography.Text>
              </Space>
            ),
        },
        ...(showRemoteToggle &&
        instance.port &&
        (engine !== 'memcached' || instance.remoteAccess)
          ? [
              {
                key: 'remoteToggle',
                label: '允许远程访问',
                children: (
                  <DatabaseRemoteAccess engine={engine} instance={instance} />
                ),
              },
            ]
          : []),
      ]}
    />
  );
}

export function DatabaseRemoteAccess({
  engine,
  instance,
}: {
  engine: string;
  instance: DatabaseInstance;
}) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const remote = useMutation({
    mutationFn: (enabled: boolean) =>
      saveDatabaseInstanceRemoteAccess(engine, instance.id, enabled),
    onSuccess: async (updated, enabled) => {
      client.setQueryData(['database-instance', engine, instance.id], updated);
      await Promise.all([
        client.invalidateQueries({ queryKey: ['database-instances', engine] }),
        client.invalidateQueries({ queryKey: ['firewall'] }),
      ]);
      void message.success(
        updated.pendingRemoteAccess !== undefined
          ? '已保存，将在实例下次启动时应用'
          : enabled
            ? '已开启远程访问'
            : '已关闭远程访问',
      );
    },
  });
  function change(enabled: boolean) {
    if (!enabled) {
      remote.mutate(false);
      return;
    }
    modal.confirm({
      title: '开启数据库远程访问？',
      content:
        '实例运行后会向所有 IPv4 接口映射该端口。请确认数据库认证、系统防火墙和云安全组符合预期。',
      okText: '确认开启',
      onOk: () => remote.mutateAsync(true),
    });
  }
  return (
    <Space wrap>
      <Switch
        aria-label="允许远程访问"
        checked={instance.pendingRemoteAccess ?? instance.remoteAccess}
        loading={remote.isPending}
        onChange={change}
      />
      {instance.pendingRemoteAccess !== undefined && (
        <Typography.Text type="secondary">下次启动时应用</Typography.Text>
      )}
    </Space>
  );
}
