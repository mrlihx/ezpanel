import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, App, Badge, Button, Input, Modal, Space } from 'antd';
import {
  databaseInstanceAction,
  databaseInstances,
  applicationVersions as fetchAppReleases,
  type Application,
  type DatabaseInstance,
} from '../../api/software';
import ContentSizedTable from '../../components/ContentSizedTable';
import { LAYOUT } from '../../styles/tokens';
import DatabaseInstanceControl from '../database/DatabaseInstanceControl';
import DatabaseConnectionDetails from '../database/DatabaseConnectionDetails';

export default function DatabaseInstances({
  app,
  initialInstance,
  busy,
  onClose,
  onCreate,
  onUpdate,
}: {
  app: Application;
  initialInstance?: string;
  busy: boolean;
  onClose: () => void;
  onCreate: () => void;
  onUpdate: (instance: DatabaseInstance) => void;
}) {
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const [connectionId, setConnectionId] = useState<string>();
  const instances = useQuery({
    queryKey: ['database-instances', app.id],
    queryFn: ({ signal }) => databaseInstances(app.id, signal),
  });
  const rows = instances.data?.list;
  const focused = rows?.find((row) => row.id === initialInstance);
  const connection = rows?.find((row) => row.id === connectionId);
  function refresh() {
    void instances.refetch();
    for (const queryKey of [
      ['software-catalog'],
      ['db-env'],
      ['db-servers'],
      ['nosql-env', app.id],
      ['nosql-connections', app.id],
    ])
      void client.invalidateQueries({ queryKey });
  }
  const [checkingVersion, setCheckingVersion] = useState(false);
  async function checkVersion() {
    if (checkingVersion) return;
    setCheckingVersion(true);
    try {
      await fetchAppReleases(app.id, true);
      refresh();
      void message.success(`已检查 ${app.name} 可用更新`);
    } finally {
      setCheckingVersion(false);
    }
  }
  function remove(instance: DatabaseInstance) {
    let name = '';
    modal.confirm({
      title: `卸载「${instance.name}」？`,
      content: (
        <Space direction="vertical">
          <span>
            {app.id === 'memcached'
              ? '将移除该实例的容器，配置会保留。'
              : app.id === 'redis'
                ? '请在实例运行时清空所有逻辑库中的键，再卸载实例。'
                : '请在实例运行时删除全部用户数据库，再卸载实例。'}
          </span>
          <Input
            placeholder={`输入 ${instance.name} 确认`}
            onChange={(event) => {
              name = event.target.value;
            }}
          />
        </Space>
      ),
      okButtonProps: { danger: true },
      onOk: async () => {
        if (name !== instance.name) {
          void message.error('请输入完整实例名称');
          throw new Error('实例名称不符');
        }
        await databaseInstanceAction(app.id, instance.id, 'remove');
        refresh();
        void message.success('实例已卸载');
      },
    });
  }
  return (
    <Modal
      open
      centered
      title={`${app.name} 实例管理`}
      width={LAYOUT.databaseModalWidth}
      onCancel={onClose}
      footer={null}
    >
      <Space direction="vertical" style={{ width: '100%' }}>
        <Button type="primary" disabled={busy} onClick={onCreate}>
          新建实例
        </Button>
        {instances.error && (
          <Alert
            type="error"
            message={instances.error.message}
            action={
              <Button onClick={() => void instances.refetch()}>重试</Button>
            }
          />
        )}
        <ContentSizedTable<DatabaseInstance>
          fillColumn="name"
          rowKey="id"
          loading={instances.isPending}
          dataSource={
            focused
              ? [focused, ...rows!.filter((row) => row.id !== focused.id)]
              : rows
          }
          locale={{ emptyText: '暂无已部署实例' }}
          pagination={{ defaultPageSize: 10, showSizeChanger: true }}
          columns={[
            { title: '实例名称', dataIndex: 'name' },
            {
              title: '版本',
              dataIndex: 'version',
              render: (version: string, row) => (
                <Button
                  type="link"
                  size="small"
                  style={{ padding: 0 }}
                  loading={checkingVersion}
                  title="点击检查更新"
                  onClick={() => void checkVersion()}
                >
                  {version}
                  {row.updateVersion ? ` → ${row.updateVersion}` : ''}
                </Button>
              ),
            },
            {
              title: '本机端口',
              dataIndex: 'port',
              render: (port: number) => port || '—',
            },
            {
              title: '状态',
              key: 'status',
              render: (_, row) => (
                <Badge
                  status={row.running ? 'success' : 'default'}
                  text={row.running ? '运行中' : '已停止'}
                />
              ),
            },
            {
              title: '操作',
              render: (_, row) => (
                <Space size={8} wrap>
                  <Button type="link" onClick={() => setConnectionId(row.id)}>
                    连接信息
                  </Button>
                  <DatabaseInstanceControl
                    link
                    initialOpen={row.id === initialInstance}
                    engine={app.id}
                    instanceId={row.id}
                    name={row.name}
                    running={row.running}
                    onChanged={refresh}
                  />
                  {row.updateVersion && (
                    <Button
                      type="link"
                      disabled={busy}
                      onClick={() => onUpdate(row)}
                    >
                      升级
                    </Button>
                  )}
                  <Button
                    type="link"
                    danger
                    style={{ padding: 0 }}
                    disabled={busy}
                    onClick={() => remove(row)}
                  >
                    卸载
                  </Button>
                </Space>
              ),
            },
          ]}
        />
      </Space>
      {connection && (
        <Modal
          open
          centered
          title={`${connection.name} · 连接信息`}
          footer={null}
          onCancel={() => setConnectionId(undefined)}
        >
          <DatabaseConnectionDetails
            instance={connection}
            engine={app.id}
            showRemoteToggle
          />
        </Modal>
      )}
    </Modal>
  );
}
