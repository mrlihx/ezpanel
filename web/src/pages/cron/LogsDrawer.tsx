import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { useLogScroll } from '../../hooks/useLogScroll';
import {
  Alert,
  App,
  Button,
  DatePicker,
  Empty,
  Flex,
  Grid,
  Modal,
  Popover,
  Segmented,
  Skeleton,
  Space,
  Switch,
  Typography,
} from 'antd';
import { ChevronDown, Maximize2, Minimize2, RotateCw } from 'lucide-react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { request } from '../../api/client';
import { openEventStream } from '../../api/eventStream';
import {
  LAYOUT,
  MONO_FONT,
  SPACING,
  TERMINAL_COLORS,
} from '../../styles/tokens';
import { formatSize } from '../../utils/format';

type LogView = {
  name: string;
  path: string;
  size: number;
  log: string;
  truncated: boolean;
  localPath: string;
  running: boolean;
};

export default function LogsDrawer({
  id,
  onClose,
  onRun,
}: {
  id: string;
  onClose: () => void;
  onRun: () => Promise<void>;
}) {
  const { modal } = App.useApp();
  const [period, setPeriod] = useState('3');
  const [dates, setDates] = useState<[string, string]>();
  const client = useQueryClient();
  const [auto, setAuto] = useState(true);
  const [streamError, setStreamError] = useState('');
  const [reconnect, setReconnect] = useState(0);
  const [starting, setStarting] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const { md } = Grid.useBreakpoint();
  const endpoint = '/api/crons/' + id;
  const rangeReady = period !== 'custom' || !!dates;
  const params =
    period === 'custom'
      ? new URLSearchParams({ from: dates?.[0] ?? '', to: dates?.[1] ?? '' })
      : new URLSearchParams({ days: period });
  const filters = params.toString();
  const query = useQuery({
    queryKey: ['cron-log-view', id, period, dates],
    queryFn: ({ signal }) =>
      request<LogView>(endpoint + '/log-view', {
        params:
          period === 'custom'
            ? { from: dates?.[0], to: dates?.[1] }
            : { days: period },
        signal,
        silentError: true,
      }),
    enabled: rangeReady && !auto,
  });
  useEffect(() => {
    if (!auto || !rangeReady) return;
    const key = ['cron-log-view', id, period, dates];
    return openEventStream(
      `${endpoint}/log-view/stream?${filters}`,
      {
        snapshot: (data) => {
          setStreamError('');
          client.setQueryData(key, data as LogView);
        },
        append: (data) => {
          setStreamError('');
          const next = data as LogView;
          client.setQueryData<LogView>(key, (previous) => ({
            ...next,
            log: (previous?.log ?? '') + next.log,
          }));
        },
      },
      setStreamError,
    );
  }, [
    auto,
    rangeReady,
    client,
    id,
    period,
    dates,
    endpoint,
    filters,
    reconnect,
  ]);
  const data = query.data;
  const {
    ref: output,
    onScroll,
    reset,
  } = useLogScroll<HTMLPreElement>(data?.log);
  return (
    <Modal
      open
      title={data ? `查看「${data.name}」日志` : '任务执行记录'}
      width={Math.max(LAYOUT.logDrawerWidth, 1080)}
      onCancel={onClose}
      footer={null}
    >
      <Flex vertical gap={SPACING.md}>
        {data && (
          <Flex gap={SPACING.md} wrap>
            <Typography.Text
              type="secondary"
              style={{ overflowWrap: 'anywhere' }}
            >
              日志路径 {data.path}
            </Typography.Text>
            <Typography.Text type="secondary">
              大小 {formatSize(data.size)}
            </Typography.Text>
          </Flex>
        )}
        <Flex gap={SPACING.md} wrap justify="space-between">
          <Space wrap>
            <Space.Compact>
              <Button
                icon={<RotateCw size={15} strokeWidth={1.9} />}
                disabled={period === 'custom' && !dates}
                loading={query.isFetching && !auto}
                onClick={() => {
                  if (auto) setReconnect((value) => value + 1);
                  else void query.refetch();
                }}
              >
                刷新日志
              </Button>
              <Popover
                trigger="click"
                content={
                  <Space>
                    <span>实时跟随</span>
                    <Switch
                      aria-label="实时跟随日志"
                      checked={auto}
                      onChange={setAuto}
                    />
                  </Space>
                }
              >
                <Button
                  icon={<ChevronDown size={15} strokeWidth={2} />}
                  aria-label="刷新设置"
                />
              </Popover>
            </Space.Compact>
            <Button
              danger
              onClick={() =>
                modal.confirm({
                  title: '清空此计划的已完成日志？',
                  content: '正在执行的日志会保留。',
                  onOk: async () => {
                    await request(endpoint + '/logs', { method: 'DELETE' });
                    if (rangeReady && !auto) await query.refetch();
                  },
                })
              }
            >
              清空日志
            </Button>
            <Button
              disabled={data?.running}
              loading={starting}
              onClick={async () => {
                setStarting(true);
                try {
                  await onRun();
                  setPeriod('3');
                  reset();
                  setAuto(true);
                } finally {
                  setStarting(false);
                }
              }}
            >
              执行任务
            </Button>
          </Space>
          <Segmented
            value={period}
            onChange={(value) => {
              reset();
              setPeriod(value);
            }}
            options={[
              { value: '3', label: '近3天' },
              { value: '7', label: '近7天' },
              { value: '30', label: '近30天' },
              { value: '0', label: '全部' },
              { value: 'custom', label: '自定义' },
            ]}
          />
        </Flex>
        {period === 'custom' && (
          <DatePicker.RangePicker
            format="YYYY-MM-DD"
            onChange={(_, [from, to]) => {
              reset();
              setDates(from && to ? [from, to] : undefined);
            }}
            style={{ alignSelf: 'flex-end', maxWidth: '100%' }}
          />
        )}
        {auto && streamError && rangeReady && (
          <Alert type="warning" message={streamError} showIcon />
        )}
        {!auto && query.error && (
          <Alert type="error" message={query.error.message} showIcon />
        )}
        {data?.truncated && (
          <Alert
            type="info"
            message="已显示所选时段最近的日志，可缩小日期范围查看更早内容。"
            showIcon
          />
        )}
        {period === 'custom' && !dates ? (
          <Empty
            description="请选择日期范围"
            image={Empty.PRESENTED_IMAGE_SIMPLE}
          />
        ) : query.isPending && !(auto && streamError) ? (
          <Skeleton active />
        ) : (
          (() => {
            const logNode = (
              <div
                className={[
                  'panel-log-box-host',
                  fullscreen && 'panel-log-box-host--fullscreen',
                ]
                  .filter(Boolean)
                  .join(' ')}
              >
                {(!md || fullscreen) && (
                  <button
                    type="button"
                    className="panel-log-fullscreen-btn"
                    aria-label={fullscreen ? '退出日志全屏' : '全屏查看日志'}
                    onClick={() => setFullscreen((v) => !v)}
                  >
                    {fullscreen ? (
                      <Minimize2 size={14} strokeWidth={2} />
                    ) : (
                      <Maximize2 size={14} strokeWidth={2} />
                    )}
                    <span>{fullscreen ? '退出全屏' : '全屏'}</span>
                  </button>
                )}
                <pre
                  ref={output}
                  aria-label="任务日志"
                  tabIndex={0}
                  className="panel-scroll-box"
                  onScroll={onScroll}
                  style={{
                    margin: 0,
                    background: TERMINAL_COLORS.background,
                    color: TERMINAL_COLORS.foreground,
                    fontFamily: MONO_FONT,
                    padding: SPACING.md,
                    paddingTop: fullscreen ? 52 : SPACING.md,
                    borderRadius: fullscreen ? 0 : 14,
                    height: fullscreen ? '100%' : 'min(56vh, 580px)',
                    flex: fullscreen ? 1 : undefined,
                    minHeight: fullscreen ? 0 : undefined,
                    overflow: 'auto',
                    overscrollBehavior: 'contain',
                    WebkitOverflowScrolling: 'touch',
                    touchAction: 'pan-y',
                    whiteSpace: 'pre-wrap',
                    overflowWrap: 'anywhere',
                  }}
                >
                  {data?.log || '所选时段暂无日志'}
                </pre>
              </div>
            );
            return fullscreen && typeof document !== 'undefined'
              ? createPortal(logNode, document.body)
              : logNode;
          })()
        )}
        {data?.localPath && (
          <Link
            to={'/files?path=' + encodeURIComponent(data.localPath)}
            onClick={onClose}
          >
            打开本地备份目录
          </Link>
        )}
      </Flex>
    </Modal>
  );
}
