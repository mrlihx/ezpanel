import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Flex,
  Form,
  Input,
  Modal,
  QRCode,
  Skeleton,
  Typography,
} from 'antd';
import {
  disableTOTP,
  enableTOTP,
  setupTOTP,
  type TOTPSetup,
} from '../../api/auth';
import { SPACING } from '../../styles/tokens';

export default function TOTPModal({
  mode,
  onSuccess,
  onClose,
}: {
  mode: 'enable' | 'disable';
  onSuccess: () => Promise<void> | void;
  onClose: () => void;
}) {
  const [form] = Form.useForm<{ code: string }>();
  const { message } = App.useApp();
  const [submitting, setSubmitting] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [overrideSetup, setOverrideSetup] = useState<TOTPSetup>();

  const setupQuery = useQuery({
    queryKey: ['panel-totp-setup'],
    queryFn: () => setupTOTP(false),
    enabled: mode === 'enable',
    gcTime: 0,
    retry: false,
  });
  const setup = overrideSetup ?? setupQuery.data;
  const loadingSetup = setupQuery.isPending && !overrideSetup;
  const setupError = setupQuery.error
    ? setupQuery.error instanceof Error
      ? setupQuery.error.message
      : '无法生成动态口令密钥'
    : '';

  const handleResetSecret = async () => {
    setResetting(true);
    try {
      const next = await setupTOTP(true);
      setOverrideSetup(next);
      form.resetFields();
      message.success('已生成新密钥，请重新扫码绑定');
    } finally {
      setResetting(false);
    }
  };

  const handleEnable = async (values: { code: string }) => {
    if (!setup) return;
    setSubmitting(true);
    try {
      await enableTOTP({ secret: setup.secret, code: values.code });
      message.success('动态口令认证已开启');
      await onSuccess();
      onClose();
    } finally {
      setSubmitting(false);
    }
  };

  const handleDisable = async () => {
    setSubmitting(true);
    try {
      await disableTOTP();
      message.success('动态口令认证已关闭');
      await onSuccess();
      onClose();
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open
      title={mode === 'enable' ? '开启动态口令认证' : '关闭动态口令认证'}
      width={460}
      okText={mode === 'enable' ? '验证并开启' : '确认关闭'}
      okButtonProps={{
        danger: mode === 'disable',
        disabled: mode === 'enable' && !setup,
      }}
      confirmLoading={submitting}
      destroyOnHidden
      onCancel={onClose}
      onOk={() => {
        if (mode === 'enable') form.submit();
        else void handleDisable();
      }}
    >
      {mode === 'enable' ? (
        loadingSetup ? (
          <Skeleton active />
        ) : setupError ? (
          <Alert type="error" message={setupError} />
        ) : setup ? (
          <Flex vertical gap={SPACING.md}>
            {setup.existing ? (
              <Alert
                type="info"
                showIcon
                message="已保留原有密钥，直接输入验证器当前 6 位验证码即可重新开启；如需重新绑定请点击「更换」。"
              />
            ) : (
              <Typography.Text type="secondary">
                请使用 Google Authenticator（或兼容 TOTP
                验证器）扫描下方二维码，或手动录入密钥：
              </Typography.Text>
            )}
            <Flex
              vertical
              align="center"
              gap={SPACING.sm}
              style={{
                padding: SPACING.md,
                borderRadius: 12,
                border: '1px solid var(--ant-color-border-secondary)',
              }}
            >
              <div
                style={{
                  padding: 10,
                  background: '#fff',
                  borderRadius: 10,
                  lineHeight: 0,
                }}
              >
                <QRCode value={setup.otpauthUrl} size={168} bordered={false} />
              </div>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {setup.issuer}（{setup.account}）
              </Typography.Text>
              <Flex
                align="center"
                justify="center"
                gap={SPACING.xs}
                wrap="wrap"
              >
                <Typography.Text code copyable style={{ marginInlineEnd: 0 }}>
                  {setup.secret}
                </Typography.Text>
                <Button
                  size="small"
                  loading={resetting}
                  onClick={() => void handleResetSecret()}
                >
                  更换
                </Button>
              </Flex>
            </Flex>
            <Form
              form={form}
              layout="vertical"
              colon={false}
              onFinish={(values) => void handleEnable(values)}
            >
              <Form.Item
                label="动态口令验证码"
                name="code"
                rules={[
                  { required: true, message: '请输入 6 位动态验证码' },
                  {
                    pattern: /^\d{6}$/,
                    message: '请输入 6 位数字验证码',
                  },
                ]}
                style={{ marginBottom: 0 }}
              >
                <Input
                  size="large"
                  maxLength={6}
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  placeholder="请输入验证器显示的 6 位数字"
                />
              </Form.Item>
            </Form>
          </Flex>
        ) : null
      ) : (
        <Alert
          type="warning"
          showIcon
          message="关闭后登录面板将不再校验动态口令验证码（原有密钥会保留，随时可免扫码重新开启），确认关闭吗？"
        />
      )}
    </Modal>
  );
}
