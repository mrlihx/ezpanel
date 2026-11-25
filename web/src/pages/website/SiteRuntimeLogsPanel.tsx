import { Alert, Button, Space, Typography } from 'antd';
import WebsiteLogContent from './WebsiteLogContent';

import type { SiteSettingsState } from './SiteSettingsContent';
const errorText = (e: unknown) => (e instanceof Error ? e.message : '请求失败');

export default function SiteRuntimeLogsPanel({
  projectLogs,
  tab,
}: Pick<SiteSettingsState, 'projectLogs' | 'tab'>) {
  return tab === 'runtime-logs' ? (
    <Space direction="vertical" style={{ width: '100%' }}>
      <Space>
        <Typography.Text>
          {projectLogs.data?.running ? '运行中' : '已停止'}
        </Typography.Text>
        <Button
          loading={projectLogs.isFetching}
          onClick={() => void projectLogs.refetch()}
        >
          刷新
        </Button>
      </Space>
      {projectLogs.data?.failedAttempt && (
        <Typography.Text type="warning">最近一次启动失败的日志</Typography.Text>
      )}
      {projectLogs.error ? (
        <Alert type="error" message={errorText(projectLogs.error)} />
      ) : (
        <WebsiteLogContent
          content={projectLogs.data?.content}
          loading={projectLogs.isFetching}
        >
          {!projectLogs.isPending && '暂无日志'}
        </WebsiteLogContent>
      )}
    </Space>
  ) : null;
}
