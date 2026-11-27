import { useQuery } from '@tanstack/react-query';
import { request } from './client';
export type PluginEnv = {
  key: string;
  label: string;
  default: string;
  required: boolean;
  secret: boolean;
  type?:
    | 'text'
    | 'password'
    | 'number'
    | 'boolean'
    | 'select'
    | 'file'
    | 'directory';
  options?: string[];
  help?: string;
};
export type PluginSettings = {
  type: 'modal' | 'drawer' | 'none';
  width?: number;
  height?: number;
};
export type Manifest = {
  id: string;
  name: string;
  version: string;
  description: string;
  author: string;
  homepage?: string;
  openMode?: 'embed' | 'new_tab';
  type: 'static' | 'process';
  ui: string;
  uiMode?: 'isolated' | 'legacy';
  menu?: {
    title: string;
    icon: string;
    order: number;
    children?: { title: string; path: string; icon?: string }[];
  };
  settings?: PluginSettings | null;
  permissions: string[];
  execAllow?: string[];
  fsRoots?: string[];
  env?: PluginEnv[];
  sandbox?: {
    cpuPercent: number;
    memoryMiB: number;
    maxProcesses: number;
    network: 'loopback' | 'outbound' | '';
  };
};
export type Plugin = Pick<
  Manifest,
  | 'id'
  | 'name'
  | 'version'
  | 'description'
  | 'author'
  | 'homepage'
  | 'openMode'
  | 'type'
  | 'menu'
  | 'settings'
  | 'uiMode'
> & {
  enabled: boolean;
  runtimeStatus: string;
  runtimeError?: string;
  installedAt: string;
  activeTaskId: string;
  hasCapabilities?: boolean;
  fileAction?: { title: string; multiple: boolean } | null;
  dashboard?: { title: string } | null;
  notificationProvider?: { title: string } | null;
  dnsProvider?: { title: string } | null;
  ssoProvider?: { title: string } | null;
  resourceActions?: Record<string, string> | null;
};
export type Detail = {
  manifest: Manifest;
  accessPreview: PluginAccessPreview;
  disabledPermissions?: string[];
  config: Record<string, { value?: string; isSet?: boolean }>;
  enabled: boolean;
  runtimeStatus: string;
};
export type PluginAccessItem = {
  label: string;
  scope: string;
  risk?: 'high' | 'attention';
};
export type PluginAccessPreview = {
  permissions: (PluginAccessItem & { code: string; enabled?: boolean })[];
  capabilities: { label: string; items: string[] }[];
  runtimeRisks: PluginAccessItem[];
};
export type PluginCredential = { id: string; name: string; updatedAt: string };
export const pluginCredentials = () =>
  request<PluginCredential[]>('/api/plugin-credentials');
export const savePluginCredential = (
  id: string | undefined,
  data: { name: string; value?: string },
) =>
  request<PluginCredential>(
    `/api/plugin-credentials${id ? `/${encodeURIComponent(id)}` : ''}`,
    { method: id ? 'PUT' : 'POST', data },
  );
export const deletePluginCredential = (id: string) =>
  request<null>(`/api/plugin-credentials/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
export type Inspection = {
  uploadId: string;
  expiresAt: string;
  installedVersion?: string;
  signatureVerified: boolean;
  revokedGrants?: string[];
  manifest: Manifest;
  accessPreview: PluginAccessPreview;
  permissionChanges: {
    permissions: string[];
    execAllow: string[];
    fsRoots: string[];
  };
};
export const pluginStatusLabel: Record<string, string> = {
  disabled: '未启用',
  starting: '启动中',
  waiting: '等待启动',
  recovering: '恢复中',
  blocked: '等待依赖',
  running: '运行中',
  stopping: '停止中',
  crashed: '已崩溃',
  failed: '启动失败',
};
export function usePluginList(poll = false) {
  return useQuery({
    queryKey: ['plugins'],
    queryFn: ({ signal }) =>
      request<Plugin[]>('/api/plugins', { signal, silentError: true }),
    staleTime: Infinity,
    refetchOnWindowFocus: poll ? 'always' : false,
    refetchInterval: (query) => {
      if (!poll) return false;
      const rows = query.state.data ?? [];
      if (
        rows.some(
          (p) =>
            p.activeTaskId ||
            [
              'waiting',
              'recovering',
              'starting',
              'stopping',
              'crashed',
            ].includes(p.runtimeStatus),
        )
      )
        return 3000;
      if (rows.some((p) => p.runtimeStatus === 'blocked')) return 10000;
      return query.state.error || rows.some((p) => p.enabled) ? 60000 : false;
    },
  });
}
export const pluginDetail = (id: string) =>
  request<Detail>(`/api/plugins/${encodeURIComponent(id)}`);
export const inspectPlugin = (file: File, signature?: File) => {
  const data = new FormData();
  data.append('file', file);
  if (signature) data.append('signature', signature);
  return request<Inspection>('/api/plugins/inspect', {
    method: 'POST',
    data,
    silentError: true,
  });
};
export const pluginAction = <T = { runtimeStatus: string }>(
  id: string,
  action: string,
  data?: unknown,
) =>
  request<T>(`/api/plugins/${encodeURIComponent(id)}/${action}`, {
    method: 'POST',
    data,
    silentError: true,
  });
export const savePluginPermissions = (
  id: string,
  disabledPermissions: string[],
) =>
  request<{ disabledPermissions: string[] }>(
    `/api/plugins/${encodeURIComponent(id)}/permissions`,
    {
      method: 'PUT',
      data: { disabledPermissions },
      silentError: true,
    },
  );
