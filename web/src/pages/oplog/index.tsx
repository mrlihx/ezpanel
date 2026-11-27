import type { CSSProperties } from 'react';
import {
  Navigate,
  useLocation,
  useNavigate,
  useParams,
} from 'react-router-dom';
import { Card, Skeleton, Tabs, theme } from 'antd';
import SectionTabs from '../../components/SectionTabs';
import AuditLogs from './AuditLogs';
import {
  RuntimeLogs,
  CronLogs,
  ApplicationLogs,
  SSHLogs,
  logApps,
} from './Sources';
import WebsiteLogs from './WebsiteLogs';
import { useRememberedTab } from '../../hooks/useRememberedTab';
import RouteNotFound from '../../router/RouteNotFound';
import './logs.css';
import { MONO_FONT, TERMINAL_COLORS } from '../../styles/tokens';

const panelTabs = [
  { key: 'operation', label: '操作日志' },
  { key: 'login', label: '登录日志' },
  { key: 'runtime', label: '运行日志' },
  { key: 'cron', label: '计划任务日志' },
];
const logSources = [
  'access',
  'container',
  'error',
  'file',
  'journal',
  'slow',
  'transfer',
  'frps',
  'frpc',
];
const appDestinations = Object.keys(logApps).map((key) => `apps/${key}`);
const logDestinations = [
  ...panelTabs.map(({ key }) => `panel/${key}`),
  'website/logs',
  'website/operations',
  'website/analysis',
  'ssh',
  ...appDestinations,
];
const destinations = [
  ...logDestinations,
  ...appDestinations.flatMap((path) =>
    logSources.map((source) => `${path}/${source}`),
  ),
];
const isSupervisorLog = (path: string) =>
  /^apps\/supervisor\/(?:main|[0-9a-f]{16})$/.test(path);

export default function OpLogPage() {
  const { token } = theme.useToken();
  const style = {
    '--logs-background': TERMINAL_COLORS.background,
    '--logs-foreground': TERMINAL_COLORS.foreground,
    '--logs-font': MONO_FONT,
    '--logs-border': token.colorBorder,
    '--logs-link': token.colorLink,
  } as CSSProperties;
  const params = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const requested = [params.section, params.tab, params.source]
    .filter(Boolean)
    .join('/');
  const { tab: saved, loading } = useRememberedTab(
    'oplogs',
    requested,
    destinations,
    isSupervisorLog,
  );
  const valid = destinations.includes(requested) || isSupervisorLog(requested);
  if (!valid) {
    if (
      params.tab ||
      (params.section &&
        !logDestinations.some((path) => path.startsWith(params.section + '/')))
    )
      return <RouteNotFound back="/oplogs" />;
    if (loading) return <Skeleton active />;
    const preferred = params.section
      ? logDestinations.find(
          (path) =>
            path === params.section || path.startsWith(params.section + '/'),
        )
      : saved;
    return (
      <Navigate
        to={`/oplogs/${preferred ?? saved}${location.search}`}
        replace
      />
    );
  }
  const [section, tab, source] = requested.split('/');
  const go = (path: string, replace = false) => {
    const [nextSection, nextTab] = path.split('/');
    const query = new URLSearchParams(location.search);
    if (nextSection !== 'website') query.delete('site');
    if (nextSection !== 'apps' || section !== 'apps' || nextTab !== tab) {
      query.delete('version');
      query.delete('instance');
    }
    void navigate(
      { pathname: `/oplogs/${path}`, search: query.toString() },
      { replace },
    );
  };
  return (
    <div
      className={
        section === 'website' && tab === 'logs'
          ? 'logs-page logs-page-website'
          : section === 'panel' && tab === 'runtime'
            ? 'logs-page logs-page-runtime'
            : 'logs-page'
      }
      style={style}
    >
      <SectionTabs
        style={{ marginBottom: 16 }}
        activeKey={section}
        onChange={(key) => {
          const target = logDestinations.find(
            (path) => path === key || path.startsWith(key + '/'),
          );
          if (target && key !== section) go(target);
        }}
        items={[
          { key: 'panel', label: '面板日志' },
          { key: 'website', label: '网站日志' },
          { key: 'ssh', label: 'SSH登录日志' },
          { key: 'apps', label: '应用日志' },
        ]}
      />
      <Card className="logs-card">
        {section === 'panel' && (
          <>
            <Tabs
              type="card"
              activeKey={tab}
              onChange={(key) => go(`panel/${key}`)}
              items={panelTabs}
            />
            {tab === 'operation' && <AuditLogs key="operation" />}
            {tab === 'login' && <AuditLogs key="login" login />}
            {tab === 'runtime' && <RuntimeLogs />}
            {tab === 'cron' && <CronLogs />}
          </>
        )}
        {section === 'website' && (
          <WebsiteLogs section={tab} onChange={(key) => go(`website/${key}`)} />
        )}
        {section === 'ssh' && <SSHLogs />}
        {section === 'apps' && (
          <ApplicationLogs
            app={tab}
            source={source}
            onChange={(key) => go(`apps/${key}`)}
            onSourceChange={(key, replace) => go(`apps/${tab}/${key}`, replace)}
          />
        )}
      </Card>
    </div>
  );
}
