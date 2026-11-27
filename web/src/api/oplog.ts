import { request } from './client';
export type OpLog = {
  id: number;
  username: string | null;
  ip: string;
  location?: string;
  module: string;
  action: string;
  target: string;
  taskId: string | null;
  phase: string;
  success: boolean;
  errorCode: number;
  errorMsg: string;
  userAgent: string;
  resourceId: string;
  createdAt: string;
};
export type Filters = {
  kind?: 'operation' | 'login';
  resourceId?: string;
  module?: string;
  success?: string;
  keyword?: string;
  startTime?: string;
  endTime?: string;
};
export const listLogs = (page: number, size: number, filters: Filters) =>
  request<{ list: OpLog[]; total: number }>('/api/oplogs', {
    params: { page, page_size: size, ...filters },
  });
export const clearLogs = (beforeDays: number, filters: Filters = {}) =>
  request<{ deletedCount: number }>('/api/oplogs/clear', {
    method: 'POST',
    data: { beforeDays },
    params: filters,
  });

export type TextLog = {
  path: string;
  content: string;
  size: number;
  truncated: boolean;
  clearable: boolean;
};
export type LogSource = {
  id: string;
  name: string;
  path: string;
  clearable: boolean;
};
export type SSHEntry = {
  id: number;
  ip: string;
  address: string;
  location: string;
  username: string;
  success: boolean;
  createdAt: string;
};
export const runtimeLog = () => request<TextLog>('/api/logs/runtime');
export const applicationLogTargets = (id: string) =>
  request<{ id: string; name: string }[]>(`/api/logs/apps/${id}/targets`);
export const applicationSources = (id: string, target?: string) =>
  request<LogSource[]>(`/api/logs/apps/${id}`, { params: { target } });
export const applicationLog = (id: string, source: string, target?: string) =>
  request<TextLog>(`/api/logs/apps/${id}/${source}`, { params: { target } });
export const sshLogs = () =>
  request<{ list: SSHEntry[]; truncated: boolean; scope: string }>(
    '/api/logs/ssh',
  );
import { fetchDownload, saveBlob } from '../utils/download';

export async function downloadLogFile(
  url: string,
  name: string,
  filters: Record<string, string | undefined> = {},
) {
  const params = new URLSearchParams();
  Object.entries(filters).forEach(([key, value]) => {
    if (value !== undefined) params.set(key, value);
  });
  const query = params.toString();
  await fetchDownload(query ? `${url}?${query}` : url, name, '日志导出失败');
}
export function saveLogFile(content: Blob | string, name: string) {
  saveBlob(content, name);
}
