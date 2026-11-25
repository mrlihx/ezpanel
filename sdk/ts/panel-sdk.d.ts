import type { App } from "antd";
type PanelApp = ReturnType<typeof App.useApp>;
export interface PanelSDK {
  sdkVersion: 1;
  pluginId: string;
  dark: boolean;
  api: <T = unknown>(path: string, init?: RequestInit) => Promise<T>;
  navigate: (path: string) => void;
  message: PanelApp["message"];
  modal: PanelApp["modal"];
}
export interface PanelAPIError extends Error {
  code: number;
  status?: number;
}
