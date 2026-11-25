import { useEffect } from 'react';
import { App as AntApp, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from 'react-router-dom';
import { getTheme } from './styles/tokens';
import { useTheme } from './stores/theme';
import { configureErrors } from './api/client';
import { router } from './router';
const client = new QueryClient({
  defaultOptions: {
    queries: { retry: false, staleTime: 30000, refetchOnWindowFocus: false },
  },
});
function RoutedContent() {
  const { message } = AntApp.useApp();
  useEffect(() => {
    configureErrors((text) => void message.error(text));
  }, [message]);
  return <RouterProvider router={router} />;
}
export default function App() {
  const { mode, systemDark } = useTheme();
  const dark = mode === 'dark' || (mode === 'system' && systemDark);
  useEffect(() => {
    document.body.classList.toggle('panel-theme-dark', dark);
    document.body.classList.toggle('panel-theme-light', !dark);
    document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
  }, [dark]);
  return (
    <ConfigProvider locale={zhCN} theme={getTheme(dark)}>
      <AntApp>
        <QueryClientProvider client={client}>
          <RoutedContent />
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}
