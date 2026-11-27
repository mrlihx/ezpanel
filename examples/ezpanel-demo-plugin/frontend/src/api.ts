import type { PanelAPIError, PanelSDK } from "./panel-sdk";

export function formatAPIError(err: unknown): string {
  if (err && typeof err === "object" && "message" in err) {
    const apiErr = err as Partial<PanelAPIError>;
    const msg = String(apiErr.message || "请求失败");
    if (msg.includes("缺少「") || msg.includes("应用商店")) {
      return msg;
    }
    if (
      apiErr.status === 403 ||
      apiErr.code === 4030 ||
      msg.includes("未获") ||
      msg.includes("未授权")
    ) {
      if (msg.includes("路径")) {
        return `${msg}（请确认路径位于插件授权目录 fsRoots: /tmp 范围内）`;
      }
      return `${msg}（请在「应用商店 → EzPanel演示插件 → 设置 → 授权」中配置对应资源范围或凭据绑定）`;
    }
    return msg;
  }
  return "请求失败";
}

export async function postJSON<T = unknown>(
  sdk: PanelSDK,
  path: string,
  body: unknown,
  signal?: AbortSignal,
): Promise<T> {
  return sdk.api<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal,
  });
}

export async function putJSON<T = unknown>(
  sdk: PanelSDK,
  path: string,
  body: unknown,
  signal?: AbortSignal,
): Promise<T> {
  return sdk.api<T>(path, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal,
  });
}

export function formatBytes(bytes?: number): string {
  if (bytes == null || Number.isNaN(bytes)) return "-";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let idx = -1;
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024;
    idx++;
  }
  return `${value.toFixed(1)} ${units[idx]}`;
}
