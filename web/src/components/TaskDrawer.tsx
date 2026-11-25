import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  Alert,
  Button,
  Drawer,
  Flex,
  Grid,
  Tag,
  Typography,
  List,
  Popconfirm,
} from 'antd';
import { Maximize2, Minimize2 } from 'lucide-react';
import { useMutation } from '@tanstack/react-query';
import { cancelTask, subscribeTask, type Task } from '../api/task';
import { LAYOUT, MONO_FONT, SPACING, TERMINAL_COLORS } from '../styles/tokens';
import { useLogScroll } from '../hooks/useLogScroll';
import { formatTime } from '../utils/format';
const states = {
  running: { label: '执行中', color: 'processing' },
  success: { label: '成功', color: 'success' },
  failed: { label: '失败', color: 'error' },
  interrupted: { label: '已中断', color: 'warning' },
};
type Props = {
  taskId: string | null;
  title: string;
  open: boolean;
  onClose: () => void;
  onSuccess?: () => void;
  onSettled?: (task: Task) => void;
  onRetry?: () => void;
};
export default function TaskDrawer(props: Props) {
  const [notified] = useState(() => new Set<string>());
  return (
    <Drawer
      title={props.title}
      width={LAYOUT.drawerWidth}
      open={props.open}
      onClose={props.onClose}
    >
      {props.open && props.taskId && (
        <TaskContent
          key={props.taskId}
          {...props}
          taskId={props.taskId}
          notified={notified}
        />
      )}
    </Drawer>
  );
}
export function TaskContent({
  taskId,
  onSuccess,
  onSettled,
  onRetry,
  notified,
  logsOnly = false,
  fit = false,
}: Pick<Props, 'onSuccess' | 'onSettled' | 'onRetry'> & {
  taskId: string;
  notified?: Set<string>;
  logsOnly?: boolean;
  fit?: boolean;
}) {
  const [task, setTask] = useState<Task>();
  const [logs, setLogs] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const [fullscreen, setFullscreen] = useState(false);
  const cancel = useMutation({ mutationFn: () => cancelTask(taskId) });
  const { md } = Grid.useBreakpoint();
  const { ref: logRef, onScroll, reset } = useLogScroll<HTMLPreElement>(logs);
  const callbacks = useRef({ onSuccess, onSettled });
  useEffect(() => {
    callbacks.current = { onSuccess, onSettled };
  }, [onSuccess, onSettled]);
  useEffect(
    () =>
      subscribeTask(
        taskId,
        (next, finished) => {
          setError('');
          setTask(next);
          if (next.logs.length)
            setLogs((previous) => [...previous, ...next.logs]);
          if (finished && !notified?.has(taskId)) {
            notified?.add(taskId);
            if (next.status === 'success') callbacks.current.onSuccess?.();
            callbacks.current.onSettled?.(next);
          }
        },
        setError,
      ),
    [taskId, attempt, notified],
  );
  const loadError = error && (
    <Alert
      type="error"
      message={error}
      action={
        <Button
          size="small"
          onClick={() => {
            reset();
            setTask(undefined);
            setLogs([]);
            setError('');
            setAttempt(attempt + 1);
          }}
        >
          重新连接
        </Button>
      }
    />
  );
  useEffect(() => {
    if (!fullscreen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setFullscreen(false);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [fullscreen]);
  const outputNode = (
    <div
      className={[
        'panel-log-box-host',
        fullscreen && 'panel-log-box-host--fullscreen',
      ]
        .filter(Boolean)
        .join(' ')}
      style={
        !fullscreen && fit
          ? { flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }
          : undefined
      }
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
        ref={logRef}
        tabIndex={0}
        aria-label="任务日志"
        className="panel-scroll-box"
        onScroll={onScroll}
        style={{
          fontFamily: MONO_FONT,
          background: TERMINAL_COLORS.background,
          color: TERMINAL_COLORS.foreground,
          padding: SPACING.lg,
          paddingTop: fullscreen ? 52 : SPACING.lg,
          borderRadius: fullscreen ? 0 : 14,
          height: fullscreen
            ? '100%'
            : fit
              ? undefined
              : logsOnly
                ? LAYOUT.messageBoxHeight
                : LAYOUT.taskLogHeight,
          flex: fullscreen || fit ? 1 : undefined,
          minHeight: fullscreen || fit ? 0 : undefined,
          overflow: 'auto',
          overscrollBehavior: 'contain',
          WebkitOverflowScrolling: 'touch',
          touchAction: 'pan-y',
          whiteSpace: 'pre-wrap',
          overflowWrap: 'anywhere',
          margin: 0,
        }}
      >
        {logs.join('\n') ||
          (logsOnly && !task && !error ? '正在读取日志…' : '暂无日志')}
      </pre>
    </div>
  );
  const output =
    fullscreen && typeof document !== 'undefined'
      ? createPortal(outputNode, document.body)
      : outputNode;
  if (logsOnly) {
    return (
      <Flex
        vertical
        gap={SPACING.md}
        style={fit ? { flex: 1, minHeight: 0 } : undefined}
      >
        {loadError}
        {task?.errorMsg && (
          <Alert
            type={task.status === 'interrupted' ? 'warning' : 'error'}
            showIcon
            message={task.errorMsg}
          />
        )}
        {output}
      </Flex>
    );
  }
  const status = task ? states[task.status] : undefined;
  const cancellable =
    task?.status === 'running' &&
    task.module === 'docker' &&
    task.action.startsWith('compose-');
  return (
    <Flex vertical gap={SPACING.lg}>
      <Flex align="center" justify="space-between" wrap="wrap" gap={8}>
        <Typography.Text type="secondary">
          关闭窗口后任务继续执行
        </Typography.Text>
        {cancellable && (
          <Popconfirm
            title="取消当前任务？"
            okText="取消任务"
            okButtonProps={{ danger: true }}
            cancelText="继续执行"
            onConfirm={() => cancel.mutate()}
          >
            <Button
              size="small"
              danger
              loading={cancel.isPending}
              disabled={cancel.isSuccess}
            >
              {cancel.isSuccess ? '正在取消' : '取消任务'}
            </Button>
          </Popconfirm>
        )}
      </Flex>
      {status && <Tag color={status.color}>{status.label}</Tag>}
      {task && (
        <Typography.Text type="secondary">
          开始时间：{formatTime(task.startedAt)}（服务器时区）
          {task.exitCode !== null && ` · 退出码 ${task.exitCode}`}
        </Typography.Text>
      )}
      {loadError}
      {task?.errorMsg && (
        <Alert
          type={task.status === 'interrupted' ? 'warning' : 'error'}
          message={task.errorMsg}
          description={
            task.status === 'interrupted'
              ? '任务已中断，目标可能已部分改变，请检查结果后再重试。'
              : undefined
          }
        />
      )}{' '}
      {task?.logTruncated && (
        <Alert type="warning" message="日志已达到上限，后续输出不再保存" />
      )}
      {output}
      {task?.batch && (
        <>
          <Typography.Text strong>
            操作结果：成功 {task.batch.successCount} 项，失败{' '}
            {task.batch.failedCount} 项
          </Typography.Text>
          <List
            dataSource={task.batch.results.filter((item) => item.code !== 0)}
            renderItem={(item) => (
              <List.Item>
                <Alert
                  type="error"
                  message={item.target}
                  description={item.msg}
                />
              </List.Item>
            )}
          />
        </>
      )}
      {task?.metadata && (
        <>
          <Typography.Text strong>操作结果</Typography.Text>
          <List
            dataSource={task.metadata}
            renderItem={(item) => (
              <List.Item>
                <Typography.Text>{item}</Typography.Text>
              </List.Item>
            )}
          />
        </>
      )}
      {task &&
        (task.status === 'failed' || task.status === 'interrupted') &&
        onRetry && <Button onClick={onRetry}>重新确认并重试</Button>}
    </Flex>
  );
}
