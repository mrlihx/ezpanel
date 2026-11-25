import {
  Component,
  useEffect,
  useMemo,
  useState,
  type ComponentType,
  type ReactNode,
} from 'react';
import { App, Button, Result, Spin } from 'antd';
import { useNavigate, useParams } from 'react-router-dom';
import { APIError } from '../api/client';
import { pluginDetail, usePluginList } from '../api/plugin';
import { useTheme } from '../stores/theme';
import { sharedReady } from './shared';

import type { PanelSDK } from '../../../sdk/ts/panel-sdk';
const stylesheetURLs = new Map<string, Set<string>>();

type PluginModule = {
  default: ComponentType<{ sdk: PanelSDK }>;
  dispose?: () => void;
};
class PluginBoundary extends Component<
  { children: ReactNode; retry: () => void },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? (
      <Result
        status="error"
        title="插件渲染失败"
        subTitle="请检查插件兼容性与开发者日志"
        extra={<Button onClick={this.props.retry}>重新加载</Button>}
      />
    ) : (
      this.props.children
    );
  }
}
function relativePath(input: string) {
  const decoded = decodeURIComponent(input);
  if (
    /^[a-z][a-z0-9+.-]*:/i.test(decoded) ||
    decoded.startsWith('//') ||
    /[\\\r\n]/.test(decoded) ||
    decoded.split(/[/?#]/).some((part) => part === '..' || part === '.') ||
    decoded.includes('\0')
  )
    throw new APIError(1001, '只允许插件内部相对路径');
  return input.startsWith('/') ? input : `/${input}`;
}
export default function PluginPage() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const query = usePluginList();
  if (query.error)
    return (
      <Result
        status="error"
        title="插件状态读取失败"
        subTitle={query.error.message}
        extra={<Button onClick={() => void query.refetch()}>重试</Button>}
      />
    );
  if (!query.data) return <Spin />;
  const plugin = query.data.find((p) => p.id === id);
  if (!plugin || plugin.runtimeStatus !== 'running')
    return (
      <Result
        status="warning"
        title="插件未运行"
        extra={<Button onClick={() => navigate('/apps')}>前往应用商店</Button>}
      />
    );
  return (
    <PluginRuntime
      key={`${id}:${plugin.version}`}
      id={id}
      version={plugin.version}
    />
  );
}
function PluginRuntime({ id, version }: { id: string; version: string }) {
  const navigate = useNavigate();
  const { message, modal } = App.useApp();
  const { mode, systemDark } = useTheme();
  const [loaded, setLoaded] = useState<{
    id: string;
    version: string;
    module: PluginModule;
  }>();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const retry = () => {
    setError('');
    setLoaded(undefined);
    setAttempt((n) => n + 1);
  };
  useEffect(() => {
    let live = true;
    let module: PluginModule | undefined;
    const base = `/plugin/${encodeURIComponent(id)}/ui/${encodeURIComponent(version)}/`;
    const cleanCSS = () => {
      const urls = stylesheetURLs.get(base) ?? new Set<string>();
      for (const link of document.querySelectorAll<HTMLLinkElement>(
        'link[rel="stylesheet"]',
      )) {
        if (link.href.startsWith(location.origin + base)) {
          urls.add(link.href);
          link.remove();
        }
      }
      stylesheetURLs.set(base, urls);
    };
    const dispose = () => {
      try {
        module?.dispose?.();
      } catch {
        console.error('插件 dispose 执行失败');
      }
      cleanCSS();
    };
    void (async () => {
      await sharedReady;
      const detail = await pluginDetail(id);
      if (!live) return;
      if (
        detail.runtimeStatus !== 'running' ||
        detail.manifest.version !== version
      )
        throw new Error('插件状态已变化，请重新加载');
      const urls = new Set(stylesheetURLs.get(base));
      urls.add(location.origin + base + 'entry.css');
      await Promise.all(
        [...urls].map(
          (href) =>
            new Promise<void>((resolve) => {
              const link = document.createElement('link');
              link.rel = 'stylesheet';
              link.href = href;
              link.onload = () => resolve();
              link.onerror = () => resolve();
              document.head.append(link);
            }),
        ),
      );
      if (!live) {
        cleanCSS();
        return;
      }
      const url = base + detail.manifest.ui.slice('web/'.length);
      module = (await import(/* @vite-ignore */ url)) as PluginModule;
      if (
        typeof module.default !== 'function' &&
        typeof module.default !== 'object'
      )
        throw new Error('插件未导出 React 组件');
      if (live) setLoaded({ id, version, module });
      else dispose();
    })().catch((e: unknown) => {
      if (live) setError(e instanceof Error ? e.message : '插件加载失败');
    });
    return () => {
      live = false;
      dispose();
    };
  }, [id, version, attempt]);
  const sdk = useMemo<PanelSDK>(
    () => ({
      sdkVersion: 1,
      pluginId: id,
      dark: mode === 'dark' || (mode === 'system' && systemDark),
      message,
      modal,
      navigate: (path) =>
        navigate(`/p/${encodeURIComponent(id)}${relativePath(path)}`),
      api: async <T,>(path: string, init?: RequestInit) => {
        try {
          const headers = new Headers(init?.headers);
          headers.set('X-Panel-Request', '1');
          const response = await fetch(
            `/plugin/${encodeURIComponent(id)}/api${relativePath(path)}`,
            { ...init, credentials: 'same-origin', headers },
          );
          const body = (await response.json()) as {
            code: number;
            msg: string;
            data: T;
          };
          if (!response.ok || body.code !== 0)
            throw new APIError(body.code, body.msg, response.status);
          return body.data;
        } catch (error) {
          if (error instanceof APIError || init?.signal?.aborted) throw error;
          throw new APIError(5000, '插件响应无效或连接失败');
        }
      },
    }),
    [id, mode, systemDark, message, modal, navigate],
  );
  if (error)
    return (
      <Result
        status="error"
        title="插件加载失败"
        subTitle={error}
        extra={<Button onClick={retry}>重新加载</Button>}
      />
    );
  if (!loaded || loaded.id !== id || loaded.version !== version)
    return <Spin />;
  const PluginComponent = loaded.module.default;
  return (
    <div data-panel-plugin={id}>
      <PluginBoundary key={`${id}:${version}:${attempt}`} retry={retry}>
        <PluginComponent sdk={sdk} />
      </PluginBoundary>
    </div>
  );
}
