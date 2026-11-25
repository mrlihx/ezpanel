import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { App, Button, Input, Modal, Popover, Space, theme } from 'antd';
import { Pause, Play } from 'lucide-react';
import {
  applicationService,
  uninstallApplication,
  type Application,
} from '../../api/software';
import { useMessageBox } from '../../stores/messageBox';
import ApplicationSettings from './ApplicationSettings';
export default function ApplicationServiceControl({
  app,
  onChanged,
  onAuthentication,
  disabled = false,
  onUninstall,
}: {
  app: Application;
  onChanged: () => void;
  onAuthentication?: () => void;
  disabled?: boolean;
  onUninstall?: () => void;
}) {
  const { message, modal } = App.useApp();
  const { token } = theme.useToken();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [hover, setHover] = useState(false);
  const [busy, setBusy] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [confirmation, setConfirmation] = useState('');
  const control = async (action: 'start' | 'stop' | 'restart' | 'reload') => {
    setHover(false);
    setBusy(true);
    try {
      await applicationService(app.id, action);
      void message.success('服务操作已完成');
      onChanged();
    } finally {
      setBusy(false);
    }
  };
  const serviceAction = (action: 'start' | 'stop' | 'restart' | 'reload') => {
    if (app.id === 'openresty' && (action === 'stop' || action === 'restart')) {
      setHover(false);
      modal.confirm({
        title: `${action === 'stop' ? '停止' : '重启'} OpenResty？`,
        content: '操作期间，站点访问可能中断。',
        onOk: () => control(action),
      });
      return;
    }
    void control(action).catch(() => {});
  };
  return (
    <>
      <Popover
        open={hover}
        onOpenChange={setHover}
        content={
          <Space>
            {app.running !== null && (
              <Button
                type="link"
                disabled={busy || disabled || !app.manageable}
                onClick={() => serviceAction(app.running ? 'stop' : 'start')}
              >
                {app.running ? '停止' : '启动'}
              </Button>
            )}
            <Button
              type="link"
              disabled={busy || disabled || !app.manageable}
              onClick={() => serviceAction('restart')}
            >
              重启
            </Button>
            {app.id === 'openresty' && (
              <Button
                type="link"
                disabled={busy || disabled || !app.manageable || !app.running}
                onClick={() => serviceAction('reload')}
              >
                重载配置
              </Button>
            )}
          </Space>
        }
      >
        <Button
          disabled={!app.installed || disabled}
          loading={busy}
          icon={
            app.running ? (
              <Play
                size={13}
                strokeWidth={2.2}
                fill="currentColor"
                style={{ color: token.colorSuccess }}
              />
            ) : (
              <Pause
                size={13}
                strokeWidth={2.2}
                style={{ color: token.colorError }}
              />
            )
          }
          iconPosition="end"
          onClick={() => {
            setHover(false);
            setOpen(true);
          }}
        >
          {app.name}
          {app.version ? ` ${app.version}` : ''}
        </Button>
      </Popover>
      {open && (
        <ApplicationSettings
          app={app}
          onAuthentication={onAuthentication}
          busy={busy}
          control={serviceAction}
          onClose={() => setOpen(false)}
          switchVersion={() => {
            setOpen(false);
            navigate(`/apps?settings=${encodeURIComponent(app.id)}`);
          }}
          uninstall={
            onUninstall ??
            (() => {
              setConfirmation('');
              setRemoving(true);
            })
          }
        />
      )}
      <Modal
        open={removing}
        title={`卸载${app.name}`}
        onCancel={() => setRemoving(false)}
        confirmLoading={busy}
        okButtonProps={{ danger: true, disabled: confirmation !== app.name }}
        onOk={async () => {
          setBusy(true);
          try {
            const result = await uninstallApplication(app.id, confirmation);
            setRemoving(false);
            setOpen(false);
            useMessageBox.getState().show(result.taskId);
            onChanged();
          } finally {
            setBusy(false);
          }
        }}
      >
        <Input
          aria-label="确认软件名称"
          placeholder={`输入 ${app.name} 确认卸载`}
          value={confirmation}
          onChange={(e) => setConfirmation(e.target.value)}
        />
      </Modal>
    </>
  );
}
