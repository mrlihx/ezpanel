import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, App, Button, Modal, Space, Typography } from 'antd';
import {
  applicationVersions as fetchAppReleases,
  runtimeEnvironments,
  runtimeEnvironmentLabel,
  type Application,
  type RuntimeEnvironment,
} from '../../api/software';
import ContentSizedTable from '../../components/ContentSizedTable';
import { LAYOUT } from '../../styles/tokens';

export default function RuntimeEnvironments({
  app,
  busy,
  onClose,
  onInstall,
  onUninstall,
}: {
  app: Application;
  busy: boolean;
  onClose: () => void;
  onInstall: () => void;
  onUninstall: (environment: RuntimeEnvironment) => void;
}) {
  const { message } = App.useApp();
  const client = useQueryClient();
  const [checkingVersion, setCheckingVersion] = useState(false);
  const environments = useQuery({
    queryKey: ['runtime-environments', app.id],
    queryFn: ({ signal }) => runtimeEnvironments(app.id, signal),
  });
  async function checkVersion() {
    if (checkingVersion) return;
    setCheckingVersion(true);
    try {
      await fetchAppReleases(app.id, true);
      await Promise.all([
        environments.refetch(),
        client.invalidateQueries({ queryKey: ['software'] }),
      ]);
      void message.success(`已检查 ${app.name} 可用更新`);
    } finally {
      setCheckingVersion(false);
    }
  }
  return (
    <Modal
      open
      centered
      width={LAYOUT.databaseModalWidth}
      title={`${app.name} 环境管理`}
      footer={null}
      onCancel={onClose}
    >
      <Space direction="vertical" style={{ width: '100%' }}>
        <Button
          type="primary"
          disabled={!app.installable || busy}
          onClick={onInstall}
        >
          新建环境
        </Button>
        {environments.error && (
          <Alert
            type="error"
            message={environments.error.message}
            action={
              <Button onClick={() => void environments.refetch()}>重试</Button>
            }
          />
        )}
        <ContentSizedTable<RuntimeEnvironment>
          fillColumn="projects"
          rowKey="id"
          dataSource={environments.data?.list}
          loading={environments.isPending}
          locale={{ emptyText: '暂无运行环境' }}
          pagination={{ defaultPageSize: 10, showSizeChanger: true }}
          columns={[
            {
              title: '已安装版本',
              key: 'version',
              render: (_, row) => (
                <Button
                  type="link"
                  size="small"
                  style={{ padding: 0 }}
                  loading={checkingVersion}
                  title="点击检查更新"
                  onClick={() => void checkVersion()}
                >
                  {runtimeEnvironmentLabel(row)}
                </Button>
              ),
            },
            {
              title: '关联项目',
              key: 'projects',
              render: (_, row) =>
                row.projects.length ? (
                  row.projects.map((project) => (
                    <div key={project.id} style={{ overflowWrap: 'anywhere' }}>
                      {project.name}
                    </div>
                  ))
                ) : (
                  <Typography.Text type="secondary">—</Typography.Text>
                ),
            },
            {
              title: '操作',
              render: (_, row) => (
                <Button
                  type="link"
                  danger
                  disabled={busy || row.projects.length > 0}
                  onClick={() => onUninstall(row)}
                >
                  卸载
                </Button>
              ),
            },
          ]}
        />
      </Space>
    </Modal>
  );
}
