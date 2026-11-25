import { App, Button, Space, Tooltip, Typography } from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { terminateProcess, type Process } from '../../api/sysinfo';
import { SPACING } from '../../styles/tokens';

export default function useProcessTermination(onTerminated?: () => void) {
  const { modal, message } = App.useApp();
  const queryClient = useQueryClient();
  const terminate = useMutation({
    mutationFn: (process: Process) =>
      terminateProcess(process.pid, process.started),
    onSuccess: () => {
      message.success('已发送 SIGTERM 终止请求，正在刷新进程列表');
      onTerminated?.();
      void queryClient.invalidateQueries({ queryKey: ['processes'] });
      void queryClient.invalidateQueries({ queryKey: ['process-detail'] });
    },
  });
  const confirmTerminate = (process: Process) => {
    modal.confirm({
      title: `终止进程 ${process.name || process.pid}？`,
      content: (
        <Space direction="vertical" size={SPACING.sm}>
          <Typography.Text>
            PID {process.pid} · 用户 {process.user || '--'}
          </Typography.Text>
          <Typography.Text>
            将发送
            SIGTERM，请确认相关服务可以中断。请求发送后进程可能仍在清理资源，是否退出以刷新后的列表为准。
          </Typography.Text>
        </Space>
      ),
      okText: '发送终止请求',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => terminate.mutateAsync(process),
    });
  };
  const terminateButton = (process: Process) => {
    const reason = process.protected
      ? process.protectedReason || '系统或面板关键进程，禁止终止'
      : process.started <= 0
        ? '无法确认进程启动时间，禁止终止'
        : '';
    return (
      <Tooltip title={reason}>
        <span>
          <Button
            type="link"
            size="small"
            danger
            disabled={Boolean(reason) || terminate.isPending}
            onClick={() => confirmTerminate(process)}
          >
            终止
          </Button>
        </span>
      </Tooltip>
    );
  };
  return terminateButton;
}
