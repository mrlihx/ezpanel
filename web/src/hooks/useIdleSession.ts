import { useEffect } from 'react';
import { request } from '../api/client';
import { useAuth } from '../stores/auth';
import type { User } from '../api/auth';
// 只把实际输入视为活动，列表轮询和监控连接不延长登录。
export function useIdleSession() {
  const user = useAuth((s) => s.user);
  const setUser = useAuth((s) => s.setUser);
  const username = user?.username;
  const idleMinutes = user?.idleMinutes;
  useEffect(() => {
    if (!username || !idleMinutes) return;
    let lastInput = Date.now();
    let dirty = !document.hidden;
    let stopped = false;
    let sending = false;
    const input = () => {
      if (!document.hidden) {
        lastInput = Date.now();
        dirty = true;
      }
    };
    const events = [
      'pointerdown',
      'pointermove',
      'keydown',
      'wheel',
      'touchstart',
      'paste',
    ];
    for (const event of events)
      window.addEventListener(event, input, { passive: true, capture: true });
    const timer = window.setInterval(() => {
      if (document.hidden) return;
      if (Date.now() - lastInput >= idleMinutes * 60000) {
        // 多标签共用Cookie；以服务端会话活动为准，空闲标签不能注销正在使用的标签。
        if (!sending && !stopped) {
          sending = true;
          void request('/api/auth/me', { silentError: true })
            .catch(() => {})
            .finally(() => {
              sending = false;
            });
        }
        return;
      }
      if (!dirty || sending || stopped) return;
      dirty = false;
      sending = true;
      void request<User>('/api/auth/activity', {
        method: 'POST',
        silentError: true,
      })
        .then((data) => {
          if (!stopped) {
            const current = useAuth.getState().user;
            if (
              current &&
              (current.alias !== data.alias ||
                current.idleMinutes !== data.idleMinutes ||
                current.siteRoot !== data.siteRoot ||
                current.backupRoot !== data.backupRoot)
            )
              setUser(data);
          }
        })
        .catch(() => {})
        .finally(() => {
          sending = false;
        });
    }, 15000);
    return () => {
      stopped = true;
      window.clearInterval(timer);
      for (const event of events)
        window.removeEventListener(event, input, { capture: true });
    };
  }, [username, idleMinutes, setUser]);
}
