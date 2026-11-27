import { createRoot } from 'react-dom/client';
import { Component, type ComponentType, type ReactNode } from 'react';
import { App, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { getTheme } from '../styles/tokens';
import type { PanelSDK } from '../../../sdk/ts/panel-sdk';
import { sharedReady } from './shared';
import {
  frameReady,
  type FrameAPI,
  type FrameCall,
  type FrameContext,
  type FrameReply,
  type FrameRequest,
} from './frameProtocol';

class FrameBoundary extends Component<
  { children: ReactNode; fail: () => void },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  componentDidCatch() {
    this.props.fail();
  }
  render() {
    return this.state.failed ? null : this.props.children;
  }
}

const element = document.getElementById('root');
if (element?.dataset.entry && parent !== window) {
  const init = (event: MessageEvent) => {
    if (
      event.source !== parent ||
      event.origin !== location.origin ||
      event.data !== frameReady ||
      !event.ports[0]
    )
      return;
    window.removeEventListener('message', init);
    void start(element, event.ports[0]);
  };
  window.addEventListener('message', init);
  parent.postMessage(frameReady, location.origin);
}

async function start(element: HTMLElement, port: MessagePort) {
  const send = (message: FrameRequest) => port.postMessage(message);
  const pending = new Map<
    number,
    (reply: Extract<FrameReply, { requestId: number }>) => void
  >();
  let next = 0;
  let context: FrameContext = { dark: false };
  const root = createRoot(element);
  let Plugin: ComponentType<{ sdk: PanelSDK }> | undefined;
  const id = element.dataset.plugin!;
  const call = <T,>(command: FrameCall, signal?: AbortSignal) =>
    new Promise<T>((resolve, reject) => {
      if (signal?.aborted) {
        reject(signal.reason);
        return;
      }
      const requestId = ++next;
      const cancel = () => {
        pending.delete(requestId);
        send({ op: 'cancel', requestId });
        reject(signal?.reason);
      };
      signal?.addEventListener('abort', cancel, { once: true });
      pending.set(requestId, (reply) => {
        signal?.removeEventListener('abort', cancel);
        if ('error' in reply)
          reject(Object.assign(new Error(reply.error.message), reply.error));
        else resolve(reply.value as T);
      });
      send({ ...command, requestId });
    });
  function View({
    state,
    Plugin,
  }: {
    state: FrameContext;
    Plugin: ComponentType<{ sdk: PanelSDK }>;
  }) {
    const { message, modal } = App.useApp();
    const sdk: PanelSDK = {
      sdkVersion: 1,
      pluginId: id,
      ...state,
      message,
      modal,
      pickFile: (path = '/') => call({ op: 'pickFile', input: path }),
      pickDirectory: (path = '/') => call({ op: 'pickDirectory', input: path }),
      pickResource: (kind) => call({ op: 'pickResource', input: kind }),
      showTask: (task) => {
        void call({ op: 'task', input: task }).catch(() =>
          message.error('插件任务不可查看'),
        );
      },
      navigate: (path) => {
        void call({ op: 'navigate', input: path }).catch(() =>
          message.error('插件地址无效'),
        );
      },
      api: async <T,>(path: string, init?: RequestInit) => {
        let body: FrameAPI['body'];
        if (init?.body instanceof FormData)
          body = { form: [...init.body.entries()] };
        else if (init?.body instanceof URLSearchParams) {
          body = init.body.toString();
          const headers = new Headers(init.headers);
          if (!headers.has('Content-Type'))
            headers.set(
              'Content-Type',
              'application/x-www-form-urlencoded;charset=UTF-8',
            );
          init = { ...init, headers };
        } else if (
          typeof init?.body === 'string' ||
          init?.body instanceof Blob ||
          init?.body instanceof ArrayBuffer
        )
          body = init.body;
        else if (ArrayBuffer.isView(init?.body))
          body = new Uint8Array(
            init.body.buffer,
            init.body.byteOffset,
            init.body.byteLength,
          ).slice().buffer;
        else if (init?.body != null)
          throw new Error('隔离页面不支持流式请求正文');
        return call<T>(
          {
            op: 'api',
            input: {
              path,
              headers: [...new Headers(init?.headers)],
              method: init?.method,
              body,
            },
          },
          init?.signal ?? undefined,
        );
      },
    };
    return <Plugin sdk={sdk} />;
  }
  const render = () => {
    if (!Plugin) return;
    document.documentElement.style.colorScheme = context.dark
      ? 'dark'
      : 'light';
    root.render(
      <FrameBoundary fail={() => send({ op: 'failed' })}>
        <ConfigProvider locale={zhCN} theme={getTheme(context.dark)}>
          <App>
            <View state={context} Plugin={Plugin} />
          </App>
        </ConfigProvider>
      </FrameBoundary>,
    );
  };
  port.onmessage = ({ data }: MessageEvent<FrameReply>) => {
    if ('context' in data) {
      context = data.context;
      render();
      return;
    }
    pending.get(data.requestId)?.(data);
    pending.delete(data.requestId);
  };
  try {
    await sharedReady;
    const base = new URL('assets/', location.href);
    const css = document.createElement('link');
    css.rel = 'stylesheet';
    css.crossOrigin = 'anonymous';
    css.href = new URL('entry.css', base).href;
    document.head.append(css);
    element.dataset.panelPlugin = id;
    const module = (await import(
      /* @vite-ignore */ new URL(element.dataset.entry!, base).href
    )) as { default?: typeof Plugin; dispose?: () => void };
    Plugin = module.default;
    if (!Plugin) throw new Error('缺少插件前端入口');
    render();
    const observer = new ResizeObserver(() =>
      send({ op: 'size', height: element.offsetHeight }),
    );
    observer.observe(element);
    window.addEventListener(
      'pagehide',
      () => {
        observer.disconnect();
        module.dispose?.();
        port.close();
      },
      { once: true },
    );
    send({ op: 'loaded' });
  } catch {
    send({ op: 'failed' });
  }
}
