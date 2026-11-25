import LogText from '../../components/LogText';
import { lazy, Suspense, useState } from 'react';
import {
  Alert,
  App,
  Badge,
  Button,
  Descriptions,
  Grid,
  Modal,
  Segmented,
  Skeleton,
  Space,
  Switch,
  Tabs,
  Typography,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import {
  applicationGuard,
  saveApplicationGuard,
  applicationLogs,
  applicationSettings,
  saveApplicationSettings,
  type Application,
} from '../../api/software';
import { Fail2banIPList, Fail2banRules } from '../security/Fail2banProtection';
import NginxSettings from './NginxSettings';
import PHPSettings from './PHPSettings';
import PhpMyAdminSettings from './PhpMyAdminSettings';
import { SPACING } from '../../styles/tokens';
import {
  ClamAVHistory,
  ClamAVQuarantine,
  ClamAVSettings,
} from './ClamAVVisual';
import { FTPServiceSettings, FTPUsers } from './FTPVisual';
import RsyncVisual from './RsyncVisual';
import RsyncWorkspace from './RsyncWorkspace';
import './apps.css';
const CodeEditor = lazy(() => import('../../components/CodeEditor'));

const toolSettings: Record<
  string,
  { label: string; help: string; extra?: { label: string; value: string }[] }
> = {
  ftp: {
    label: '配置文件',
    help: '修改配置后重启 Pure-FTPd 应用。监听地址使用 IP,端口 格式，被动端口范围用空格分隔起止端口。',
    extra: [
      { label: '监听地址', value: 'bind' },
      { label: '被动端口', value: 'passive' },
    ],
  },
  supervisor: {
    label: '守护进程',
    help: '在 [program:名称] 中配置宿主机进程的运行用户、执行命令、工作目录和重启策略。',
  },
  clamav: {
    label: '扫描设置',
    help: '配置宿主机的扫描线程、文件大小上限和病毒库更新。网站目录扫描只读取并报告文件。',
    extra: [{ label: '病毒库更新', value: 'updates' }],
  },
  rsync: {
    label: '同步模块',
    help: '用宿主机的绝对路径设置同步模块，配置读写权限、访问来源和认证账号。',
    extra: [{ label: '认证账号', value: 'credentials' }],
  },
};
export const appSettingsModalStyles = {
  body: {
    height: 'clamp(480px, 72dvh, 760px)',
    display: 'flex',
    flexDirection: 'column' as const,
    overflow: 'hidden',
    paddingTop: 10,
  },
};
export type ApplicationSettingsProps = {
  app: Application;
  busy: boolean;
  control: (action: 'start' | 'stop' | 'restart' | 'reload') => void;
  uninstall: () => void;
  onClose: () => void;
  switchVersion: () => void;
  onAuthentication?: () => void;
};
export default function ApplicationSettings(props: ApplicationSettingsProps) {
  if (props.app.id === 'phpmyadmin') return <PhpMyAdminSettings {...props} />;
  if (props.app.id === 'nginx') return <NginxSettings {...props} />;
  if (props.app.id.startsWith('php-')) return <PHPSettings {...props} />;
  return <GenericSettings {...props} />;
}
function GenericSettings({
  app,
  busy,
  control,
  uninstall,
  onClose,
  onAuthentication,
}: ApplicationSettingsProps) {
  const { message, modal } = App.useApp();
  const fail2ban = app.id === 'fail2ban';
  const ftp = app.id === 'ftp';
  const clamav = app.id === 'clamav';
  const rsync = app.id === 'rsync';
  const canReload = ['openresty', 'fail2ban', 'supervisor'].includes(app.id);
  const { md } = Grid.useBreakpoint();
  const [tab, setTab] = useState(fail2ban ? 'rules' : 'service');
  const [configKind, setConfigKind] = useState('');
  const [draft, setDraft] = useState<string>();
  const [saving, setSaving] = useState(false);
  const [guardSaving, setGuardSaving] = useState(false);
  const tool = toolSettings[app.id];
  const showGuard =
    app.pkgManager !== 'Docker' && app.manageable && app.running !== null;
  const guard = useQuery({
    queryKey: ['app-guard', app.id],
    queryFn: () => applicationGuard(app.id),
    enabled: tab === 'service' && showGuard,
  });
  const config = useQuery({
    queryKey: ['app-settings', app.id, configKind],
    queryFn: () => applicationSettings(app.id, configKind),
    enabled: tab === 'config' && app.manageable,
  });
  const logs = useQuery({
    queryKey: ['app-logs', app.id],
    queryFn: () => applicationLogs(app.id),
    enabled: tab === 'logs' && app.manageable,
  });
  const dirty = draft !== undefined && draft !== config.data?.content;
  const close = () => {
    if (dirty) modal.confirm({ title: '放弃尚未保存的配置？', onOk: onClose });
    else onClose();
  };
  async function save() {
    if (!config.data) return;
    setSaving(true);
    try {
      await saveApplicationSettings(
        app.id,
        draft ?? config.data.content,
        config.data.revision,
        configKind,
      );
      setDraft(undefined);
      await config.refetch();
      message.success(canReload ? '配置已保存，重载配置后生效' : '配置已保存');
    } finally {
      setSaving(false);
    }
  }
  return (
    <Modal
      open
      centered
      title={`${app.name} · 设置`}
      width={1040}
      styles={appSettingsModalStyles}
      onCancel={close}
      footer={null}
      destroyOnHidden
      maskClosable={false}
    >
      <Tabs
        className="app-settings-tabs"
        tabPosition={md ? 'left' : 'top'}
        activeKey={tab}
        onChange={setTab}
        items={[
          ...(fail2ban
            ? [
                {
                  key: 'rules',
                  label: '服务保护',
                  children: <Fail2banRules />,
                },
                {
                  key: 'blacklist',
                  label: 'IP 黑名单',
                  children: <Fail2banIPList list="blacklist" />,
                },
                {
                  key: 'whitelist',
                  label: 'IP 白名单',
                  children: <Fail2banIPList list="whitelist" />,
                },
              ]
            : []),
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
                        children:
                          app.running === null ? (
                            <Badge status="default" text="无独立服务" />
                          ) : (
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
                      ...(app.installDir
                        ? [
                            {
                              key: 'installDir',
                              label: '安装目录',
                              span: 2,
                              children: (
                                <Typography.Text code copyable>
                                  {app.installDir}
                                </Typography.Text>
                              ),
                            },
                          ]
                        : []),
                      ...(app.dataDir
                        ? [
                            {
                              key: 'dataDir',
                              label: '数据目录',
                              span: 2,
                              children: (
                                <Typography.Text code copyable>
                                  {app.dataDir}
                                </Typography.Text>
                              ),
                            },
                          ]
                        : []),
                    ]}
                  />
                </div>
                {app.reason && (
                  <Alert type="info" showIcon message={app.reason} />
                )}
                {((app.manageable && app.running !== null) ||
                  onAuthentication) && (
                  <div className="app-settings-section">
                    <div className="app-settings-section-title">服务控制</div>
                    <Space wrap>
                      {app.manageable && app.running !== null && (
                        <>
                          <Button
                            type={!app.running ? 'primary' : 'default'}
                            disabled={busy || app.running}
                            onClick={() => control('start')}
                          >
                            启动
                          </Button>
                          <Button
                            disabled={busy || !app.running}
                            onClick={() => control('stop')}
                          >
                            停止
                          </Button>
                          <Button
                            disabled={busy}
                            onClick={() => control('restart')}
                          >
                            重启
                          </Button>
                          {canReload && (
                            <Button
                              disabled={busy || !app.running}
                              onClick={() => control('reload')}
                            >
                              重载配置
                            </Button>
                          )}
                        </>
                      )}
                      {onAuthentication && (
                        <Button onClick={onAuthentication}>本机认证</Button>
                      )}
                    </Space>
                  </div>
                )}
                {app.manageable && ftp && (
                  <div className="app-settings-section">
                    <FTPServiceSettings />
                  </div>
                )}
                {app.manageable && clamav && (
                  <div className="app-settings-section">
                    <ClamAVSettings tab="service" />
                  </div>
                )}
                {showGuard && (
                  <div className="app-settings-section">
                    <div className="app-settings-row">
                      <div className="app-settings-row-info">
                        <span className="app-settings-row-title">进程守护</span>
                        <Typography.Text type="secondary">
                          服务异常退出后由 systemd 自动拉起，手动停止保持停止。
                        </Typography.Text>
                      </div>
                      <Switch
                        aria-label="进程守护"
                        checked={guard.data?.enabled ?? false}
                        loading={guard.isPending || guardSaving}
                        disabled={busy || !guard.data}
                        onChange={async (enabled) => {
                          setGuardSaving(true);
                          try {
                            await saveApplicationGuard(
                              app.id,
                              enabled,
                              guard.data!.restartSeconds,
                            );
                            await guard.refetch();
                            message.success('进程守护设置已保存');
                          } finally {
                            setGuardSaving(false);
                          }
                        }}
                      />
                    </div>
                    {guard.error && (
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
                    )}
                  </div>
                )}
                <div className="app-settings-section app-settings-danger">
                  <div className="app-settings-row">
                    <div className="app-settings-row-info">
                      <span className="app-settings-row-title">卸载应用</span>
                      <Typography.Text type="secondary">
                        卸载后将停止并移除 {app.name} 服务与程序文件。
                      </Typography.Text>
                    </div>
                    <Button
                      danger
                      disabled={busy || !app.uninstallable}
                      onClick={uninstall}
                    >
                      卸载应用
                    </Button>
                  </div>
                </div>
              </div>
            ),
          },
          ...(app.manageable && ftp
            ? [{ key: 'ftp-users', label: '用户管理', children: <FTPUsers /> }]
            : []),
          ...(app.manageable && rsync
            ? [
                {
                  key: 'rsync-send',
                  label: '发送任务',
                  children: <RsyncWorkspace section="send" busy={busy} />,
                },
                {
                  key: 'rsync-receive',
                  label: '接收任务',
                  children: (
                    <RsyncVisual
                      running={!!app.running}
                      busy={busy}
                      onToggle={() => control(app.running ? 'stop' : 'start')}
                    />
                  ),
                },
                {
                  key: 'rsync-rules',
                  label: '排除规则',
                  children: <RsyncWorkspace section="rules" busy={busy} />,
                },
              ]
            : []),
          ...(app.manageable && clamav
            ? [
                {
                  key: 'clamav-scan',
                  label: '病毒检测',
                  children: <ClamAVSettings tab="scan" />,
                },
                {
                  key: 'clamav-quarantine',
                  label: '隔离目录',
                  children: <ClamAVQuarantine />,
                },
                {
                  key: 'clamav-history',
                  label: '检测记录',
                  children: <ClamAVHistory />,
                },
              ]
            : []),
          ...(!fail2ban
            ? [
                {
                  key: 'config',
                  label: tool?.label ?? '配置修改',
                  disabled: !app.manageable || app.id === 'mysql-client',
                  children: (
                    <Space
                      direction="vertical"
                      size={SPACING.md}
                      style={{ width: '100%' }}
                    >
                      {tool && (
                        <Typography.Text type="secondary">
                          {tool.help}
                        </Typography.Text>
                      )}
                      {tool?.extra &&
                        (app.id !== 'ftp' || app.pkgManager === 'apt-get') && (
                          <Segmented
                            value={configKind}
                            disabled={saving}
                            options={[
                              { label: tool.label, value: '' },
                              ...tool.extra,
                            ]}
                            onChange={(kind) => {
                              const change = () => {
                                setDraft(undefined);
                                setConfigKind(kind);
                              };
                              if (dirty)
                                modal.confirm({
                                  title: '放弃尚未保存的配置？',
                                  onOk: change,
                                });
                              else change();
                            }}
                          />
                        )}
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
                              <Space>
                                <Button
                                  disabled={saving}
                                  onClick={() => {
                                    if (dirty)
                                      modal.confirm({
                                        title: '丢弃未保存的修改并重新加载？',
                                        onOk: async () => {
                                          setDraft(undefined);
                                          await config.refetch();
                                        },
                                      });
                                    else void config.refetch();
                                  }}
                                >
                                  重新加载
                                </Button>
                                <Button
                                  type="primary"
                                  loading={saving}
                                  disabled={
                                    (!dirty && !config.data.missing) || busy
                                  }
                                  onClick={() => void save()}
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
                              {canReload
                                ? '保存后点击服务页的「重载配置」。'
                                : '保存后按应用要求使配置生效。'}
                            </Typography.Text>
                            {app.id === 'mysql' &&
                              app.pkgManager === '官方软件源' && (
                                <Alert
                                  type="info"
                                  message={`首次安装的 MySQL root 密码保存在服务器私有文件 ${app.initialPasswordPath ?? '/opt/ezpanel/apps/mysql/default/.state/root-password'}，请通过终端查看。修改密码后该初始文件不会同步更新。`}
                                />
                              )}
                          </>
                        )
                      )}
                    </Space>
                  ),
                },
              ]
            : []),
          {
            key: 'logs',
            label: '运行日志',
            disabled: !app.manageable || app.running === null,
            children: (
              <Space
                direction="vertical"
                size={SPACING.md}
                style={{ width: '100%' }}
              >
                <div className="app-settings-toolbar">
                  <Typography.Text type="secondary">
                    展示最近的服务运行输出日志
                  </Typography.Text>
                  <Button
                    loading={logs.isFetching}
                    onClick={() => void logs.refetch()}
                  >
                    刷新日志
                  </Button>
                </div>
                {logs.error && (
                  <Alert type="error" message={logs.error.message} />
                )}
                <LogText
                  style={{
                    height: 'max(360px, calc(72dvh - 125px))',
                    overflow: 'auto',
                    whiteSpace: 'pre-wrap',
                  }}
                >
                  {logs.data?.content || '暂无服务日志'}
                </LogText>
              </Space>
            ),
          },
        ]}
      />
    </Modal>
  );
}
