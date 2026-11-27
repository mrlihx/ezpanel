import LogText from '../../components/LogText';
import { Alert, Spin, Typography } from 'antd';
import type { TextLog } from '../../api/oplog';
import { formatSize } from '../../utils/format';

export const moduleNames: Record<string, string> = {
  website: '网站管理',
  database: '数据库管理',
  file: '文件管理',
  software: '应用管理',
  app_config: '应用配置',
  settings: '面板设置',
  process: '进程管理',
  cron: '计划任务',
  waf: 'WAF',
  docker: 'Docker',
  plugins: '应用插件',
  security: '安全管理',
  certificate: '证书管理',
  alerts: '告警管理',
  terminal: '终端',
  toolbox: '系统工具',
  oplog: '日志管理',
  auth: '认证',
};
export function LogOutput({
  data,
  loading,
  error,
  keyword = '',
  fill = false,
}: {
  data?: TextLog | null;
  loading: boolean;
  error?: Error | null;
  keyword?: string;
  fill?: boolean;
}) {
  const content = keyword
    ? data?.content
        .split('\n')
        .filter((line) => line.includes(keyword))
        .join('\n')
    : data?.content;
  return (
    <>
      {error && <Alert showIcon type="error" message={error.message} />}
      <div className="logs-source">
        <Typography.Text type="secondary">
          {data?.path || '—'}
          {data && ` · ${formatSize(data.size)}`}
        </Typography.Text>
      </div>
      {data?.truncated && (
        <Alert
          type="info"
          showIcon
          message="当前仅显示最近一段日志，较早内容未载入。"
        />
      )}
      <Spin
        spinning={loading}
        className={fill ? 'logs-output-fill' : undefined}
      >
        <LogText className="logs-output" tabIndex={0} aria-label="日志内容">
          {content || (loading ? '正在读取日志…' : '暂无日志')}
        </LogText>
      </Spin>
    </>
  );
}
