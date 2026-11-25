import { lazy, Suspense, useState } from 'react';
import {
  Alert,
  App,
  Badge,
  Button,
  Descriptions,
  Grid,
  Modal,
  Popover,
  Skeleton,
  Space,
  Tabs,
  Typography,
  theme,
} from 'antd';
import { Pause, Play } from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  databaseInstance,
  databaseInstanceAction,
  databaseInstanceSettings,
  saveDatabaseInstanceSettings,
} from '../../api/software';
import {
  DatabasePort,
  DatabaseOptions,
  DatabaseStatus,
  DatabaseLogs,
  PostgresExtensions,
} from './DatabaseInstancePanels';
import { appSettingsModalStyles } from '../apps/ApplicationSettings';
import DatabaseConnectionDetails from './DatabaseConnectionDetails';

const CodeEditor = lazy(() => import('../../components/CodeEditor'));
const actions = { start: '启动', stop: '停止', restart: '重启' };

const labels = {
  service: '服务',
  config: '配置文件',
  status: '负载状态',
  performance: '性能调整',
  options: '配置项',
  error: '错误日志',
  slow: '慢日志',
  binlog: '二进制日志',
  log: '日志',
  extensions: '扩展安装',
};
type Section = keyof typeof labels;
const sections: Record<string, Section[]> = {
  mysql: [
    'service',
    'config',
    'status',
    'performance',
    'error',
    'slow',
    'binlog',
  ],
  redis: ['service', 'performance', 'config', 'status', 'log'],
  mongodb: ['service', 'options', 'config', 'log'],
  postgresql: [
    'service',
    'config',
    'status',
    'performance',
    'log',
    'slow',
    'extensions',
  ],
  memcached: ['service', 'config', 'status', 'performance'],
};

export default function DatabaseInstanceControl({
  engine,
  instanceId,
  name,
  version,
  running,
  onChanged,
  onAuthentication,
  link = false,
  initialOpen = false,
}: {
  engine: string;
  instanceId: string;
  name: string;
  version?: string;
  running: boolean;
  onChanged: () => void;
  onAuthentication?: () => void;
  link?: boolean;
  initialOpen?: boolean;
}) {
  const { message, modal } = App.useApp();
  const { token } = theme.useToken();
  const client = useQueryClient();
  const [open, setOpen] = useState(initialOpen);
  const [hover, setHover] = useState(false);
  const [tab, setTab] = useState<Section>('service');
  const { md } = Grid.useBreakpoint();
  const [draft, setDraft] = useState<string>();
  const queryKey = ['database-instance', engine, instanceId];
  const detail = useQuery({
    queryKey,
    queryFn: ({ signal }) => databaseInstance(engine, instanceId, signal),
    enabled: open,
  });
  const config = useQuery({
    queryKey: [...queryKey, 'settings'],
    queryFn: ({ signal }) =>
      databaseInstanceSettings(engine, instanceId, signal),
    enabled: open && tab === 'config',
  });
  const control = useMutation({
    mutationFn: (action: keyof typeof actions) =>
      databaseInstanceAction(engine, instanceId, action),
    onSuccess: async (_, action) => {
      void client.invalidateQueries({
        queryKey,
      });
      onChanged();
      void message.success(`实例已${actions[action]}`);
    },
  });
  const save = useMutation({
    mutationFn: () =>
      saveDatabaseInstanceSettings(
        engine,
        instanceId,
        draft ?? config.data!.content,
        config.data!.revision,
      ),
    onSuccess: (settings) => {
      client.setQueryData([...queryKey, 'settings'], settings);
      void client.invalidateQueries({ queryKey: [...queryKey, 'options'] });
      setDraft(undefined);
      void message.success('配置已保存，实例启动或重启后应用');
    },
  });
  const active = open ? (detail.data?.running ?? running) : running;
  const action = active ? 'stop' : 'start';
  const perform = (value: keyof typeof actions) => {
    setHover(false);
    if (value === 'start') control.mutate(value);
    else
      modal.confirm({
        title: `${actions[value]}「${name}」？`,
        onOk: () => control.mutateAsync(value),
      });
  };
  const dirty = draft !== undefined && draft !== config.data?.content;
  function close() {
    const finish = () => {
      setOpen(false);
      setDraft(undefined);
    };
    if (dirty) modal.confirm({ title: '放弃尚未保存的配置？', onOk: finish });
    else finish();
  }
  function show() {
    setHover(false);
    setTab('service');
    setOpen(true);
  }
  const controls = (
    <Space wrap>
      <Button
        type={!active ? 'primary' : 'default'}
        disabled={control.isPending}
        onClick={() => perform(action)}
      >
        {actions[action]}
      </Button>
      <Button
        disabled={!active || control.isPending}
        onClick={() => perform('restart')}
      >
        重启
      </Button>
    </Space>
  );
  return (
    <>
      {link ? (
        <Button type="link" style={{ padding: 0 }} onClick={show}>
          设置
        </Button>
      ) : (
        <Popover open={hover} onOpenChange={setHover} content={controls}>
          <Button
            loading={control.isPending}
            icon={
              active ? (
                <Play
                  size={13}
                  strokeWidth={2.2}
                  fill="currentColor"
                  style={{ color: token.colorSuccess }}
                />
              ) : (
                <Pause
                  size={13}
                  strokeWidth={2.2}
                  style={{ color: token.colorError }}
                />
              )
            }
            iconPosition="end"
            onClick={show}
          >
            {name}
            {version ? ` ${version}` : ''}
          </Button>
        </Popover>
      )}
      <Modal
        open={open}
        centered
        title={`${name} · 实例设置`}
        width={1040}
        styles={appSettingsModalStyles}
        footer={null}
        onCancel={close}
        destroyOnHidden
        maskClosable={false}
      >
        <Tabs
          className="app-settings-tabs"
          tabPosition={md ? 'left' : 'top'}
          activeKey={tab}
          onChange={(key) => setTab(key as Section)}
          items={(sections[engine] ?? ['service', 'config']).map((section) => ({
            key: section,
            label:
              section === 'status' &&
              (engine === 'mysql' || engine === 'postgresql')
                ? '当前状态'
                : labels[section],
            children:
              tab !== section ? null : section === 'service' ? (
                <div className="app-settings-stack">
                  {detail.isPending && (
                    <Skeleton active paragraph={{ rows: 2 }} />
                  )}
                  {detail.error && (
                    <Alert
                      type="error"
                      showIcon
                      message={detail.error.message}
                    />
                  )}
                  {detail.data && (
                    <>
                      <div className="app-settings-section">
                        <div className="app-settings-section-title">
                          基本信息
                        </div>
                        <Descriptions
                          size="small"
                          column={2}
                          styles={{
                            label: {
                              width: 84,
                              color: 'var(--ez-text-secondary)',
                            },
                          }}
                          items={[
                            {
                              key: 'status',
                              label: '服务状态',
                              children: (
                                <Badge
                                  status={active ? 'success' : 'default'}
                                  text={active ? '运行中' : '已停止'}
                                />
                              ),
                            },
                            {
                              key: 'version',
                              label: '当前版本',
                              children: (
                                <Typography.Text strong>
                                  {detail.data.version}
                                </Typography.Text>
                              ),
                            },
                          ]}
                        />
                        {detail.data.dataDir && (
                          <Descriptions
                            size="small"
                            column={1}
                            style={{ marginTop: 8 }}
                            styles={{
                              label: {
                                width: 84,
                                color: 'var(--ez-text-secondary)',
                              },
                            }}
                            items={[
                              {
                                key: 'data',
                                label: '数据目录',
                                children: (
                                  <Typography.Text
                                    code
                                    copyable
                                    style={{ wordBreak: 'break-all' }}
                                  >
                                    {detail.data.dataDir}
                                  </Typography.Text>
                                ),
                              },
                            ]}
                          />
                        )}
                        <div style={{ marginTop: 12 }}>
                          <Space wrap>
                            {controls}
                            {onAuthentication && (
                              <Button
                                onClick={() => {
                                  close();
                                  onAuthentication();
                                }}
                              >
                                连接认证
                              </Button>
                            )}
                          </Space>
                        </div>
                      </div>
                      <div className="app-settings-section">
                        <div className="app-settings-section-title">
                          连接信息
                        </div>
                        <DatabaseConnectionDetails
                          instance={detail.data}
                          engine={engine}
                        />
                      </div>
                      <div className="app-settings-section">
                        <div className="app-settings-section-title">
                          网络与端口
                        </div>
                        <DatabasePort engine={engine} instance={detail.data} />
                      </div>
                    </>
                  )}
                </div>
              ) : section === 'config' ? (
                <Space
                  direction="vertical"
                  size="middle"
                  style={{ width: '100%' }}
                >
                  {config.isPending && <Skeleton active />}
                  {config.error && (
                    <Alert
                      type="error"
                      showIcon
                      message={config.error.message}
                      action={
                        <Button onClick={() => void config.refetch()}>
                          重试
                        </Button>
                      }
                    />
                  )}
                  {config.data && (
                    <>
                      <div className="app-settings-toolbar">
                        <Typography.Text code copyable>
                          {config.data.path}
                        </Typography.Text>
                        <Space>
                          <Button
                            disabled={save.isPending}
                            onClick={() => {
                              const reload = () => {
                                setDraft(undefined);
                                void config.refetch();
                              };
                              if (dirty)
                                modal.confirm({
                                  title: '丢弃未保存的修改并重新加载？',
                                  onOk: reload,
                                });
                              else reload();
                            }}
                          >
                            重新加载
                          </Button>
                          <Button
                            type="primary"
                            loading={save.isPending}
                            disabled={!dirty && !config.data.missing}
                            onClick={() => save.mutate()}
                          >
                            保存配置
                          </Button>
                        </Space>
                      </div>
                      {engine === 'memcached' && (
                        <Typography.Text type="secondary">
                          每行填写一个参数或参数值。
                        </Typography.Text>
                      )}
                      <div className="app-settings-editor-box">
                        <Suspense fallback={<Skeleton active />}>
                          <CodeEditor
                            path={config.data.path}
                            value={draft ?? config.data.content}
                            onChange={setDraft}
                          />
                        </Suspense>
                      </div>
                      <Typography.Text type="secondary">
                        配置保存后，需在服务页重启实例使其生效。
                      </Typography.Text>
                    </>
                  )}
                </Space>
              ) : section === 'performance' || section === 'options' ? (
                <DatabaseOptions
                  engine={engine}
                  id={instanceId}
                  instance={detail.data}
                  running={active}
                  onRestart={() => perform('restart')}
                />
              ) : section === 'status' ? (
                <DatabaseStatus
                  engine={engine}
                  id={instanceId}
                  running={active}
                />
              ) : section === 'extensions' ? (
                <PostgresExtensions id={instanceId} running={active} />
              ) : (
                <DatabaseLogs engine={engine} id={instanceId} kind={section} />
              ),
          }))}
        />
      </Modal>
    </>
  );
}
