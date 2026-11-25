import { useState } from 'react';
import { App, Button } from 'antd';
import { useNavigate } from 'react-router-dom';
import { request } from '../../api/client';

export default function PhpMyAdminButton({
  serverId,
  database,
}: {
  serverId: string;
  database: string;
}) {
  const [busy, setBusy] = useState(false);
  const { modal, message } = App.useApp();
  const navigate = useNavigate();
  return (
    <Button
      type="link"
      style={{ padding: 0 }}
      loading={busy}
      onClick={async () => {
        // 在点击事件中先创建窗口，避免异步请求完成后被浏览器拦截。
        const manager = window.open('about:blank', '_blank');
        if (!manager) {
          void message.warning('请允许此站点打开新窗口后重试');
          return;
        }
        manager.opener = null;
        setBusy(true);
        try {
          const state = await request<{
            installed: boolean;
            phpAvailable: boolean;
            phpVersion: string;
            reason: string;
          }>('/api/phpmyadmin/status');
          if (!state.installed || !state.phpAvailable) {
            manager.close();
            modal.confirm({
              title: !state.phpAvailable
                ? 'phpMyAdmin 需要 PHP 环境'
                : '安装 phpMyAdmin',
              content: !state.phpAvailable
                ? state.reason
                : '请先在应用商店安装 phpMyAdmin，安装后可从此处自动登录。',
              okText: '前往应用商店',
              cancelText: '取消',
              onOk: () =>
                navigate(
                  `/apps?install=${state.phpAvailable ? 'phpmyadmin' : `php-${state.phpVersion || '8.3'}`}`,
                ),
            });
            return;
          }
          const result = await request<{ url: string }>(
            '/api/phpmyadmin/open',
            {
              method: 'POST',
              data: { serverId, database },
            },
          );
          manager.location.replace(result.url);
        } catch {
          // 请求错误已由统一接口提示，关闭尚未加载内容的窗口。
          manager.close();
        } finally {
          setBusy(false);
        }
      }}
    >
      管理
    </Button>
  );
}
