import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  App,
  Badge,
  Button,
  Card,
  Descriptions,
  Divider,
  Flex,
  Modal,
  Popover,
  Space,
  Typography,
} from 'antd';
import { Monitor } from 'lucide-react';
import { panelStatus, restartSystem, type Overview } from '../../api/sysinfo';
import PanelUpdateModal from '../../components/PanelUpdateModal';
import { SPACING } from '../../styles/tokens';

export default function SystemBar({
  data,
  release,
}: {
  data: Overview;
  release: string;
}) {
  const { message, modal } = App.useApp();
  const [open, setOpen] = useState(false);
  const [updateOpen, setUpdateOpen] = useState(false);
  const statusQuery = useQuery({
    queryKey: ['panel-status'],
    queryFn: ({ signal }) => panelStatus(signal),
    staleTime: 5 * 60 * 1000,
  });
  const restart = useMutation({
    mutationFn: restartSystem,
    onSuccess: (_, target) => {
      setOpen(false);
      message.success(
        target === 'panel' ? '已提交面板重启请求' : '已提交服务器重启请求',
      );
    },
  });
  const confirmRestart = (target: 'panel' | 'server') => {
    const title = target === 'server' ? '重启服务器' : '重启面板';
    modal.confirm({
      title: `确认${title}？`,
      content:
        target === 'server'
          ? '网站、数据库及其他服务将在服务器重启期间暂时中断。'
          : '面板连接将在服务重启期间暂时中断。',
      okText: title,
      cancelText: '取消',
      okButtonProps: { danger: true },
      autoFocusButton: 'cancel',
      onOk: () => restart.mutateAsync(target),
    });
  };
  const system = [
    data.platformName || data.platform || data.os || '系统',
    data.platformVersion?.split('.')[0],
  ]
    .filter(Boolean)
    .join(' ');
  const uptime =
    data.uptime == null
      ? '--'
      : data.uptime < 3600
        ? `${Math.floor(data.uptime / 60)} 分钟`
        : `${Math.floor(data.uptime / 86400)} 天 ${Math.floor((data.uptime % 86400) / 3600)} 小时`;

  return (
    <>
      <Card size="small" className="apple-system-bar">
        <Flex justify="space-between" align="center" gap={SPACING.md} wrap>
          <Popover
            placement="bottomLeft"
            trigger={['hover', 'click']}
            content={
              <Descriptions
                column={1}
                size="small"
                style={{ maxWidth: 'min(360px, 75vw)' }}
                styles={{ content: { overflowWrap: 'anywhere' } }}
                items={[
                  { key: 'release', label: '系统', children: release },
                  { key: 'uptime', label: '持续运行', children: uptime },
                ]}
              />
            }
          >
            <Button
              type="text"
              className="apple-system-tag"
              icon={<Monitor size={15} strokeWidth={1.9} />}
              aria-label={`${system}，查看系统详情`}
            >
              {system}
            </Button>
          </Popover>
          <Space wrap size={SPACING.sm}>
            <Typography.Text strong style={{ fontSize: 13.5 }}>
              ezpanel {data.panelVersion || '--'}
            </Typography.Text>
            <Divider type="vertical" style={{ marginInline: 2 }} />
            <Badge dot={Boolean(statusQuery.data?.hasUpdate)} offset={[-2, 2]}>
              <Button size="small" onClick={() => setUpdateOpen(true)}>
                更新
              </Button>
            </Badge>
            <Button size="small" onClick={() => setOpen(true)}>
              重启
            </Button>
          </Space>
        </Flex>
      </Card>
      <Modal
        title="重启服务器或者面板"
        width={420}
        open={open}
        onCancel={() => {
          if (!restart.isPending) setOpen(false);
        }}
        footer={null}
        maskClosable={false}
        destroyOnHidden
      >
        <Flex gap={SPACING.md} style={{ paddingBlock: SPACING.xl }}>
          <Button
            size="large"
            style={{ flex: 1 }}
            onClick={() => confirmRestart('server')}
          >
            重启服务器
          </Button>
          <Button
            size="large"
            style={{ flex: 1 }}
            onClick={() => confirmRestart('panel')}
          >
            重启面板
          </Button>
        </Flex>
      </Modal>
      {updateOpen && (
        <PanelUpdateModal
          open={updateOpen}
          onClose={() => setUpdateOpen(false)}
        />
      )}
    </>
  );
}
