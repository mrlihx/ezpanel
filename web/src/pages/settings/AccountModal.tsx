import { useState } from 'react';
import { App, Button, Form, Input, Modal, Space, Typography } from 'antd';
import { saveAccount } from '../../api/settings';
import { useAuth } from '../../stores/auth';
import { LAYOUT } from '../../styles/tokens';
import { validPassword } from '../../utils/format';

export default function AccountModal({
  kind,
  username,
  onClose,
}: {
  kind: 'username' | 'password';
  username: string;
  onClose: () => void;
}) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const { modal } = App.useApp();
  const setUser = useAuth((s) => s.setUser);
  const isPassword = kind === 'password';
  const field = isPassword ? '密码' : '账号';

  return (
    <Modal
      open
      maskClosable={false}
      title={`修改面板${field}`}
      width={LAYOUT.modalWidth}
      onCancel={onClose}
      confirmLoading={busy}
      okText="保存并重新登录"
      onOk={() => form.submit()}
    >
      <Form
        form={form}
        layout="horizontal"
        labelAlign="left"
        labelCol={{ flex: '96px' }}
        wrapperCol={{ flex: 1 }}
        colon={false}
        initialValues={isPassword ? undefined : { value: username }}
        onFinish={(values) => {
          modal.confirm({
            title: `保存新${field}并退出登录？`,
            content: '当前账号的已有会话将失效，请保存好新的登录信息。',
            onOk: async () => {
              setBusy(true);
              try {
                const result = await saveAccount({
                  username: isPassword ? username : values.value,
                  newPassword: isPassword ? values.value : undefined,
                });
                setUser(null);
                window.location.assign(result.loginUrl);
              } finally {
                setBusy(false);
              }
            },
          });
        }}
      >
        <Form.Item
          label={`新${field}`}
          required
          extra={
            isPassword
              ? '至少8个字符，包含字母和数字，最多72个UTF-8字节。'
              : '3至32位字母、数字或_.-，首位为字母、数字或下划线。'
          }
        >
          {isPassword ? (
            <Space.Compact style={{ width: '100%' }}>
              <Form.Item
                name="value"
                noStyle
                rules={[
                  { required: true, message: `请输入新${field}` },
                  {
                    validator: (_, value: string) =>
                      !value || validPassword(value)
                        ? Promise.resolve()
                        : Promise.reject(new Error('密码长度或格式不符合要求')),
                  },
                ]}
              >
                <Input.Password autoComplete="new-password" />
              </Form.Item>
              <Button
                onClick={() => {
                  const bytes = crypto.getRandomValues(new Uint8Array(12));
                  const password = `Aa1${Array.from(bytes, (n) => n.toString(16).padStart(2, '0')).join('')}`;
                  form.setFieldsValue({
                    value: password,
                    confirm: undefined,
                  });
                }}
              >
                随机生成
              </Button>
            </Space.Compact>
          ) : (
            <Form.Item
              name="value"
              noStyle
              rules={[
                { required: true, message: `请输入新${field}` },
                {
                  pattern: /^[a-zA-Z0-9_][a-zA-Z0-9_.-]{2,31}$/,
                  message: '账号格式不符合要求',
                },
              ]}
            >
              <Input autoComplete="username" maxLength={32} />
            </Form.Item>
          )}
        </Form.Item>
        <Form.Item
          label={`重复新${field}`}
          name="confirm"
          dependencies={['value']}
          rules={[
            { required: true, message: `请再次输入新${field}` },
            ({ getFieldValue }) => ({
              validator: (_, value) =>
                !value || value === getFieldValue('value')
                  ? Promise.resolve()
                  : Promise.reject(new Error(`两次${field}不一致`)),
            }),
          ]}
        >
          {isPassword ? (
            <Input.Password autoComplete="new-password" />
          ) : (
            <Input autoComplete="off" maxLength={32} />
          )}
        </Form.Item>
        <Typography.Text type="secondary">
          修改后需要重新登录，正在运行的服务任务会继续执行。
        </Typography.Text>
      </Form>
    </Modal>
  );
}
