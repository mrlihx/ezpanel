import { request } from './client';

export type NginxLoad = {
  pid: number;
  processes: number;
  residentBytes: number | null;
  cpuSeconds: number | null;
  uptimeSeconds: number | null;
  connections: number | null;
  listeners: number | null;
  notice: string;
};
export type NginxGuard = {
  enabled: boolean;
  policy: string;
  delay: string;
  restartSeconds: number;
  path: string;
};
export type NginxPerformanceValues = Partial<
  Record<
    | 'worker_processes'
    | 'worker_connections'
    | 'keepalive_timeout'
    | 'client_max_body_size'
    | 'gzip'
    | 'sendfile',
    string
  >
>;
export const nginxLoad = () =>
  request<NginxLoad>('/api/software/nginx/load', { silentError: true });
export const nginxGuard = () =>
  request<NginxGuard>('/api/software/nginx/guard', { silentError: true });
export const saveNginxGuard = (enabled: boolean, restartSeconds: number) =>
  request('/api/software/nginx/guard', {
    method: 'PUT',
    data: { enabled, restartSeconds },
  });
export const nginxPerformance = () =>
  request<{
    values: NginxPerformanceValues;
    revision: string;
    path: string;
  }>('/api/software/nginx/performance', { silentError: true });
export const saveNginxPerformance = (
  values: NginxPerformanceValues,
  revision: string,
) =>
  request('/api/software/nginx/performance', {
    method: 'PUT',
    data: { values, revision },
  });
export const nginxErrorLog = () =>
  request<{
    files: { path: string; content: string; notice: string }[];
    notice: string;
  }>('/api/software/nginx/error-log', { silentError: true });
