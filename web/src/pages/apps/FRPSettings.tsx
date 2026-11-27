import { lazy, Suspense, useEffect, useState } from 'react';
import {
  Alert,
  App,
  Badge,
  Button,
  Card,
  Descriptions,
  Grid,
  Modal,
  Select,
  Skeleton,
  Space,
  Tabs,
  Typography,
} from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import ApplicationInstallGuide from '../../components/ApplicationInstallGuide';
import { applicationVersions } from '../../api/software';
import {
  frpAction,
  frpServices,
  type FRPRole,
  type FRPService,
} from '../../api/frp';
import { useMessageBox } from '../../stores/messageBox';
import {
  appSettingsModalStyles,
  type ApplicationSettingsProps,
} from './ApplicationSettings';
import FRPConfiguration from './FRPConfiguration';
import './frp.css';
const SoftwareLogContent = lazy(() => import('./SoftwareLogContent'));

export default function FRPSettings({
  app,
  busy,
  onClose,
}: ApplicationSettingsProps) {
  const { modal, message } = App.useApp();
  const { md } = Grid.useBreakpoint();
  const client = useQueryClient();
  const [tab, setTab] = useState('service');
  const [logRole, setLogRole] = useState<FRPRole>('frps');
  const [dirty, setDirty] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [installing, setInstalling] = useState<FRPRole>();
  const [version, setVersion] = useState<string>();
  const services = useQuery({
    queryKey: ['frp-services'],
    queryFn: frpServices,
  });
  const versions = useQuery({
    queryKey: ['app-versions', 'frp', 'official', ''],
    queryFn: () => applicationVersions('frp'),
    enabled: !!installing,
  });
  useEffect(() => {
    void client.invalidateQueries({ queryKey: ['frp-services'] });
  }, [
    app.installed,
    app.instanceCount,
    app.version,
    app.running,
    busy,
    client,
  ]);
  const discard = (next: () => void) => {
    if (dirty)
      modal.confirm({
        title: '放弃尚未保存的配置？',
        onOk: () => {
          setDirty(false);
          next();
        },
      });
    else next();
  };
  async function action(
    role: FRPRole,
    operation: string,
    selectedVersion?: string,
  ) {
    setSubmitting(true);
    try {
      const result = await frpAction(role, operation, selectedVersion);
      if ('taskId' in result) {
        setInstalling(undefined);
        useMessageBox.getState().show(result.taskId);
        await client.invalidateQueries({ queryKey: ['software-tasks'] });
      } else {
        client.setQueryData(['frp-services'], result);
        await client.invalidateQueries({ queryKey: ['software-catalog'] });
        const labels: Record<string, string> = {
          start: '已启动',
          stop: '已停止',
          restart: '已重启',
          reload: '已重载配置',
        };
        message.success(`${role} ${labels[operation]}`);
      }
    } finally {
      setSubmitting(false);
    }
  }
  const showInstall = (role: FRPRole) => {
    setVersion(app.version ?? undefined);
    setInstalling(role);
  };
  const rows = services.data?.list ?? [];
  const unavailable = busy || submitting;
  return (
    <>
      <Modal
        open
        centered
        title="Frp服务 设置"
        width={1040}
        styles={appSettingsModalStyles}
        footer={null}
        maskClosable={false}
        onCancel={() => discard(onClose)}
        destroyOnHidden
      >
        <Tabs
          className="app-settings-tabs frp-settings-tabs"
          tabPosition={md ? 'left' : 'top'}
          activeKey={tab}
          onChange={(key) => discard(() => setTab(key))}
          destroyOnHidden
          items={[
            {
              key: 'service',
              label: '服务',
              children: (
                <div className="frp-service-content">
                  <div className="app-settings-toolbar">
                    <Typography.Text type="secondary">
                      默认安装 frps，frpc
                      可单独安装。两端独立启停，卸载时保留配置和日志。
                    </Typography.Text>
                    <Button
                      onClick={() => {
                        void services.refetch();
                        void client.invalidateQueries({
                          queryKey: ['software-catalog'],
                        });
                      }}
                      loading={services.isFetching}
                    >
                      刷新
                    </Button>
                  </div>
                  {services.error && (
                    <Alert type="error" message={services.error.message} />
                  )}
                  {services.isPending ? (
                    <Skeleton active />
                  ) : (
                    <div className="frp-service-grid">
                      {rows.map((row) => (
                        <ServiceCard
                          key={row.role}
                          service={row}
                          busy={unavailable}
                          onAction={(operation) => {
                            if (operation === 'install') {
                              showInstall(row.role);
                              return;
                            }
                            if (
                              operation === 'uninstall' ||
                              operation === 'reinstall'
                            )
                              modal.confirm({
                                title:
                                  operation === 'uninstall'
                                    ? `卸载 ${row.role} 服务？`
                                    : `重新安装 ${row.role} ${row.version}？`,
                                content:
                                  '保留配置文件和日志。重新安装会保留原运行状态。',
                                okButtonProps: {
                                  danger: operation === 'uninstall',
                                },
                                onOk: () => action(row.role, operation),
                              });
                            else if (
                              operation === 'restart' ||
                              operation === 'stop'
                            )
                              modal.confirm({
                                title: `${operation === 'restart' ? '重启' : '停止'} ${row.role}？`,
                                content: '现有穿透连接将会中断。',
                                onOk: () => action(row.role, operation),
                              });
                            else void action(row.role, operation);
                          }}
                        />
                      ))}
                    </div>
                  )}
                </div>
              ),
            },
            ...(['frps', 'frpc'] as const).map((role) => ({
              key: role,
              label: role === 'frps' ? 'Frp服务端' : 'Frp客户端',
              children: services.isPending ? (
                <Skeleton active />
              ) : services.isError ? (
                <Alert
                  showIcon
                  type="error"
                  message={services.error.message}
                  action={
                    <Button onClick={() => void services.refetch()}>
                      重新检测
                    </Button>
                  }
                />
              ) : rows.find((row) => row.role === role)?.installed ? (
                <FRPConfiguration
                  role={role}
                  busy={unavailable}
                  onDirty={setDirty}
                />
              ) : (
                <div style={{ height: '100%', overflowY: 'auto' }}>
                  <ApplicationInstallGuide
                    name={role}
                    install={() => showInstall(role)}
                    onDetect={() => void services.refetch()}
                    installDisabled={unavailable}
                    description="选择官方版本安装，保留已有配置。"
                  />
                </div>
              ),
            })),
            {
              key: 'logs',
              label: '日志',
              children: (
                <div className="frp-logs">
                  <Tabs
                    activeKey={logRole}
                    onChange={(key) => setLogRole(key as FRPRole)}
                    items={[
                      { key: 'frps', label: 'frps' },
                      { key: 'frpc', label: 'frpc' },
                    ]}
                  />
                  <Suspense fallback={<Skeleton active />}>
                    <SoftwareLogContent
                      key={logRole}
                      minHeight={0}
                      url={`/api/software/frp/services/${logRole}/log?source=file`}
                    />
                  </Suspense>
                </div>
              ),
            },
          ]}
        />
      </Modal>
      <Modal
        open={!!installing}
        title={`安装 ${installing ?? ''}`}
        confirmLoading={submitting}
        okButtonProps={{
          disabled:
            !(version ?? versions.data?.list[0]?.version) ||
            versions.isFetching,
        }}
        onCancel={() => setInstalling(undefined)}
        onOk={() =>
          installing &&
          action(
            installing,
            'install',
            version ?? versions.data?.list[0]?.version,
          )
        }
      >
        <Typography.Paragraph>
          从 Frp
          官方源安装所选服务，已有配置会保留。新装服务在完成配置后手动启动。
        </Typography.Paragraph>
        {versions.error && (
          <Alert type="error" message={versions.error.message} />
        )}
        <Select
          aria-label="安装版本"
          style={{ width: '100%' }}
          loading={versions.isFetching}
          value={version ?? versions.data?.list[0]?.version}
          onChange={setVersion}
          options={versions.data?.list.map((row) => ({
            value: row.version,
            label: row.version,
          }))}
        />
      </Modal>
    </>
  );
}
function ServiceCard({
  service: row,
  busy,
  onAction,
}: {
  service: FRPService;
  busy: boolean;
  onAction: (action: string) => void;
}) {
  return (
    <Card
      title={row.role === 'frps' ? 'frps 服务端' : 'frpc 客户端'}
      extra={
        <Badge
          status={
            !row.installed ? 'default' : row.running ? 'success' : 'error'
          }
          text={!row.installed ? '未安装' : row.running ? '运行中' : '已停止'}
        />
      }
    >
      <Descriptions
        size="small"
        column={1}
        items={[
          { key: 'version', label: '版本', children: row.version || '—' },
          {
            key: 'config',
            label: '配置文件',
            children: (
              <Typography.Text className="frp-path" copyable>
                {row.configPath}
              </Typography.Text>
            ),
          },
          {
            key: 'log',
            label: '日志文件',
            children: (
              <Typography.Text className="frp-path" copyable>
                {row.logPath}
              </Typography.Text>
            ),
          },
        ]}
      />
      <Space wrap className="frp-service-actions">
        {!row.installed ? (
          <Button
            type="primary"
            disabled={busy}
            onClick={() => onAction('install')}
          >
            安装
          </Button>
        ) : (
          <>
            <Button
              type="primary"
              disabled={busy}
              onClick={() => onAction(row.running ? 'stop' : 'start')}
            >
              {row.running ? '停止' : '启动'}
            </Button>
            <Button
              disabled={busy || !row.running}
              onClick={() => onAction('restart')}
            >
              重启
            </Button>
            {row.role === 'frpc' && (
              <Button
                disabled={busy || !row.running}
                onClick={() => onAction('reload')}
              >
                重载配置
              </Button>
            )}
            <Button disabled={busy} onClick={() => onAction('reinstall')}>
              重新安装
            </Button>
            <Button
              danger
              disabled={busy}
              onClick={() => onAction('uninstall')}
            >
              卸载
            </Button>
          </>
        )}
      </Space>
    </Card>
  );
}
