import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Flex,
  Input,
  Select,
  Skeleton,
  Space,
} from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  composeProjectLogStreamURL,
  composeProjectLogs,
  downloadComposeProjectLogs,
  type ComposeLogView,
} from '../../../api/composeProjects';
import { openEventStream } from '../../../api/eventStream';
import LogText from '../../../components/LogText';
import '../container-manager.css';

const displayLimit = 1 << 20;

export default function ComposeLogsTab({
  name,
  services,
  containerRevision,
  active,
}: {
  name: string;
  services: string[];
  containerRevision: string;
  active: boolean;
}) {
  const { message } = App.useApp();
  const client = useQueryClient();
  const [service, setService] = useState<string>();
  const [tail, setTail] = useState(100);
  const [since, setSince] = useState<string>();
  const [timestamps, setTimestamps] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [live, setLive] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [streamRevision, setStreamRevision] = useState(0);
  const [streamStatus, setStreamStatus] = useState<{
    key: string;
    message?: string;
    reconnecting?: boolean;
  }>();
  const filters = { service, tail, since, timestamps, keyword };
  const serviceOptions = services.map((value) => ({ value, label: value }));
  if (service && !services.includes(service)) {
    serviceOptions.push({ value: service, label: `${service}（暂无容器）` });
  }
  const queryKey = useMemo(
    () => [
      'compose-project-logs',
      name,
      service,
      tail,
      since,
      timestamps,
      keyword,
    ],
    [name, service, tail, since, timestamps, keyword],
  );
  const logs = useQuery({
    queryKey,
    queryFn: ({ signal }) => composeProjectLogs(name, filters, signal),
    enabled: active && !live,
    retry: false,
  });
  const streamURL = composeProjectLogStreamURL(name, filters);
  const connectionKey = `${streamURL}:${containerRevision}:${streamRevision}`;
  const currentStream =
    active && live && streamStatus?.key === connectionKey
      ? streamStatus
      : undefined;
  const connecting = active && live && !currentStream;

  useEffect(() => {
    if (!active || !live) return;
    return openEventStream(
      streamURL,
      {
        snapshot(data) {
          client.setQueryData<ComposeLogView>(queryKey, data as ComposeLogView);
          setStreamStatus({ key: connectionKey });
        },
        append(data) {
          const { text, truncated, interrupted } = data as Pick<
            ComposeLogView,
            'text' | 'truncated' | 'interrupted'
          >;
          client.setQueryData<ComposeLogView>(queryKey, (previous) => {
            if (!previous) return previous;
            const merged =
              previous.text && text
                ? `${previous.text}\n${text}`
                : previous.text + text;
            const overflow = merged.length - displayLimit;
            const newline = overflow > 0 ? merged.indexOf('\n', overflow) : -1;
            return {
              ...previous,
              text:
                overflow > 0
                  ? merged.slice(newline >= 0 ? newline + 1 : overflow)
                  : merged,
              truncated: previous.truncated || truncated || overflow > 0,
              interrupted,
            };
          });
        },
      },
      (message, reconnecting) =>
        setStreamStatus({ key: connectionKey, message, reconnecting }),
    );
  }, [active, client, connectionKey, live, queryKey, streamURL]);

  async function download() {
    setDownloading(true);
    try {
      await downloadComposeProjectLogs(name, filters);
    } catch (error) {
      void message.error(
        error instanceof Error ? error.message : '日志下载失败',
      );
    } finally {
      setDownloading(false);
    }
  }

  return (
    <Flex vertical gap={12}>
      <Space wrap>
        <Select
          aria-label="服务"
          style={{ minWidth: 140 }}
          placeholder="全部服务"
          allowClear
          value={service}
          options={serviceOptions}
          onChange={setService}
        />
        <Select
          aria-label="日志行数"
          value={tail}
          options={[100, 500, 1000].map((value) => ({
            value,
            label: `${value} 行`,
          }))}
          onChange={setTail}
        />
        <Select
          aria-label="日志时间范围"
          value={since}
          allowClear
          placeholder="全部时间"
          style={{ minWidth: 120 }}
          options={[
            { value: '10m', label: '最近 10 分钟' },
            { value: '1h', label: '最近 1 小时' },
            { value: '24h', label: '最近 24 小时' },
          ]}
          onChange={setSince}
        />
        <Input.Search
          aria-label="搜索日志关键字"
          placeholder="过滤日志关键字"
          allowClear
          maxLength={256}
          onSearch={setKeyword}
        />
        <Checkbox
          checked={timestamps}
          onChange={(event) => setTimestamps(event.target.checked)}
        >
          时间戳
        </Checkbox>
        <Checkbox
          checked={live}
          onChange={(event) => setLive(event.target.checked)}
        >
          实时跟踪
        </Checkbox>
      </Space>
      <Space wrap>
        <Button
          loading={logs.isFetching || connecting}
          onClick={() => {
            if (live) setStreamRevision((revision) => revision + 1);
            else void logs.refetch();
          }}
        >
          刷新日志
        </Button>
        <Button
          loading={downloading}
          disabled={!logs.data?.text && !logs.data?.truncated}
          onClick={() => void download()}
        >
          下载日志
        </Button>
      </Space>
      {!live && logs.error && (
        <Alert type="error" message={logs.error.message} showIcon />
      )}
      {currentStream?.message && (
        <Alert
          type={currentStream.reconnecting ? 'warning' : 'error'}
          message={currentStream.message}
          showIcon
        />
      )}
      {logs.data?.truncated && (
        <Alert
          type="warning"
          message="预览达到大小上限，可下载更多日志"
          showIcon
        />
      )}
      {!!logs.data?.unavailable && (
        <Alert
          type="warning"
          message={`有 ${logs.data.unavailable} 个容器的日志无法读取`}
          showIcon
        />
      )}
      {live && !!logs.data?.interrupted && (
        <Alert
          type="warning"
          message={`有 ${logs.data.interrupted} 个容器的实时日志连接中断`}
          showIcon
        />
      )}
      {logs.isLoading || (connecting && !logs.data) ? (
        <Skeleton active />
      ) : (
        <LogText key={streamURL} className="docker-container-log">
          {logs.data?.text || '暂无日志'}
        </LogText>
      )}
    </Flex>
  );
}
