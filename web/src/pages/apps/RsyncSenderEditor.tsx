import { useState } from 'react';
import { App, Form, Input, InputNumber, Modal, Segmented, Select } from 'antd';
import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import type { RsyncRule, RsyncSender } from '../../api/rsync';
import HostToolDirectory from './HostToolDirectory';

type SenderFields = RsyncSender & { host?: string; port?: number };

export default function RsyncSenderEditor({
  sender,
  rules,
  saving,
  onClose,
  onSave,
}: {
  sender: RsyncSender;
  rules: RsyncRule[];
  saving: boolean;
  onClose: () => void;
  onSave: (row: RsyncSender) => Promise<unknown>;
}) {
  const { modal } = App.useApp();
  const [form] = Form.useForm<SenderFields>();
  const [local, setLocal] = useState(sender.destination.startsWith('/'));
  const schedule = Form.useWatch('schedule', form) ?? sender.schedule;
  const target =
    sender.destination && !sender.destination.startsWith('/')
      ? new URL(sender.destination)
      : undefined;
  function submit(fields: SenderFields) {
    const { host, port, ...values } = fields;
    let destination = values.destination;
    if (!local) {
      const hostname = host!.trim().replace(/^\[|\]$/g, '');
      const address = hostname.includes(':') ? `[${hostname}]` : hostname;
      const sameTarget =
        target &&
        target.hostname.replace(/^\[|\]$/g, '').toLowerCase() ===
          hostname.toLowerCase() &&
        Number(target.port || 873) === port &&
        sender.username === values.username;
      destination = sameTarget
        ? sender.destination
        : `rsync://${address}:${port}/${values.username}`;
    }
    const row = {
      ...values,
      id: sender.id,
      destination,
      username: local ? '' : values.username,
      password: local ? '' : values.password,
    };
    const save = () => onSave(row);
    if (row.mode === 'full')
      modal.confirm({
        title: '保存完全同步任务？',
        content: '执行时将删除目标端多余文件，排除文件会保留。',
        onOk: save,
      });
    else void save();
  }
  return (
    <Modal
      open
      title={sender.id ? '编辑发送任务' : '创建发送任务'}
      width={720}
      onCancel={onClose}
      onOk={() => form.submit()}
      confirmLoading={saving}
      destroyOnHidden
      maskClosable={false}
    >
      <ApplicationSettingsForm
        form={form}
        initialValues={{
          ...sender,
          host: target?.hostname.replace(/^\[|\]$/g, ''),
          port: Number(target?.port || 873),
        }}
        autoComplete="off"
        disabled={saving}
        onFinish={submit}
      >
        <Form.Item
          name="name"
          label="任务名称"
          rules={[{ required: true, whitespace: true }]}
        >
          <Input maxLength={256} />
        </Form.Item>
        <Form.Item
          name="source"
          label="数据源目录"
          rules={[{ required: true }]}
        >
          <HostToolDirectory />
        </Form.Item>
        <Form.Item label="目标类型">
          <Segmented
            value={local ? 'local' : 'remote'}
            options={[
              { value: 'remote', label: '远程 Rsync' },
              { value: 'local', label: '本机目录' },
            ]}
            onChange={(value) => {
              setLocal(value === 'local');
              form.setFieldsValue({
                destination: '',
                host: '',
                username: '',
                password: '',
              });
            }}
          />
        </Form.Item>
        {local ? (
          <Form.Item
            name="destination"
            label="目标目录"
            rules={[{ required: true }]}
            preserve={false}
          >
            <HostToolDirectory />
          </Form.Item>
        ) : (
          <>
            <Form.Item
              name="host"
              label="IP/域名"
              rules={[{ required: true, whitespace: true }]}
              preserve={false}
            >
              <Input placeholder="192.168.10.97 或 sync.example.com" />
            </Form.Item>
            <Form.Item name="port" label="端口" rules={[{ required: true }]}>
              <InputNumber min={1} max={65535} precision={0} />
            </Form.Item>
            <Form.Item
              name="username"
              label="用户名"
              rules={[{ required: true, whitespace: true }]}
            >
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item
              name="password"
              label="密码"
              rules={[{ required: !sender.id }]}
            >
              <Input.Password
                autoComplete="new-password"
                placeholder={
                  sender.id
                    ? '地址和用户名未变时，留空保留原密码'
                    : '填写接收任务的密码'
                }
              />
            </Form.Item>
          </>
        )}
        <Form.Item
          name="mode"
          label="同步方式"
          rules={[{ required: true }]}
          extra="增量保留目标端额外文件，完全同步清理目标端多余文件。"
        >
          <Segmented
            options={[
              { value: 'incremental', label: '增量' },
              { value: 'full', label: '完全' },
            ]}
          />
        </Form.Item>
        <Form.Item
          name="bandwidthKb"
          label="限速"
          rules={[{ required: true }]}
          extra="0 表示不限速。"
        >
          <InputNumber min={0} precision={0} addonAfter="KB/s" />
        </Form.Item>
        <Form.Item
          name="schedule"
          label="同步周期"
          rules={[{ required: true }]}
        >
          <Segmented
            options={[
              { value: 'realtime', label: '实时同步' },
              { value: 'interval', label: '定时同步' },
            ]}
          />
        </Form.Item>
        <Form.Item
          name="checkMinutes"
          label="定时同步检查"
          rules={[{ required: true }]}
          extra={
            schedule === 'realtime'
              ? '0 表示关闭定时检查，文件变化仍触发实时同步。'
              : '0 表示关闭自动同步，可手动执行。'
          }
        >
          <InputNumber
            min={0}
            max={153722867}
            precision={0}
            addonBefore="每隔"
            addonAfter="分钟"
          />
        </Form.Item>
        <Form.Item name="ruleId" label="排除规则">
          <Select
            allowClear
            placeholder="选择排除规则"
            options={rules.map((rule) => ({
              value: rule.id,
              label: rule.name,
            }))}
          />
        </Form.Item>
      </ApplicationSettingsForm>
    </Modal>
  );
}
