import type { App } from "antd";
type PanelApp = ReturnType<typeof App.useApp>;
export type PanelResourceKind =
  | "sites"
  | "containers"
  | "databases"
  | "certificates"
  | "channels";
export type PanelMenuResource = {
  kind: Exclude<PanelResourceKind, "channels">;
  id: string;
  name?: string;
  engine?: string;
};
export interface PanelSDK {
  sdkVersion: 1;
  pluginId: string;
  dark: boolean;
  route?: string;
  context?: { siteId?: string; filePaths?: string[]; resource?: PanelMenuResource };
  pickFile?: (initialPath?: string) => Promise<string | null>;
  pickDirectory?: (initialPath?: string) => Promise<string | null>;
  pickResource?: (kind: PanelResourceKind) => Promise<string | null>;
  showTask?: (taskId: string) => void;
  api: <T = unknown>(path: string, init?: RequestInit) => Promise<T>;
  navigate: (path: string) => void;
  message: PanelApp["message"];
  modal: PanelApp["modal"];
}
export interface PanelAPIError extends Error {
  code: number;
  status?: number;
}
