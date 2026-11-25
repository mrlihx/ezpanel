import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  Flex,
  Form,
  Input,
  Result,
  Typography,
  theme,
} from 'antd';
import { Lock, Server, User } from 'lucide-react';
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom';
import { branding, login, me } from '../../api/auth';
import { useAuth } from '../../stores/auth';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { APIError } from '../../api/client';
import { safeRedirect } from '../../utils/format';

export default function Login() {
  const { token } = theme.useToken();
  const panel = useQuery({
    queryKey: ['panel-branding'],
    queryFn: branding,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    retry: false,
  });
  const alias = panel.data?.alias || 'ezpanel';
  useEffect(() => {
    document.title = alias;
  }, [alias]);
  const user = useAuth((s) => s.user);
  const setUser = useAuth((s) => s.setUser);
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const ready = useAuth((s) => s.ready);
  useEffect(() => {
    if (ready) return;
    let live = true;
    void me()
      .then((value) => {
        if (live) setUser(value);
      })
      .catch((error) => {
        if (!live) return;
        if (error instanceof APIError && error.code === 1002) setUser(null);
        else setError(error instanceof Error ? error.message : '连接失败');
      });
    return () => {
      live = false;
    };
  }, [ready, setUser]);
  if (user)
    return <Navigate to={safeRedirect(params.get('redirect'))} replace />;
  if (panel.isPending || (panel.isFetching && !panel.isFetchedAfterMount))
    return null;
  if (panel.isError) {
    const missing =
      panel.error instanceof APIError && panel.error.status === 404;
    return (
      <Result
        status={missing ? '404' : 'error'}
        title={missing ? '页面不存在' : '无法连接面板'}
      />
    );
  }
  const submit = async (values: { username: string; password: string }) => {
    setBusy(true);
    setError('');
    try {
      setUser(await login(values));
      navigate(safeRedirect(params.get('redirect')), { replace: true });
    } catch (error) {
      setError(error instanceof Error ? error.message : '登录失败');
    } finally {
      setBusy(false);
    }
  };
  return (
    <Flex
      align="center"
      justify="center"
      style={{
        minHeight: '100dvh',
        padding: SPACING.xl,
        background: token.colorBgLayout,
      }}
    >
      <Card
        style={{
          width: '100%',
          maxWidth: LAYOUT.loginWidth,
          borderRadius: 22,
          boxShadow: '0 12px 40px rgba(20, 20, 22, 0.08)',
        }}
        styles={{ body: { padding: SPACING.xxl } }}
      >
        <Flex vertical gap={SPACING.xl}>
          <Flex
            vertical
            align="center"
            gap={SPACING.xs}
            style={{ textAlign: 'center' }}
          >
            <div
              style={{
                width: 52,
                height: 52,
                display: 'grid',
                placeItems: 'center',
                borderRadius: 16,
                background: 'linear-gradient(135deg, #0071E3, #AF52DE)',
                color: '#FFFFFF',
                boxShadow: '0 6px 16px rgba(0, 113, 227, 0.32)',
                marginBottom: SPACING.xs,
              }}
            >
              <Server size={26} strokeWidth={2} />
            </div>
            <Typography.Title
              level={4}
              style={{ margin: 0, fontWeight: 700, letterSpacing: '-0.02em' }}
            >
              {alias}
            </Typography.Title>
            <Typography.Text type="secondary" style={{ fontSize: 13 }}>
              登录以管理你的服务器
            </Typography.Text>
          </Flex>
          {error && <Alert type="error" showIcon message={error} />}
          <Form layout="vertical" onFinish={submit} validateTrigger="onBlur">
            <Form.Item
              name="username"
              label="用户名"
              rules={[{ required: true, message: '请输入用户名' }]}
            >
              <Input
                autoComplete="username"
                prefix={
                  <User
                    size={16}
                    strokeWidth={1.9}
                    style={{ color: token.colorTextTertiary }}
                  />
                }
                placeholder="请输入用户名"
                size="large"
              />
            </Form.Item>
            <Form.Item
              name="password"
              label="密码"
              rules={[{ required: true, message: '请输入密码' }]}
            >
              <Input.Password
                autoComplete="current-password"
                prefix={
                  <Lock
                    size={16}
                    strokeWidth={1.9}
                    style={{ color: token.colorTextTertiary }}
                  />
                }
                placeholder="请输入密码"
                size="large"
              />
            </Form.Item>
            <Button
              type="primary"
              size="large"
              block
              htmlType="submit"
              loading={busy}
              style={{ marginTop: SPACING.xs, height: 44 }}
            >
              登录面板
            </Button>
          </Form>
        </Flex>
      </Card>
    </Flex>
  );
}
