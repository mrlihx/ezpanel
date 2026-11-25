import { me } from './auth';

export function openEventStream(
  url: string,
  listeners: Record<string, (data: unknown) => void>,
  onError: (message: string, reconnecting: boolean) => void,
) {
  const source = new EventSource(url);
  for (const [name, listen] of Object.entries(listeners))
    source.addEventListener(name, (event: MessageEvent<string>) => {
      try {
        listen(JSON.parse(event.data));
      } catch {
        // 忽略损坏的 SSE 数据帧。
      }
    });
  source.addEventListener('stream-error', (event: MessageEvent<string>) => {
    source.close();
    let msg = event.data || '实时流连接异常';
    try {
      msg = String(JSON.parse(event.data));
    } catch {
      // 纯文本错误直接展示原文。
    }
    onError(msg, false);
  });
  source.addEventListener('session-expired', () => {
    source.close();
    onError('会话已失效', false);
    void me().catch(() => {});
  });
  source.onerror = () => {
    const reconnecting = source.readyState === EventSource.CONNECTING;
    onError(
      reconnecting ? '连接中断，正在重连' : '实时连接无法建立',
      reconnecting,
    );
    if (!reconnecting) void me().catch(() => {});
  };
  return () => source.close();
}
