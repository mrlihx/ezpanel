import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import LogText from '../../components/LogText';
import { lazy, Suspense, useState } from 'react';
import {
  Alert,
  App,
  Badge,
  Button,
  Descriptions,
  Empty,
  Form,
  Grid,
  Input,
  InputNumber,
  Modal,
  Popover,
  Select,
  Skeleton,
  Space,
  Switch,
  Tabs,
  Typography,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  applicationSettings,
  saveApplicationSettings,
  type Application,
} from '../../api/software';
import {
  nginxErrorLog,
  nginxGuard,
  nginxLoad,
  nginxPerformance,
  saveNginxGuard,
  saveNginxPerformance,
  type NginxPerformanceValues,
} from '../../api/nginx';
import { appSettingsModalStyles } from './ApplicationSettings';
import { MONO_FONT, SPACING, TERMINAL_COLORS } from '../../styles/tokens';

const CodeEditor = lazy(() => import('../../components/CodeEditor'));
const performanceFields = [
  { name: 'worker_processes', label: '工作进程', hint: 'auto 或 1–1024' },
  {
    name: 'worker_connections',
    label: '单进程连接上限',
    hint: '1–1048576，同时受系统文件描述符上限约束',
  },
  {
    name: 'keepalive_timeout',
    label: '长连接超时',
    hint: '如 65 或 65s；0 关闭长连接，可填写第二个响应头超时值',
  },
  {
    name: 'client_max_body_size',
    label: '请求体大小上限',
    hint: '如 1m、100m；不带单位为字节，0 不限制',
  },
  { name: 'gzip', label: 'Gzip 压缩', hint: '对应 http 块的 gzip 指令' },
  { name: 'sendfile', label: 'Sendfile', hint: '对应 http 块的 sendfile 指令' },
] as const;

export default function NginxSettings({
  app,
  busy,
  control,
  uninstall,
  onClose,
}: {
  app: Application;
  busy: boolean;
  control: (action: 'start' | 'stop' | 'restart' | 'reload') => void;
  uninstall: () => void;
  onClose: () => void;
}) {
  const { message, modal } = App.useApp();
  const screens = Grid.useBreakpoint();
  const client = useQueryClient();
  const [tab, setTab] = useState('service');
  const [draft, setDraft] = useState<string>();
  const [performanceDirty, setPerformanceDirty] = useState(false);
  const [performanceReload, setPerformanceReload] = useState(0);
  const [guardOpen, setGuardOpen] = useState(false);
  const config = useQuery({
    queryKey: ['app-settings', 'nginx'],
    queryFn: () => applicationSettings('nginx'),
    enabled: tab === 'config' && app.manageable,
  });
  const guard = useQuery({
    queryKey: ['nginx-guard'],
    queryFn: nginxGuard,
    enabled: tab === 'service' && app.manageable,
  });
  const load = useQuery({
    queryKey: ['nginx-load'],
    queryFn: nginxLoad,
    enabled: tab === 'load' && app.manageable,
    refetchInterval: tab === 'load' ? 5000 : false,
  });
  const performance = useQuery({
    queryKey: ['nginx-performance'],
    queryFn: nginxPerformance,
    enabled: tab === 'performance' && app.manageable,
  });
  const logs = useQuery({
    queryKey: ['nginx-error-log'],
    queryFn: nginxErrorLog,
    enabled: tab === 'logs' && app.manageable,
  });
  const dirty = draft !== undefined && draft !== config.data?.content;
  const saveConfig = useMutation({
    mutationFn: () =>
      saveApplicationSettings('nginx', draft!, config.data!.revision),
    onSuccess: async () => {
      setDraft(undefined);
      await client.invalidateQueries({ queryKey: ['app-settings', 'nginx'] });
      await client.invalidateQueries({ queryKey: ['nginx-performance'] });
      message.success('配置已保存并检查通过，重载配置后生效');
    },
  });
  const saveGuard = useMutation({
    mutationFn: (values: { enabled: boolean; restartSeconds: number }) =>
      saveNginxGuard(values.enabled, values.restartSeconds),
    onSuccess: async () => {
      await guard.refetch();
      setGuardOpen(false);
      message.success('进程守护设置已保存');
    },
  });
  const savePerformance = useMutation({
    mutationFn: (values: NginxPerformanceValues) =>
      saveNginxPerformance(values, performance.data!.revision),
    onSuccess: async () => {
      setPerformanceDirty(false);
      await performance.refetch();
      await client.invalidateQueries({ queryKey: ['app-settings', 'nginx'] });
      message.success('性能配置已保存并检查通过，重载配置后生效');
    },
  });
  const saving = saveConfig.isPending || savePerformance.isPending;
  const close = () => {
    if (saving || saveGuard.isPending) return;
    if (dirty || performanceDirty)
      modal.confirm({ title: '放弃尚未保存的配置？', onOk: onClose });
    else onClose();
  };
  const stack = { width: '100%' };
  const logStyle = {
    height: 'max(360px, calc(72dvh - 175px))',
    overflow: 'auto',
    whiteSpace: 'pre-wrap' as const,
    overflowWrap: 'anywhere' as const,
    background: TERMINAL_COLORS.background,
    color: TERMINAL_COLORS.foreground,
    padding: SPACING.md,
    borderRadius: 14,
    fontFamily: MONO_FONT,
  };
  return (
    <Modal
      open
      centered
      title="Nginx · 设置"
      width={1040}
      styles={appSettingsModalStyles}
      onCancel={close}
      footer={null}
      destroyOnHidden
      maskClosable={false}
    >
      <Tabs
        className="app-settings-tabs"
        tabPosition={screens.md ? 'left' : 'top'}
        activeKey={tab}
        onChange={setTab}
        items={[
          {
            key: 'service',
            label: '服务',
            children: (
              <div className="app-settings-stack">
                <div className="app-settings-section">
                  <div className="app-settings-section-title">基本信息</div>
                  <Descriptions
                    size="small"
                    column={{ xs: 1, sm: 2 }}
                    items={[
                      {
                        key: 'state',
                        label: '服务状态',
                        children: (
                          <Badge
                            status={app.running ? 'success' : 'default'}
                            text={app.running ? '运行中' : '已停止'}
                          />
                        ),
                      },
                      {
                        key: 'version',
                        label: '当前版本',
                        children: app.version ?? '未识别',
                      },
                      {
                        key: 'source',
                        label: '安装来源',
                        children: app.pkgManager,
                      },
                    ]}
                  />
                </div>
                {app.reason && (
                  <Alert type="info" showIcon message={app.reason} />
                )}
                <div className="app-settings-section">
                  <div className="app-settings-section-title">服务控制</div>
                  <Space wrap>
                    {!app.running && (
                      <Button
                        type="primary"
                        disabled={busy || !app.manageable}
                        onClick={() => control('start')}
                      >
                        启动
                      </Button>
                    )}
                    {!!app.running && (
                      <Button
                        disabled={busy || !app.manageable}
                        onClick={() => control('stop')}
                      >
                        停止
                      </Button>
                    )}
                    <Button
                      disabled={busy || !app.manageable}
                      onClick={() => control('restart')}
                    >
                      重启
                    </Button>
                    <Button
                      disabled={busy || !app.manageable || !app.running}
                      onClick={() => control('reload')}
                    >
                      重载配置
                    </Button>
                  </Space>
                  <Typography.Text type="secondary">
                    重载会先检查配置，检查通过后平滑加载新配置。
                  </Typography.Text>
                </div>
                <div className="app-settings-section">
                  {guard.error ? (
                    <Alert
                      type="error"
                      message={guard.error.message}
                      action={
                        <Button
                          size="small"
                          onClick={() => void guard.refetch()}
                        >
                          重试
                        </Button>
                      }
                    />
                  ) : (
                    <div className="app-settings-row">
                      <div className="app-settings-row-info">
                        <span className="app-settings-row-title">进程守护</span>
                        <Typography.Text type="secondary">
                          通过 systemd
                          在异常退出后自动拉起服务，手动停止不会触发守护。
                          {guard.data &&
                            ` 当前策略：${guard.data.policy}，间隔 ${guard.data.delay}。`}
                        </Typography.Text>
                      </div>
                      <Space size={SPACING.sm}>
                        <Switch
                          checked={guard.data?.enabled ?? false}
                          loading={guard.isFetching || saveGuard.isPending}
                          disabled={busy || !app.manageable || !guard.data}
                          checkedChildren="开启"
                          unCheckedChildren="关闭"
                          onChange={(enabled) =>
                            saveGuard.mutate({
                              enabled,
                              restartSeconds: guard.data!.restartSeconds,
                            })
                          }
                        />
                        <Popover
                          title="进程守护设置"
                          trigger="click"
                          open={guardOpen}
                          onOpenChange={setGuardOpen}
                          content={
                            <ApplicationSettingsForm
                              key={`${guard.data?.enabled}-${guard.data?.delay}`}
                              initialValues={{
                                restartSeconds: guard.data?.restartSeconds ?? 5,
                              }}
                              onFinish={(values: { restartSeconds: number }) =>
                                saveGuard.mutate({
                                  enabled: guard.data!.enabled,
                                  restartSeconds: values.restartSeconds,
                                })
                              }
                            >
                              <Form.Item
                                name="restartSeconds"
                                label="重启间隔"
                                rules={[
                                  { required: true, message: '请输入重启间隔' },
                                ]}
                              >
                                <InputNumber
                                  min={1}
                                  max={300}
                                  precision={0}
                                  addonAfter="秒"
                                />
                              </Form.Item>
                              <Form.Item className="application-settings-actions">
                                <Button
                                  type="primary"
                                  htmlType="submit"
                                  loading={saveGuard.isPending}
                                  disabled={busy}
                                >
                                  保存设置
                                </Button>
                              </Form.Item>
                            </ApplicationSettingsForm>
                          }
                        >
                          <Button
                            size="small"
                            disabled={
                              busy || !guard.data || saveGuard.isPending
                            }
                          >
                            间隔设置
                          </Button>
                        </Popover>
                      </Space>
                    </div>
                  )}
                </div>
                <div className="app-settings-section app-settings-danger">
                  <div className="app-settings-row">
                    <div className="app-settings-row-info">
                      <span className="app-settings-row-title">卸载 Nginx</span>
                      <Typography.Text type="secondary">
                        卸载后将停止并移除 Nginx 服务，已有站点将无法访问。
                      </Typography.Text>
                    </div>
                    <Button
                      danger
                      disabled={busy || !app.uninstallable}
                      onClick={uninstall}
                    >
                      卸载 Nginx
                    </Button>
                  </div>
                </div>
              </div>
            ),
          },
          {
            key: 'config',
            label: '配置文件',
            disabled: !app.manageable,
            children: (
              <Space direction="vertical" size={SPACING.md} style={stack}>
                {config.error && (
                  <Alert
                    type="error"
                    message={config.error.message}
                    action={
                      <Button onClick={() => void config.refetch()}>
                        重试
                      </Button>
                    }
                  />
                )}
                {config.isPending ? (
                  <Skeleton active />
                ) : (
                  config.data && (
                    <>
                      <div className="app-settings-toolbar">
                        <Typography.Text code copyable>
                          {config.data.path}
                        </Typography.Text>
                        <Space wrap>
                          <Button
                            disabled={saving || busy}
                            onClick={() => {
                              const refresh = async () => {
                                setDraft(undefined);
                                await config.refetch();
                              };
                              if (dirty)
                                modal.confirm({
                                  title: '丢弃未保存的修改并重新加载？',
                                  onOk: refresh,
                                });
                              else void refresh();
                            }}
                          >
                            重新加载
                          </Button>
                          <Button
                            disabled={busy || saving || !app.running || dirty}
                            onClick={() => control('reload')}
                          >
                            重载配置
                          </Button>
                          <Button
                            type="primary"
                            loading={saveConfig.isPending}
                            disabled={
                              !dirty || busy || savePerformance.isPending
                            }
                            onClick={() => saveConfig.mutate()}
                          >
                            保存配置
                          </Button>
                        </Space>
                      </div>
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
                        保存时保留备份并执行 nginx
                        -t，检查失败会恢复原配置。保存后需手动重载生效。
                      </Typography.Text>
                    </>
                  )
                )}
              </Space>
            ),
          },
          {
            key: 'load',
            label: '负载状态',
            disabled: !app.manageable,
            children: (
              <Space direction="vertical" size={SPACING.lg} style={stack}>
                <Button
                  loading={load.isFetching}
                  onClick={() => void load.refetch()}
                >
                  刷新状态
                </Button>
                {load.error && (
                  <Alert type="error" message={load.error.message} />
                )}
                {load.isPending ? (
                  <Skeleton active />
                ) : (
                  load.data && (
                    <>
                      <Descriptions
                        column={screens.md ? 2 : 1}
                        items={[
                          {
                            key: 'pid',
                            label: '主进程 PID',
                            children: load.data.pid || '未运行',
                          },
                          {
                            key: 'processes',
                            label: '进程数量',
                            children: load.data.processes,
                          },
                          {
                            key: 'memory',
                            label: '驻留内存',
                            children:
                              load.data.residentBytes === null
                                ? '未知'
                                : `${(load.data.residentBytes / 1048576).toFixed(1)} MiB`,
                          },
                          {
                            key: 'cpu',
                            label: '累计 CPU 时间',
                            children:
                              load.data.cpuSeconds === null
                                ? '未知'
                                : `${load.data.cpuSeconds.toFixed(2)} 秒`,
                          },
                          {
                            key: 'connections',
                            label: 'TCP 已建立连接',
                            children: load.data.connections ?? '未知',
                          },
                          {
                            key: 'listeners',
                            label: 'TCP 监听地址',
                            children: load.data.listeners ?? '未知',
                          },
                          {
                            key: 'uptime',
                            label: '主进程运行时间',
                            children:
                              load.data.uptimeSeconds === null
                                ? '未知'
                                : `${Math.floor(load.data.uptimeSeconds / 60)} 分钟`,
                          },
                        ]}
                      />
                      <Typography.Paragraph type="secondary">
                        {load.data.notice} 每 5 秒刷新。
                      </Typography.Paragraph>
                    </>
                  )
                )}
              </Space>
            ),
          },
          {
            key: 'performance',
            label: '性能调整',
            disabled: !app.manageable,
            children: (
              <Space direction="vertical" size={SPACING.lg} style={stack}>
                <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
                  调整主配置中的常用指令。留空会移除该项，使用 Nginx
                  默认值或其他 include 配置；站点覆盖值请在配置文件中调整。
                </Typography.Paragraph>
                {performance.error && (
                  <Alert
                    type="error"
                    message={performance.error.message}
                    action={
                      <Button onClick={() => void performance.refetch()}>
                        重试
                      </Button>
                    }
                  />
                )}
                {performance.isPending ? (
                  <Skeleton active />
                ) : (
                  performance.data && (
                    <ApplicationSettingsForm
                      key={`${performance.data.revision}-${performanceReload}`}

                      initialValues={Object.fromEntries(
                        performanceFields.map(({ name }) => [
                          name,
                          performance.data.values[name] ?? '',
                        ]),
                      )}
                      onValuesChange={() => setPerformanceDirty(true)}
                      onFinish={(values: NginxPerformanceValues) =>
                        savePerformance.mutate(values)
                      }
                    >
                      {performanceFields.map(({ name, label, hint }) => (
                        <Form.Item
                          key={name}
                          name={name}
                          label={label}
                          extra={`${name} · ${hint}`}
                        >
                          {name === 'gzip' || name === 'sendfile' ? (
                            <Select
                              options={[
                                { value: '', label: '未设置' },
                                { value: 'on', label: '开启' },
                                { value: 'off', label: '关闭' },
                              ]}
                            />
                          ) : (
                            <Input
                              placeholder="未在主配置中设置"
                              maxLength={32}
                            />
                          )}
                        </Form.Item>
                      ))}
                      <Form.Item className="application-settings-actions">
                        <Space wrap>
                          <Button
                            type="primary"
                            htmlType="submit"
                            loading={savePerformance.isPending}
                            disabled={
                              busy || saveConfig.isPending || !performanceDirty
                            }
                          >
                            保存调整
                          </Button>
                          <Button
                            disabled={
                              busy || saving || performanceDirty || !app.running
                            }
                            onClick={() => control('reload')}
                          >
                            重载配置
                          </Button>
                          <Button
                            disabled={busy || saving}
                            onClick={() => {
                              const refresh = async () => {
                                await performance.refetch();
                                setPerformanceDirty(false);
                                setPerformanceReload((value) => value + 1);
                              };
                              if (performanceDirty)
                                modal.confirm({
                                  title: '丢弃未保存的性能调整？',
                                  onOk: refresh,
                                });
                              else void refresh();
                            }}
                          >
                            重新加载
                          </Button>
                        </Space>
                      </Form.Item>
                    </ApplicationSettingsForm>
                  )
                )}
                <Typography.Text type="secondary">
                  保存前备份并检查配置，检查通过后再手动重载生效。
                </Typography.Text>
              </Space>
            ),
          },
          {
            key: 'logs',
            label: '错误日志',
            disabled: !app.manageable,
            children: (
              <Space direction="vertical" size={SPACING.md} style={stack}>
                <Button
                  loading={logs.isFetching}
                  onClick={() => void logs.refetch()}
                >
                  刷新日志
                </Button>
                {logs.error && (
                  <Alert type="error" message={logs.error.message} />
                )}
                {logs.isPending ? (
                  <Skeleton active />
                ) : (
                  logs.data && (
                    <>
                      <Typography.Paragraph
                        type="secondary"
                        style={{ margin: 0 }}
                      >
                        {logs.data.notice}
                      </Typography.Paragraph>
                      {logs.data.files.length === 0 && (
                        <Empty
                          image={Empty.PRESENTED_IMAGE_SIMPLE}
                          description="没有可读取的主错误日志"
                        />
                      )}
                      {logs.data.files.map((file, index) => (
                        <Space
                          key={`${file.path}-${index}`}
                          direction="vertical"
                          size={SPACING.sm}
                          style={stack}
                        >
                          <Typography.Text code>{file.path}</Typography.Text>
                          <Typography.Text type="secondary">
                            {file.notice}
                          </Typography.Text>
                          <LogText style={logStyle}>
                            {file.content || '暂无错误日志'}
                          </LogText>
                        </Space>
                      ))}
                    </>
                  )
                )}
              </Space>
            ),
          },
        ]}
      />
    </Modal>
  );
}
