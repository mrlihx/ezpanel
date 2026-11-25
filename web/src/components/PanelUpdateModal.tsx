import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Descriptions,
  Modal,
  Space,
  Spin,
  Tag,
  Typography,
} from 'antd';
import {
  applyPanelUpdate,
  checkPanelUpdate,
  dismissPanelUpdateError,
  panelStatus,
  type PanelStatus,
} from '../api/sysinfo';

export default function PanelUpdateModal({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const [checking, setChecking] = useState(false);
  const [updating, setUpdating] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [updateError, setUpdateError] = useState('');
  const pollRef = useRef<number | null>(null);

  const statusQuery = useQuery({
    queryKey: ['panel-status'],
    queryFn: ({ signal }) => panelStatus(signal),
    enabled: open,
    staleTime: 30000,
  });

  useEffect(() => {
    return () => {
      if (pollRef.current !== null) {
        window.clearInterval(pollRef.current);
        pollRef.current = null;
      }
    };
  }, []);

  const data = statusQuery.data;

  async function handleCheck() {
    if (checking || updating) return;
    setChecking(true);
    setUpdateError('');
    try {
      const next = await checkPanelUpdate();
      client.setQueryData<PanelStatus>(['panel-status'], next);
      if (next.hasUpdate) {
        void message.info(`发现新版本 v${next.latestVersion}`);
      } else {
        void message.success(`当前已是最新版本 v${next.panelVersion}`);
      }
    } finally {
      setChecking(false);
    }
  }

  function startPollAfterRestart(expectedVersion: string) {
    setRestarting(true);
    let attempts = 0;
    let sawOffline = false;
    pollRef.current = window.setInterval(async () => {
      attempts++;
      try {
        const res = await fetch('/api/sysinfo/panel-status', {
          credentials: 'same-origin',
          cache: 'no-store',
        });
        if (!res.ok) {
          sawOffline = true;
          return;
        }
        const payload = (await res.json()) as {
          code?: number;
          data?: PanelStatus;
        };
        if (payload.code !== 0 || !payload.data) return;
        const latest = payload.data;
        if (latest.lastUpdateError) {
          if (pollRef.current !== null) {
            window.clearInterval(pollRef.current);
            pollRef.current = null;
          }
          setRestarting(false);
          setUpdating(false);
          setUpdateError(latest.lastUpdateError);
          client.setQueryData<PanelStatus>(['panel-status'], latest);
          return;
        }
        if (
          latest.panelVersion === expectedVersion ||
          (sawOffline && attempts >= 3) ||
          attempts >= 20
        ) {
          if (pollRef.current !== null) {
            window.clearInterval(pollRef.current);
            pollRef.current = null;
          }
          setRestarting(false);
          setUpdating(false);
          void message.success(
            `面板已更新至 v${latest.panelVersion}，即将刷新页面`,
          );
          window.setTimeout(() => window.location.reload(), 800);
        }
      } catch {
        sawOffline = true;
        if (attempts >= 35) {
          if (pollRef.current !== null) {
            window.clearInterval(pollRef.current);
            pollRef.current = null;
          }
          setRestarting(false);
          setUpdating(false);
          setUpdateError(
            '等待面板重启超时，请手动刷新页面或检查 systemctl status ezpanel.service',
          );
        }
      }
    }, 2500);
  }

  function confirmApply() {
    if (!data?.latestTag) return;
    modal.confirm({
      title: `确认更新面板至 ${data.latestVersion} (${data.latestTag})？`,
      content:
        '更新过程会自动校验官方签名与 SHA-256 并备份旧程序；若新版本启动健康检查失败，将自动回退原版本，不影响网站与数据库运行。',
      okText: '立即更新',
      cancelText: '取消',
      onOk: async () => {
        setUpdating(true);
        setUpdateError('');
        try {
          const res = await applyPanelUpdate(data.latestTag);
          if (res.updating) {
            startPollAfterRestart(res.targetVersion);
          }
        } catch (err) {
          setUpdating(false);
          setUpdateError(
            err instanceof Error ? err.message : '面板更新失败，原版本未受影响',
          );
        }
      },
    });
  }

  const rollbackError = updateError || data?.lastUpdateError || '';

  return (
    <Modal
      open={open}
      title="检查面板更新"
      width={560}
      maskClosable={!updating && !restarting}
      closable={!updating && !restarting}
      keyboard={!updating && !restarting}
      onCancel={onClose}
      footer={
        <Space>
          <Button
            disabled={updating || restarting}
            loading={checking}
            onClick={() => void handleCheck()}
          >
            重新检查
          </Button>
          {data?.hasUpdate && (
            <Button
              type="primary"
              loading={updating || restarting}
              onClick={confirmApply}
            >
              立即在线更新
            </Button>
          )}
          <Button disabled={updating || restarting} onClick={onClose}>
            关闭
          </Button>
        </Space>
      }
      destroyOnHidden
    >
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        {rollbackError && (
          <Alert
            type="error"
            showIcon
            closable={!updating && !restarting}
            onClose={() => {
              setUpdateError('');
              void dismissPanelUpdateError();
              client.setQueryData<PanelStatus>(['panel-status'], (old) =>
                old ? { ...old, lastUpdateError: '' } : old,
              );
            }}
            message="上次更新未成功（已自动回退保护）"
            description={rollbackError}
          />
        )}
        {restarting ? (
          <div style={{ textAlign: 'center', padding: '28px 0' }}>
            <Spin size="large" />
            <Typography.Paragraph style={{ marginTop: 16, marginBottom: 4 }}>
              正在替换程序并重启面板服务，请勿关闭页面…
            </Typography.Paragraph>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              若新版本启动健康检查未通过，系统将自动回退至原版本并展示原因。
            </Typography.Text>
          </div>
        ) : (
          <>
            <Descriptions
              size="small"
              bordered
              column={1}
              items={[
                {
                  key: 'current',
                  label: '当前版本',
                  children: (
                    <Typography.Text code>
                      v{data?.panelVersion || '--'}
                    </Typography.Text>
                  ),
                },
                {
                  key: 'latest',
                  label: '最新版本',
                  children: data?.latestVersion ? (
                    <Space size={8} wrap>
                      <Typography.Text code>
                        v{data.latestVersion}
                      </Typography.Text>
                      <Typography.Text
                        type="secondary"
                        style={{ fontSize: 12 }}
                      >
                        ({data.latestTag})
                      </Typography.Text>
                      {data.hasUpdate ? (
                        <Tag color="red">可更新</Tag>
                      ) : (
                        <Tag color="success">已是最新</Tag>
                      )}
                    </Space>
                  ) : (
                    <Typography.Text type="secondary">
                      尚未获取到版本信息，请点击「重新检查」
                    </Typography.Text>
                  ),
                },
                ...(data?.releaseUrl
                  ? [
                      {
                        key: 'release',
                        label: '发布说明',
                        children: (
                          <a
                            href={data.releaseUrl}
                            target="_blank"
                            rel="noreferrer"
                          >
                            在 GitHub 查看 Release 详情
                          </a>
                        ),
                      },
                    ]
                  : []),
              ]}
            />
            {data?.releaseNotes && (
              <div
                style={{
                  maxHeight: 160,
                  overflow: 'auto',
                  padding: '10px 12px',
                  borderRadius: 8,
                  background: 'rgba(128, 128, 136, 0.06)',
                  fontSize: 12,
                  whiteSpace: 'pre-wrap',
                }}
              >
                {data.releaseNotes}
              </div>
            )}
          </>
        )}
      </Space>
    </Modal>
  );
}
