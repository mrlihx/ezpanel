import { useEffect, useRef, useState } from 'react';
import { me } from '../api/auth';
import { APIError } from '../api/client';
// 浏览器不公开握手状态码；失败后用受保护接口确认会话，不绕过安全入口。
export function useWebSocket<T>(
  path: string,
  onPoint: (point: T) => void,
  subscription?: string,
) {
  const current = useRef<WebSocket>();
  const pending = useRef(subscription);
  const onPointRef = useRef(onPoint);
  useEffect(() => {
    onPointRef.current = onPoint;
  }, [onPoint]);
  useEffect(() => {
    pending.current = subscription;
    if (subscription && current.current?.readyState === WebSocket.OPEN)
      current.current.send(subscription);
  }, [subscription]);
  const [state, setState] = useState('连接中');
  useEffect(() => {
    let stopped = false;
    let socket: WebSocket;
    let timer: ReturnType<typeof setTimeout>;
    let attempts = 0;
    const connect = () => {
      socket = new WebSocket(
        `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}${path}`,
      );
      current.current = socket;
      socket.onopen = () => {
        if (pending.current) socket.send(pending.current);
        attempts = 0;
        setState('实时连接');
      };
      socket.onmessage = (event) => {
        try {
          onPointRef.current(JSON.parse(event.data) as T);
        } catch {
          // 忽略异常截断的非 JSON 帧，避免中断事件循环。
        }
      };
      socket.onclose = (event) => {
        if (stopped) return;
        if (event.code === 4001) {
          setState('会话已失效');
          void me().catch(() => {});
          return;
        }
        setState('连接中断，正在重连');
        void me()
          .then(() => {
            if (!stopped)
              timer = setTimeout(
                connect,
                [1000, 2000, 4000, 8000, 16000, 30000][Math.min(attempts++, 5)],
              );
          })
          .catch((error: unknown) => {
            if (stopped) return;
            if (error instanceof APIError && error.code === 1002) {
              setState('会话已失效');
              return;
            }
            if (error instanceof APIError && error.status === 404) {
              setState('安全入口无效');
              return;
            }
            timer = setTimeout(
              connect,
              [1000, 2000, 4000, 8000, 16000, 30000][Math.min(attempts++, 5)],
            );
          });
      };
    };
    connect();
    return () => {
      stopped = true;
      clearTimeout(timer);
      socket.close();
      current.current = undefined;
    };
  }, [path]);
  return state;
}
