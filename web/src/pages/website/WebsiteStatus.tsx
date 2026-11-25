import { useState } from 'react';
import { App, Button, Divider, Popover, Space, Tag } from 'antd';
import { Pause, Play } from 'lucide-react';
import { restartRuntime, toggleWebsite, type Website } from '../../api/website';

const labels = { start: '启动', stop: '停止', restart: '重启' };
type Action = keyof typeof labels;

export default function WebsiteStatus({
  site,
  manageable,
  onSettings,
  onChanged,
}: {
  site: Website;
  manageable: boolean;
  onSettings: () => void;
  onChanged: () => void;
}) {
  const { message, modal } = App.useApp();
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  const managed = Boolean(site.runtimeVersion);
  const running = managed
    ? (site.runtimeRunning ?? site.effectiveRunning)
    : site.effectiveRunning;
  const needsSetup = managed && !site.runtimeConfigured;
  const disabled = busy || !manageable;
  const status = needsSetup
    ? '待配置'
    : running
      ? '运行中'
      : site.enabled
        ? '未运行'
        : '已停止';

  async function execute(action: Action) {
    setOpen(false);
    setBusy(true);
    try {
      if (
        action === 'restart' ||
        (action === 'start' && managed && site.enabled)
      ) {
        await restartRuntime(site.id);
      } else {
        await toggleWebsite(site.id, action === 'start');
      }
      void message.success(`已${labels[action]}${site.name}`);
    } finally {
      setBusy(false);
      onChanged();
    }
  }

  const button = (
    <Button
      type="text"
      size="small"
      className="website-status-button"
      disabled={disabled}
      loading={busy}
      aria-expanded={managed && !needsSetup ? open : undefined}
      onClick={
        needsSetup
          ? onSettings
          : !managed
            ? () => {
                const action = site.enabled ? 'stop' : 'start';
                modal.confirm({
                  title: `${labels[action]}「${site.name}」？`,
                  okText: labels[action],
                  okButtonProps: { danger: action === 'stop' },
                  onOk: () => execute(action),
                });
              }
            : undefined
      }
    >
      <Tag color={running ? 'success' : needsSetup ? 'warning' : 'default'}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
          {status}
          {running ? (
            <Play size={11} strokeWidth={2.2} fill="currentColor" />
          ) : (
            <Pause size={11} strokeWidth={2.2} />
          )}
        </span>
      </Tag>
    </Button>
  );
  if (!managed || needsSetup || disabled) return button;

  const actions: Action[] = running ? ['stop', 'restart'] : ['start'];
  return (
    <Popover
      placement="right"
      trigger={['hover', 'click']}
      open={open}
      onOpenChange={setOpen}
      content={
        <Space size={0} split={<Divider type="vertical" />}>
          {actions.map((action) => (
            <Button
              key={action}
              type="link"
              size="small"
              onClick={() => void execute(action).catch(() => {})}
            >
              {labels[action]}
            </Button>
          ))}
        </Space>
      }
    >
      {button}
    </Popover>
  );
}
