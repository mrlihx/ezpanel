import { useAuth } from '../stores/auth';

export function saveBlob(content: Blob | string, name: string, mime?: string) {
  const blob =
    typeof content === 'string'
      ? new Blob([content], { type: mime ?? 'text/plain;charset=utf-8' })
      : content;
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = name;
  anchor.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export async function fetchDownload(
  url: string,
  fallbackName: string,
  fallbackError = '文件下载失败',
) {
  const response = await fetch(url, {
    credentials: 'same-origin',
    headers: { 'X-Panel-Request': '1' },
  });
  if (!response.ok) {
    let msg = fallbackError;
    let code: number | undefined;
    try {
      const body = (await response.json()) as { code?: number; msg?: string };
      if (typeof body.code === 'number') code = body.code;
      if (body.msg) msg = body.msg;
    } catch {
      // 非 JSON 响应（如反代 502/504 HTML）回退到默认错误提示。
    }
    if (code === 1002 || response.status === 401) {
      useAuth.getState().setUser(null);
      if (location.pathname !== '/login') {
        location.assign(
          `/login?redirect=${encodeURIComponent(location.pathname + location.search)}`,
        );
      }
    }
    throw new Error(msg);
  }
  const disposition = response.headers.get('Content-Disposition') ?? '';
  const encodedName = /filename\*=UTF-8''([^;]+)/i.exec(disposition)?.[1];
  const filename = /filename="([^"]+)"|filename=([^;]+)/i.exec(disposition);
  const resolvedName = encodedName
    ? decodeURIComponent(encodedName)
    : (filename?.[1] ?? filename?.[2]?.trim() ?? fallbackName);
  saveBlob(await response.blob(), resolvedName);
}
