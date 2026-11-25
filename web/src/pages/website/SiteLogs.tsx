import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Empty,
  Flex,
  Select,
  Space,
  Switch,
  Typography,
} from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { openEventStream } from '../../api/eventStream';
import { clearSiteLog, siteLogs } from '../../api/website';
import { SPACING } from '../../styles/tokens';
import SiteLogLocation from './SiteLogLocation';
import WebsiteLogContent from './WebsiteLogContent';

export default function SiteLogs({
  id,
  directory,
}: {
  id: string;
  directory?: string;
}) {
  const [type, setType] = useState<'access' | 'error'>('access');
  const [lines, setLines] = useState(200);
  const [auto, setAuto] = useState(false);
  const [streamAttempt, setStreamAttempt] = useState(0);
  const [streamError, setStreamError] = useState<{
    key: string;
    message: string;
    reconnecting: boolean;
  }>();
  const [clearing, setClearing] = useState(false);
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const queryKey = useMemo(
    () => ['website-logs', id, type, lines],
    [id, type, lines],
  );
  const streamKey = `${id}:${type}:${lines}:${streamAttempt}`;
  const connectionError =
    auto && streamError?.key === streamKey ? streamError : undefined;
  const logs = useQuery({
    queryKey,
    queryFn: () => siteLogs(id, type, lines),
    enabled: !auto,
  });

  useEffect(() => {
    if (!auto) return;
    return openEventStream(
      `/api/websites/${encodeURIComponent(id)}/logs/stream?type=${type}&lines=${lines}`,
      {
        logs(data) {
          client.setQueryData(
            queryKey,
            data as Awaited<ReturnType<typeof siteLogs>>,
          );
          setStreamError(undefined);
        },
      },
      (message, reconnecting) =>
        setStreamError({ key: streamKey, message, reconnecting }),
    );
  }, [auto, client, id, lines, queryKey, streamKey, type]);

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <SiteLogLocation directory={directory} />
      <Flex gap={SPACING.sm} wrap align="center">
        <Select
          aria-label="日志类型"
          value={type}
          onChange={setType}
          options={[
            { value: 'access', label: '访问日志' },
            { value: 'error', label: '错误日志' },
          ]}
        />
        <Select
          aria-label="显示行数"
          value={lines}
          onChange={setLines}
          options={[200, 500, 1000].map((value) => ({
            value,
            label: `最近${value}行`,
          }))}
        />
        <Button loading={logs.isFetching} onClick={() => void logs.refetch()}>
          刷新
        </Button>
        <Space>
          <Switch
            aria-label="实时日志"
            checked={auto}
            onChange={(checked) => {
              setAuto(checked);
              if (checked) setStreamAttempt((attempt) => attempt + 1);
            }}
          />
          <Typography.Text>实时日志</Typography.Text>
        </Space>
        <Button
          href={`/api/websites/${encodeURIComponent(id)}/logs/download?type=${type}`}
        >
          下载日志
        </Button>
        <Button
          danger
          loading={clearing}
          onClick={() =>
            modal.confirm({
              title: `清空${type === 'access' ? '访问' : '错误'}日志？`,
              content: '当前日志内容将被清空，历史轮转文件保留。',
              okText: '清空',
              okButtonProps: { danger: true },
              onOk: async () => {
                setClearing(true);
                try {
                  await clearSiteLog(id, type);
                  void message.success('日志已清空');
                  await logs.refetch();
                } finally {
                  setClearing(false);
                }
              },
            })
          }
        >
          清空日志
        </Button>
      </Flex>
      {connectionError && (
        <Alert
          showIcon
          type={connectionError.reconnecting ? 'warning' : 'error'}
          message={connectionError.message}
          action={
            !connectionError.reconnecting && (
              <Button
                onClick={() => setStreamAttempt((attempt) => attempt + 1)}
              >
                重连
              </Button>
            )
          }
        />
      )}
      {!auto && logs.isError && (
        <Alert
          type="error"
          message={logs.error.message}
          action={<Button onClick={() => void logs.refetch()}>重试</Button>}
        />
      )}
      <WebsiteLogContent
        key={`${type}:${lines}`}
        content={logs.data?.content}
        loading={
          logs.isFetching || (auto && logs.isPending && !connectionError)
        }
        truncated={logs.data?.truncated}
        dark
      >
        {!logs.isPending && !logs.isError && (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description="暂无日志记录"
          />
        )}
      </WebsiteLogContent>
    </Space>
  );
}
