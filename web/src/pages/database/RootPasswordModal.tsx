import { useEffect, useState } from 'react';
import { Alert, App, Button, Form, Input, Modal, Space, Tooltip } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { request } from '../../api/client';
export default function RootPasswordModal({
  serverId,
  base,
  local = false,
  onClose,
  onSaved,
  onTask,
}: {
  serverId?: string;
  base?: string;
  local?: boolean;
  onClose: () => void;
  onSaved: () => void;
  onTask?: (id: string) => void;
}) {
  const { message, modal } = App.useApp();
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const endpoint = base ?? `/api/db/servers/${serverId}`;
  useEffect(() => {
    let active = true;
    void request<{ password: string }>(`${endpoint}/root-password/reveal`, {
      method: 'POST',
    })
      .then((result) => {
        if (active) setPassword(result.password);
      })
      .catch((e: Error) => {
        if (active) setError(e.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [endpoint]);
  const submit = async (confirmation?: string) => {
    setSaving(true);
    try {
      if (local) {
        const result = await request<{ taskId: string }>(
          `${endpoint}/root-password/reset`,
          { method: 'POST', data: { password, confirmation } },
        );
        onTask?.(result.taskId);
      } else {
        await request(`${endpoint}/root-password`, {
          method: 'PUT',
          data: { password },
        });
        void message.success('ROOT密码已修改');
        onSaved();
      }
      onClose();
    } finally {
      setSaving(false);
    }
  };
  const confirm = () => {
    let value = '';
    modal.confirm({
      title: '强制重置本机ROOT密码？',
      content: (
        <Space direction="vertical">
          <span>
            不使用旧密码恢复root@localhost，MySQL会短暂重启。数据库自身密码策略仍需满足。
          </span>
          <Input
            aria-label="确认重置ROOT密码"
            placeholder="输入 确认重置ROOT密码"
            onChange={(e) => {
              value = e.target.value;
            }}
          />
        </Space>
      ),
      okText: '确认重置',
      okButtonProps: { danger: true },
      onOk: () => {
        if (value !== '确认重置ROOT密码') {
          void message.error('请输入确认重置ROOT密码');
          return Promise.reject(new Error('确认文案不符'));
        }
        return submit(value);
      },
    });
  };
  return (
    <Modal
      open
      title="ROOT密码"
      width={620}
      styles={{ body: { paddingBlock: 24 } }}
      onCancel={onClose}
      closable={!saving}
      cancelButtonProps={{ disabled: saving }}
      confirmLoading={saving}
      okText={local ? '强制重置' : '修改密码'}
      okButtonProps={{ disabled: loading || !password || (!local && !!error) }}
      onOk={() => (local ? confirm() : void submit())}
    >
      {error && (
        <Alert type="warning" message={error} style={{ marginBottom: 20 }} />
      )}
      <Form
        layout="horizontal"
        labelCol={{ flex: '100px' }}
        wrapperCol={{ flex: 1 }}
        colon={false}
      >
        <Form.Item label="ROOT密码" style={{ marginBottom: 12 }}>
          <Space.Compact style={{ width: '100%' }}>
            <Input
              aria-label="ROOT密码"
              autoComplete="off"
              value={password}
              disabled={loading || saving}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Tooltip title="随机密码">
              <Button
                aria-label="生成随机密码"
                disabled={loading || saving}
                icon={<ReloadOutlined />}
                onClick={() => {
                  const alphabet =
                    'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';
                  setPassword(
                    'Aa9!' +
                      Array.from(
                        crypto.getRandomValues(new Uint8Array(16)),
                        (n) => alphabet[n % alphabet.length],
                      ).join(''),
                  );
                }}
              />
            </Tooltip>
          </Space.Compact>
        </Form.Item>
        <Form.Item label=" " style={{ marginBottom: 0 }}>
          <span style={{ color: 'var(--ant-color-text-secondary, #737373)' }}>
            {local
              ? '显示面板保存的密码。强制重置无需旧密码，后台完成后同步面板凭据。'
              : '显示面板保存的密码，保存后同步管理员连接凭据。'}
          </span>
        </Form.Item>
      </Form>
    </Modal>
  );
}
