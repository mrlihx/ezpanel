import { useState } from 'react';
import { Alert, Form, Input, Modal, Skeleton, Switch } from 'antd';
import { useQuery } from '@tanstack/react-query';
import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import { request } from '../../api/client';
import type { RsyncReceiver } from '../../api/rsync';
import HostToolDirectory from './HostToolDirectory';

export default function RsyncReceiverEditor({
  receiver,
  saving,
  onSave,
  onClose,
}: {
  receiver: RsyncReceiver;
  saving: boolean;
  onSave: (row: RsyncReceiver) => Promise<unknown>;
  onClose: () => void;
}) {
  const [form] = Form.useForm<RsyncReceiver>();
  const [visible, setVisible] = useState(true);
  const password = useQuery({
    queryKey: ['rsync-receiver-password', receiver.name],
    queryFn: () =>
      request<{ password: string }>(
        `/api/software/rsync/receivers/${encodeURIComponent(receiver.name)}/password`,
        { silentError: true },
      ),
    enabled: !!receiver.name,
    gcTime: 0,
    retry: false,
  });
  const loading = !!receiver.name && password.isPending;
  return (
    <Modal
      open
      title={receiver.name ? '编辑接收任务' : '创建接收任务'}
      width={680}
      onCancel={onClose}
      onOk={() => form.submit()}
      confirmLoading={saving}
      okButtonProps={{ disabled: loading || !!password.error }}
      destroyOnHidden
      maskClosable={false}
    >
      {loading ? (
        <Skeleton active />
      ) : password.error ? (
        <Alert type="error" message={password.error.message} />
      ) : (
        <ApplicationSettingsForm
          form={form}
          initialValues={{
            ...receiver,
            password: password.data?.password ?? receiver.password,
          }}
          disabled={saving}
          autoComplete="off"
          onFinish={(fields) => void onSave({ ...fields, name: receiver.name })}
        >
          <Form.Item name="path" label="接收目录" rules={[{ required: true }]}>
            <HostToolDirectory />
          </Form.Item>
          <Form.Item
            name="username"
            label="用户名"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true }]}>
            <Input.Password
              autoComplete="new-password"
              visibilityToggle={{ visible, onVisibleChange: setVisible }}
            />
          </Form.Item>
          <Form.Item
            name="allow"
            label="IP 白名单"
            extra="每行一个 IP 或 CIDR 网段，留空沿用全局白名单。"
          >
            <Input.TextArea
              rows={3}
              placeholder={'192.168.10.0/24\n192.168.10.10'}
            />
          </Form.Item>
          <Form.Item name="enabled" label="启用接收" valuePropName="checked">
            <Switch />
          </Form.Item>
        </ApplicationSettingsForm>
      )}
    </Modal>
  );
}
