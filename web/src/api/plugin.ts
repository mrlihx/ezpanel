import { useQuery } from '@tanstack/react-query';
import { request } from './client';
export type PluginEnv = {
  key: string;
  label: string;
  default: string;
  required: boolean;
  secret: boolean;
};
export type Manifest = {
  id: string;
  name: string;
  version: string;
  description: string;
  author: string;
  type: 'static' | 'process';
  ui: string;
  menu?: { title: string; icon: string; order: number };
  permissions: string[];
  execAllow?: string[];
  fsRoots?: string[];
  env?: PluginEnv[];
};
export type Plugin = Pick<
  Manifest,
  'id' | 'name' | 'version' | 'description' | 'type' | 'menu'
> & {
  enabled: boolean;
  runtimeStatus: string;
  installedAt: string;
  activeTaskId: string;
};
export type Detail = {
  manifest: Manifest;
  config: Record<string, { value?: string; isSet?: boolean }>;
  enabled: boolean;
  runtimeStatus: string;
};
export type Inspection = {
  uploadId: string;
  expiresAt: string;
  manifest: Manifest;
  permissionChanges: {
    permissions: string[];
    execAllow: string[];
    fsRoots: string[];
  };
};
export function usePluginList(poll = false) {
  return useQuery({
    queryKey: ['plugins'],
    queryFn: ({ signal }) =>
      request<Plugin[]>('/api/plugins', { signal, silentError: true }),
    staleTime: Infinity,
    refetchInterval: (query) => {
      if (!poll || query.state.error) return false;
      const rows = query.state.data ?? [];
      if (
        rows.some(
          (p) =>
            p.runtimeStatus === 'starting' || p.runtimeStatus === 'stopping',
        )
      )
        return 3000;
      return rows.some((p) => p.runtimeStatus === 'running') ? 60000 : false;
    },
  });
}
export const pluginDetail = (id: string) =>
  request<Detail>(`/api/plugins/${encodeURIComponent(id)}`);
export const inspectPlugin = (file: File) => {
  const data = new FormData();
  data.append('file', file);
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
