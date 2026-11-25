import { lazy, useEffect, useState } from 'react';
import { Button, Result, Skeleton } from 'antd';
import {
  Navigate,
  Outlet,
  Route,
  createBrowserRouter,
  createRoutesFromElements,
  useLocation,
} from 'react-router-dom';
import { me } from '../api/auth';
import { APIError } from '../api/client';
import { useAuth } from '../stores/auth';
import MainLayout from '../layouts/MainLayout';
import Login from '../pages/login';
import RouteNotFound from './RouteNotFound';

import { LAYOUT, SPACING } from '../styles/tokens';
const Dashboard = lazy(() => import('../pages/dashboard'));
const SecurityPage = lazy(() => import('../pages/security'));
const WAFPage = lazy(() => import('../pages/waf'));
const FilePage = lazy(() => import('../pages/file'));
const TerminalPage = lazy(() => import('../pages/terminal'));
const WebsitePage = lazy(() => import('../pages/website'));
const DatabasePage = lazy(() => import('../pages/database'));
const DockerPage = lazy(() => import('../pages/docker'));
const CronPage = lazy(() => import('../pages/cron'));
const OpLogPage = lazy(() => import('../pages/oplog'));
const SettingsPage = lazy(() => import('../pages/settings'));
const ToolsPage = lazy(() => import('../pages/tools'));
const AppStorePage = lazy(() => import('../pages/apps'));
const PluginPage = lazy(() => import('../plugins/PluginPage'));
function RequireAuth() {
  const { user, ready, setUser } = useAuth();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const location = useLocation();
  useEffect(() => {
    if (ready) return;
    let live = true;
    void me()
      .then((user) => {
        if (live) setUser(user);
      })
      .catch((e) => {
        if (!live) return;
        if (e instanceof APIError && e.code === 1002) setUser(null);
        else setError(e instanceof Error ? e.message : '连接失败');
      });
    return () => {
      live = false;
    };
  }, [ready, setUser, attempt]);
  if (error)
    return (
      <Result
        status="error"
        title="无法连接面板"
        subTitle={error}
        extra={
          <Button
            onClick={() => {
              setError('');
              setAttempt(attempt + 1);
            }}
          >
            重试连接
          </Button>
        }
      />
    );
  if (!ready) {
    const isDesktop =
      typeof window !== 'undefined' &&
      window.matchMedia(`(min-width: ${LAYOUT.mobileBreakpoint}px)`).matches;
    return (
      <div
        style={{
          minHeight: '100dvh',
          paddingInlineStart: isDesktop ? LAYOUT.siderWidth : 0,
        }}
      >
        <div style={{ padding: isDesktop ? SPACING.xl : SPACING.lg }}>
          <Skeleton active />
        </div>
      </div>
    );
  }
  return user ? (
    <Outlet />
  ) : (
    <Navigate
      to={`/login?redirect=${encodeURIComponent(location.pathname + location.search + location.hash)}`}
      replace
    />
  );
}
export const router = createBrowserRouter(
  createRoutesFromElements(
    <>
      <Route path="/login" element={<Login />} />
      <Route element={<RequireAuth />}>
        <Route element={<MainLayout />}>
          <Route index element={<Dashboard />} />
          <Route path="security/:section?/:view?" element={<SecurityPage />} />
          <Route path="waf/:section?" element={<WAFPage />} />
          <Route path="files" element={<FilePage />} />
          <Route
            path="websites/:section?/:siteId?/:siteTab?"
            element={<WebsitePage />}
          />
          <Route
            path="databases/:engine?/:section?"
            element={<DatabasePage />}
          />
          <Route
            path="docker/:section?/:view?/:resource?/:detailTab?"
            element={<DockerPage />}
          />
          <Route path="crons" element={<CronPage />} />
          <Route
            path="oplogs/:section?/:tab?/:source?"
            element={<OpLogPage />}
          />
          <Route path="apps/:section?" element={<AppStorePage />} />
          <Route path="tools/:section?" element={<ToolsPage />} />
          <Route
            path="settings/:section?/:alertTab?"
            element={<SettingsPage />}
          />
          <Route path="terminal/:section?" element={<TerminalPage />} />
          <Route path="plugins" element={<Navigate to="/apps" replace />} />
          <Route path="p/:id/*" element={<PluginPage />} />
          <Route path="*" element={<RouteNotFound />} />
        </Route>
      </Route>
    </>,
  ),
);
