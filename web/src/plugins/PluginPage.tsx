import {
  Component,
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type ReactNode,
} from 'react';
import { App, Button, Result, Spin } from 'antd';
import {
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from 'react-router-dom';
import { APIError } from '../api/client';
import { pluginDetail, usePluginList } from '../api/plugin';
import { useTheme } from '../stores/theme';
import { sharedReady } from './shared';
import PluginFrame from './PluginFrame';

import type {
  PanelMenuResource,
  PanelResourceKind,
  PanelSDK,
} from '../../../sdk/ts/panel-sdk';
const stylesheetURLs = new Map<string, Set<string>>();
const mountedAssets = new Map<
  string,
  { users: number; load: Promise<PluginModule> }
>();
const FilePicker = lazy(() => import('../pages/file/FilePicker'));
const DirectoryPicker = lazy(() => import('../pages/file/DirectoryPicker'));
const TaskDrawer = lazy(() => import('../components/TaskDrawer'));
const ResourcePicker = lazy(() => import('./ResourcePicker'));

type PickerRequest =
  | { type: 'file' | 'directory'; initialPath: string }
  | { type: 'resource'; kind: PanelResourceKind };

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
        subTitle={plugin?.runtimeError}
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
export function PluginRuntime({
  id,
  version,
  embeddedSiteId,
}: {
  id: string;
  version: string;
  embeddedSiteId?: string;
}) {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const { '*': subpath = '' } = useParams();
  const context = useLocation().state as {
    siteId?: string;
    filePaths?: string[];
    resource?: PanelMenuResource;
  } | null;
  const filePaths = embeddedSiteId ? undefined : context?.filePaths;
  const resource = embeddedSiteId ? undefined : context?.resource;
  const siteId =
    embeddedSiteId ??
    (resource?.kind === 'sites'
      ? resource.id
      : (searchParams.get('siteId') ?? context?.siteId));
  const pendingPick = useRef<((path: string | null) => void) | null>(null);
  const [picker, setPicker] = useState<PickerRequest>();
  const [task, setTask] = useState<string | null>(null);
  const pick = useCallback(
    (request: PickerRequest) =>
      new Promise<string | null>((resolve) => {
        pendingPick.current?.(null);
        pendingPick.current = resolve;
        setPicker(request);
      }),
    [],
  );
  function finishPick(path: string | null) {
    pendingPick.current?.(path);
    pendingPick.current = null;
    setPicker(undefined);
  }
  useEffect(() => () => pendingPick.current?.(null), []);
  const { message, modal } = App.useApp();
  const { mode, systemDark } = useTheme();
  const sdk = useMemo<PanelSDK>(
    () => ({
      sdkVersion: 1,
      pluginId: id,
      route: embeddedSiteId ? '/' : `/${subpath}`,
      context:
        siteId || filePaths?.length || resource
          ? { siteId: siteId ?? undefined, filePaths, resource }
          : undefined,
      pickFile: (initialPath = '/') => pick({ type: 'file', initialPath }),
      pickDirectory: (initialPath = '/') =>
        pick({ type: 'directory', initialPath }),
      pickResource: (kind) => pick({ type: 'resource', kind }),
      showTask: setTask,
      dark: mode === 'dark' || (mode === 'system' && systemDark),
      message,
      modal,
      navigate: (path) =>
        navigate(`/p/${encodeURIComponent(id)}${relativePath(path)}`, {
          state: { siteId, filePaths, resource },
        }),
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
    [
      id,
      embeddedSiteId,
      subpath,
      siteId,
      filePaths,
      resource,
      pick,
      mode,
      systemDark,
      message,
      modal,
      navigate,
    ],
  );
  const plugin = usePluginList().data?.find((row) => row.id === id);
  return (
    <div data-panel-plugin={id}>
      {plugin ? (
        plugin.uiMode !== 'legacy' ? (
          <PluginFrame
            key={`${id}:${version}`}
            sdk={sdk}
            embedded={!!embeddedSiteId}
          />
        ) : (
          <NativePluginView
            key={`${id}:${version}`}
            id={id}
            version={version}
            sdk={sdk}
          />
        )
      ) : (
        <Spin />
      )}
      <Suspense fallback={null}>
        {picker &&
          (picker.type === 'resource' ? (
            <ResourcePicker
              key={picker.kind}
              pluginId={id}
              kind={picker.kind}
              onFinish={finishPick}
            />
          ) : picker.type === 'directory' ? (
            <DirectoryPicker
              initialPath={picker.initialPath}
              onClose={() => finishPick(null)}
              onSelect={finishPick}
            />
          ) : (
            <FilePicker
              initialPath={picker.initialPath}
              onClose={() => finishPick(null)}
              onSelect={finishPick}
            />
          ))}
        {task && (
          <TaskDrawer
            open
            taskId={task}
            title="插件任务"
            onClose={() => setTask(null)}
          />
        )}
      </Suspense>
    </div>
  );
}

function NativePluginView({
  id,
  version,
  sdk,
}: {
  id: string;
  version: string;
  sdk: PanelSDK;
}) {
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
    let assets = mountedAssets.get(base);
    if (!assets) {
      const load = async () => {
        await sharedReady;
        const detail = await pluginDetail(id);
        if (mountedAssets.get(base) !== current)
          throw new Error('插件页面已关闭');
        if (
          detail.runtimeStatus !== 'running' ||
          detail.manifest.uiMode !== 'legacy' ||
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
        if (mountedAssets.get(base) !== current)
          throw new Error('插件页面已关闭');
        const url = base + detail.manifest.ui.slice('web/'.length);
        const module = (await import(/* @vite-ignore */ url)) as PluginModule;
        if (
          typeof module.default !== 'function' &&
          (typeof module.default !== 'object' || module.default === null)
        )
          throw new Error('插件未导出 React 组件');
        return module;
      };
      assets = { users: 0, load: load() };
      mountedAssets.set(base, assets);
    }
    const current = assets;
    current.users++;
    void current.load.then(
      (module) => {
        if (live) setLoaded({ id, version, module });
      },
      (e: unknown) => {
        if (live) setError(e instanceof Error ? e.message : '插件加载失败');
      },
    );
    return () => {
      live = false;
      if (--current.users !== 0) return;
      mountedAssets.delete(base);
      cleanCSS();
      void current.load.then(
        (module) => {
          if (mountedAssets.has(base)) return;
          try {
            module.dispose?.();
          } catch {
            console.error('插件 dispose 执行失败');
          }
          cleanCSS();
        },
        () => {},
      );
    };
  }, [id, version, attempt]);
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
    <PluginBoundary key={`${id}:${version}:${attempt}`} retry={retry}>
      <PluginComponent sdk={sdk} />
    </PluginBoundary>
  );
}
