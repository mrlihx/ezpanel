import { Button, Form, Input, Select } from 'antd';
import type { ToolboxPageState } from './index';

export default function ToolPasswordPanel({
  busy,
  confirm,
  formProps,
  password,
  passwordForm,
  save,
}: Pick<
  ToolboxPageState,
  'busy' | 'confirm' | 'formProps' | 'password' | 'passwordForm' | 'save'
>) {
  return (
    <>
      {password.data && (
        <Form
          {...formProps}
          form={passwordForm}
          initialValues={{ username: 'root' }}
          onFinish={(v) =>
            confirm(
              '修改系统登录密码？',
              '此处修改 Linux 用户密码，面板登录密码不变。请保存新密码以便后续 SSH 登录。',
              async () => {
                await save('password', {
                  username: v.username,
                  password: v.password,
                });
                passwordForm.resetFields(['password', 'confirm']);
              },
            )
          }
        >
          <Form.Item
            label="系统用户"
            name="username"
            rules={[{ required: true }]}
          >
            <Select
              options={password.data.users.map((value) => ({
                value,
              }))}
            />
          </Form.Item>
          <Form.Item
            label="新密码"
            name="password"
            rules={[{ required: true, min: 12, max: 256 }]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            label="确认密码"
            name="confirm"
            dependencies={['password']}
            rules={[
              { required: true },
              ({ getFieldValue }) => ({
                validator: (_, value) =>
                  !value || value === getFieldValue('password')
                    ? Promise.resolve()
                    : Promise.reject(new Error('两次密码不一致')),
              }),
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item label=" ">
            <Button type="primary" htmlType="submit" loading={busy}>
              修改密码
            </Button>
          </Form.Item>
        </Form>
      )}
    </>
  );
}
