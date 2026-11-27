import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button, Result, Spin } from 'antd';
import { APIError, request } from '../api/client';
import { SPACING } from '../styles/tokens';
import type { PanelResourceKind, PanelSDK } from '../../../sdk/ts/panel-sdk';
import {
  frameReady,
  type FrameContext,
  type FrameReply,
  type FrameRequest,
} from './frameProtocol';

export default function PluginFrame({
  sdk,
  embedded,
}: {
  sdk: PanelSDK;
  embedded: boolean;
}) {
  const id = sdk.pluginId;
  const [attempt, setAttempt] = useState(0);
  const [url, setURL] = useState('');
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    void request<{ url: string }>(
      `/api/plugins/${encodeURIComponent(id)}/ui-session`,
      { method: 'POST', signal: controller.signal, silentError: true },
    ).then(
      (data) => setURL(data.url),
      (err: unknown) => {
        if (!controller.signal.aborted)
          setError(err instanceof Error ? err.message : '无法启动隔离页面');
      },
    );
    return () => controller.abort();
  }, [id, attempt]);
  if (error)
    return (
      <Result
        status="error"
        title="插件隔离页面加载失败"
        subTitle={error}
        extra={
          <Button
            onClick={() => {
              setError('');
              setURL('');
              setAttempt((n) => n + 1);
            }}
          >
            重新加载
          </Button>
        }
      />
    );
  return url ? (
    <FrameDocument url={url} sdk={sdk} embedded={embedded} fail={setError} />
  ) : (
    <Spin />
  );
}

function FrameDocument({
  url,
  sdk,
  embedded,
  fail,
}: {
  url: string;
  sdk: PanelSDK;
  embedded: boolean;
  fail: (reason: string) => void;
}) {
  const frame = useRef<HTMLIFrameElement>(null);
  const port = useRef<MessagePort>();
  const value = useRef(sdk);
  const [contentHeight, setContentHeight] = useState(280);
  const [ready, setReady] = useState(false);
  const { dark, route, context } = sdk;
  useLayoutEffect(() => {
    value.current = sdk;
  }, [sdk]);
  useEffect(() => {
    port.current?.postMessage({
      context: { dark, route, context },
    } satisfies FrameReply);
  }, [dark, route, context]);
  useLayoutEffect(() => {
    const calls = new Map<number, AbortController>();
    const timer = window.setTimeout(
      () => fail('插件隔离运行时或入口未能加载'),
      15000,
    );
    const init = (event: MessageEvent) => {
      if (
        event.origin !== 'null' ||
        event.source !== frame.current?.contentWindow ||
        event.data !== frameReady ||
        port.current
      )
        return;
      const channel = new MessageChannel();
      port.current = channel.port1;
      channel.port1.onmessage = ({ data }: MessageEvent<FrameRequest>) => {
        if (!data || typeof data !== 'object') return;
        if (data.op === 'loaded') {
          window.clearTimeout(timer);
          setReady(true);
          return;
        }
        if (data.op === 'failed') {
          fail('插件入口执行失败，请检查插件兼容性');
          return;
        }
        if (data.op === 'size') {
          if (embedded && Number.isFinite(data.height))
            setContentHeight(
              Math.max(240, Math.min(2000, Math.ceil(data.height))),
            );
          return;
        }
        if (!('requestId' in data) || !Number.isSafeInteger(data.requestId))
          return;
        const requestId = data.requestId;
        if (data.op === 'cancel') {
          calls.get(requestId)?.abort();
          return;
        }
        if (calls.has(requestId)) return;
        if (calls.size >= 16) {
          channel.port1.postMessage({
            requestId,
            error: { message: '插件请求过多', code: 429, status: 429 },
          } satisfies FrameReply);
          return;
        }
        const controller = new AbortController();
        calls.set(requestId, controller);
        const run = async () => {
          const current = value.current;
          if (data.op === 'api') {
            const { path, method, headers, body } = data.input;
            if (typeof path !== 'string')
              throw new APIError(1001, '插件 API 路径无效');
            let content: BodyInit | undefined;
            if (body && typeof body === 'object' && 'form' in body) {
              const form = new FormData();
              for (const [key, item] of body.form) form.append(key, item);
              content = form;
            } else content = body;
            return current.api(path, {
              method,
              headers,
              body: content,
              signal: controller.signal,
              redirect: 'error',
            });
          }
          if (typeof data.input !== 'string')
            throw new APIError(1001, '插件参数无效');
          switch (data.op) {
            case 'navigate':
              return current.navigate(data.input);
            case 'pickFile':
              return current.pickFile?.(data.input);
            case 'pickDirectory':
              return current.pickDirectory?.(data.input);
            case 'pickResource':
              if (
                ![
                  'sites',
                  'containers',
                  'databases',
                  'certificates',
                  'channels',
                ].includes(data.input)
              )
                break;
              return current.pickResource?.(data.input as PanelResourceKind);
            case 'task':
              await request(
                `/api/plugins/${encodeURIComponent(current.pluginId)}/tasks/${encodeURIComponent(data.input)}`,
                { signal: controller.signal, silentError: true },
              );
              return current.showTask?.(data.input);
          }
          throw new APIError(1001, '插件能力未开放');
        };
        void run()
          .then(
            (result) =>
              channel.port1.postMessage({
                requestId,
                value: result,
              } satisfies FrameReply),
            (error: unknown) =>
              channel.port1.postMessage({
                requestId,
                error: {
                  message:
                    error instanceof Error ? error.message : '插件请求失败',
                  code: error instanceof APIError ? error.code : 5000,
                  status: error instanceof APIError ? error.status : undefined,
                },
              } satisfies FrameReply),
          )
          .finally(() => calls.delete(requestId));
      };
      const { dark, route, context } = value.current;
      const state: FrameContext = { dark, route, context };
      // 仅向刚刚发出握手的沙箱窗口移交端口。后续通信不经过全局消息事件。
      frame.current?.contentWindow?.postMessage(frameReady, '*', [
        channel.port2,
      ]);
      channel.port1.postMessage({ context: state } satisfies FrameReply);
    };
    window.addEventListener('message', init);
    return () => {
      window.removeEventListener('message', init);
      window.clearTimeout(timer);
      for (const call of calls.values()) call.abort();
      port.current?.close();
      port.current = undefined;
    };
  }, [embedded, fail, url]);
  return (
    <div style={{ position: 'relative' }} aria-busy={!ready}>
      {!ready && (
        <Spin
          style={{
            position: 'absolute',
            insetBlockStart: SPACING.lg,
            insetInlineStart: SPACING.lg,
          }}
        />
      )}
      <iframe
        ref={frame}
        src={url}
        title={`插件 ${sdk.pluginId}`}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        style={{
          display: 'block',
          border: 0,
          width: '100%',
          // 主布局已从动态视口高度扣除页眉、页脚和移动导航占用。
          height: embedded
            ? contentHeight
            : 'var(--panel-content-height, 100dvh)',
          maxHeight: embedded ? '75dvh' : undefined,
        }}
      />
    </div>
  );
}
