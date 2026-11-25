import LogText from '../../components/LogText';
import { useState } from 'react';
import {
  Alert,
  Button,
  DatePicker,
  Flex,
  Radio,
  Skeleton,
  Space,
  Typography,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import type { Dayjs } from 'dayjs';
import { request } from '../../api/client';
import { MONO_FONT, TERMINAL_COLORS } from '../../styles/tokens';
import { saveBlob } from '../../utils/download';

export default function ContainerLogs({
  id,
  name,
}: {
  id: string;
  name: string;
}) {
  const [period, setPeriod] = useState('3');
  const [range, setRange] = useState<[Dayjs | null, Dayjs | null] | null>(null);
  const [clearedAt, setClearedAt] = useState(0);
  const logs = useQuery({
    queryKey: [
      'docker-logs',
      id,
      period,
      range?.[0]?.unix(),
      range?.[1]?.unix(),
      clearedAt,
    ],
    enabled: period !== 'custom' || Boolean(range?.[0] && range[1]),
    queryFn: ({ signal }) => {
      const from =
        period === 'custom'
          ? range?.[0]?.unix()
          : period === 'all'
            ? 0
            : Math.floor(Date.now() / 1000) - Number(period) * 86400;
      return request<{ content: string }>(`/api/docker/containers/${id}/logs`, {
        params: {
          since: Math.max(from ?? 0, clearedAt),
          until: period === 'custom' ? range?.[1]?.unix() : undefined,
        },
        signal,
        silentError: true,
      });
    },
  });
  const choose = (value: string) => {
    setPeriod(value);
    setClearedAt(0);
  };
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Flex justify="space-between" gap={12} wrap>
        <Space wrap>
          <Button
            loading={logs.isFetching}
            disabled={period === 'custom' && !range}
            onClick={() => void logs.refetch()}
          >
            刷新日志
          </Button>
          <Button
            disabled={!logs.data?.content}
            onClick={() =>
              saveBlob(
                logs.data?.content ?? '',
                `${name.replace(/^\//, '')}.log`,
              )
            }
          >
            下载日志
          </Button>
          <Button
            disabled={!logs.data?.content}
            onClick={() => {
              setPeriod('all');
              setClearedAt(Math.ceil(Date.now() / 1000));
            }}
          >
            清空显示
          </Button>
        </Space>
        <Radio.Group
          value={period}
          onChange={(event) => choose(event.target.value)}
          optionType="button"
          options={[
            { label: '最近 3 天', value: '3' },
            { label: '最近 7 天', value: '7' },
            { label: '全部', value: 'all' },
            { label: '自定义', value: 'custom' },
          ]}
        />
      </Flex>
      {period === 'custom' && (
        <DatePicker.RangePicker showTime value={range} onChange={setRange} />
      )}
      {logs.error && <Alert type="error" message={logs.error.message} />}
      {logs.isLoading ? (
        <Skeleton active />
      ) : (
        <LogText
          className="docker-container-log"
          style={{
            fontFamily: MONO_FONT,
            background: TERMINAL_COLORS.background,
            color: TERMINAL_COLORS.foreground,
          }}
        >
          {logs.data?.content || '暂无日志'}
        </LogText>
      )}
      <Typography.Text type="secondary">
        每次显示所选范围内最近 500 行，最多 2 MiB。
      </Typography.Text>
    </Space>
  );
}
