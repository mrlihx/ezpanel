import { useState } from 'react';
import { ReloadOutlined } from '@ant-design/icons';
import { App, Button, Form, Input, Modal, Space } from 'antd';
import type { Database } from '../../api/database';
import { request } from '../../api/client';
import { LAYOUT } from '../../styles/tokens';

export default function DatabasePasswordModal({
  serverId,
  database,
  onClose,
  onSaved,
}: {
  serverId: string;
  database: Database;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const { message } = App.useApp();
  return (
    <Modal
      open
      title={`设置数据库[${database.name}]密码`}
      width={LAYOUT.databaseModalWidth}
      maskClosable={false}
      confirmLoading={busy}
      cancelText="取消"
      okText="确认"
      onCancel={onClose}
      onOk={async () => {
        const values = await form.validateFields();
        setBusy(true);
        try {
          await request(
            `/api/db/servers/${serverId}/databases/${encodeURIComponent(database.name)}/password`,
            { method: 'PUT', data: { password: values.password } },
          );
          message.success('数据库密码已修改');
          onSaved();
          onClose();
        } finally {
          setBusy(false);
        }
      }}
    >
      <Form
        form={form}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1 }}
        colon={false}
      >
        <Form.Item label="用户名">
          <Input value={database.username} readOnly />
        </Form.Item>
        <Form.Item label="新密码" required>
          <Space.Compact style={{ width: '100%' }}>
            <Form.Item
              name="password"
              noStyle
              rules={[
                { required: true, message: '请输入新密码' },
                { max: 1024, message: '密码不能超过1024个字符' },
              ]}
            >
              <Input.Password autoComplete="new-password" />
            </Form.Item>
            <Button
              icon={<ReloadOutlined />}
              aria-label="生成随机数据库密码"
              onClick={() => {
                const alphabet =
                  'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';
                form.setFieldValue(
                  'password',
                  'Aa9!' +
                    Array.from(
                      crypto.getRandomValues(new Uint8Array(12)),
                      (value) => alphabet[value % alphabet.length],
                    ).join(''),
                );
              }}
            />
          </Space.Compact>
        </Form.Item>
      </Form>
    </Modal>
  );
}
