import { lazy, Suspense, useState } from 'react';
import { Button, Modal, Space, Spin, Tooltip, Typography } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { terminalSettings, saveTerminalSettings } from '../../api/settings';
import {
  CloseOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
} from '@ant-design/icons';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { useFloatingWindow } from '../../hooks/useFloatingWindow';

const Session = lazy(() => import('../terminal/session'));

export default function TerminalModal({
  cwd,
  onClose,
  initialInput,
  title,
}: {
  cwd: string;
  onClose: () => void;
  initialInput?: string;
  title?: string;
}) {
  const client = useQueryClient();
  const settings = useQuery({
    queryKey: ['terminal-settings'],
    queryFn: terminalSettings,
  });
  const fontSize = settings.data?.fontSize ?? 14;
  const fontSizeChange = useMutation({
    mutationFn: saveTerminalSettings,
    onSuccess: (data) => client.setQueryData(['terminal-settings'], data),
  });
  const [clearCount, setClearCount] = useState(0);
  const floating = useFloatingWindow({
    width: Math.min(
      LAYOUT.fileTerminalWidth,
      window.innerWidth - SPACING.lg * 2,
    ),
    height: Math.min(
      LAYOUT.fileTerminalWindowHeight,
      window.innerHeight - SPACING.lg * 2,
    ),
    minWidth: LAYOUT.fileTerminalMinWidth,
    minHeight: LAYOUT.fileTerminalMinHeight,
  });
  const { full, toggleFull } = floating;
  return (
    <Modal
      className="file-terminal-modal"
      title={
        <div
          {...floating.titleProps}
          style={{
            ...floating.titleProps.style,
            display: 'flex',
            alignItems: 'center',
            gap: SPACING.sm,
          }}
        >
          <Typography.Text
            ellipsis
            title={cwd}
            style={{ flex: 1, minWidth: 0 }}
          >
            {title ?? `终端 · ${cwd}`}
          </Typography.Text>
          <Space size="small" style={{ flexShrink: 0 }}>
            <Tooltip title={full ? '还原窗口' : '最大化'}>
              <Button
                type="text"
                size="small"
                aria-label={full ? '还原终端窗口' : '最大化终端'}
                icon={
                  full ? <FullscreenExitOutlined /> : <FullscreenOutlined />
                }
                onClick={toggleFull}
              />
            </Tooltip>
            <Button
              type="text"
              size="small"
              aria-label="关闭终端"
              icon={<CloseOutlined />}
              onClick={onClose}
            />
          </Space>
        </div>
      }
      open
      width={floating.width}
      style={floating.modalStyle}
      footer={null}
      closable={false}
      keyboard={false}
      maskClosable={false}
      onCancel={onClose}
      destroyOnHidden
      styles={{
        content: { height: '100%', display: 'flex', flexDirection: 'column' },
        body: {
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          minHeight: 0,
        },
        wrapper: { overflow: 'hidden' },
      }}
      modalRender={(node) => <div {...floating.frameProps}>{node}</div>}
    >
      <Space size={SPACING.sm} style={{ marginBottom: SPACING.sm }}>
        <Button
          size="small"
          disabled={
            !settings.data || fontSizeChange.isPending || fontSize <= 12
          }
          onClick={() => fontSizeChange.mutate(fontSize - 1)}
        >
          A−
        </Button>
        <Button
          size="small"
          disabled={
            !settings.data || fontSizeChange.isPending || fontSize >= 20
          }
          onClick={() => fontSizeChange.mutate(fontSize + 1)}
        >
          A＋
        </Button>
        <Button size="small" onClick={() => setClearCount(clearCount + 1)}>
          清屏
        </Button>
        <Typography.Text type="secondary">
          拖动标题栏移动 · 右下角调整大小
        </Typography.Text>
      </Space>
      <Suspense fallback={<Spin />}>
        <Session
          cwd={cwd}
          active
          fontSize={fontSize}
          clearCount={clearCount}
          initialInput={initialInput}
        />
      </Suspense>
    </Modal>
  );
}
