import { request } from './client';
import type { Application } from './software';
export type ProxyRoute = {
  name: string;
  path: string;
  target: string;
  host: string;
  enabled: boolean;
  webSocket?: boolean;
  cacheTTL?: number;
  headers?: {
    direction: 'request' | 'response';
    name: string;
    value: string;
  }[];
  rateLimit?: { requestsPerSecond: number; burst: number };
};
export type ProjectRuntime = 'nodejs' | 'python' | 'go';
export type RuntimeLaunch =
  'command' | 'uwsgi' | 'gunicorn' | 'script' | 'file';
export type Website = {
  backupCount: number;
  dailyTraffic: number | null;
  trafficNotice: string;
  logDirectory?: string;
  id: string;
  name: string;
  domains: string[];
  type: 'static' | 'proxy';
  projectType: 'php' | 'proxy' | ProjectRuntime;
  runtimeVersion?: string;
  runtimeEnvironment?: string;
  runtimeLaunch?: RuntimeLaunch | '';
  runtimeEntry?: string;
  runtimeCommand?: string;
  runtimePort?: number;
  runtimeWorkers?: number;
  runtimeThreads?: number;
  runtimeTimeout?: number;
  runtimeConfig?: { path: string; content: string };
  runtimeFile?: string;
  runtimeAutoStart?: boolean;
  runtimePackageManager?: string;
  runtimeScript?: string;
  runtimeEnv?: string;
  runtimeConfigured: boolean;
  runtimeRunning?: boolean;
  root: string;
  proxyRoutes: ProxyRoute[];
  firewallPorts?: number[];
  phpVersion: string;
  remark: string;
  enabled: boolean;
  effectiveRunning: boolean;
  sslEnabled: boolean;
  forceHttps: boolean;
  rewriteRule: string;
  redirectEnabled: boolean;
  redirectTarget: string;
  redirectCode: number;
  redirectKeepUri: boolean;
  sslExpireAt: string | null;
  canDeleteFiles: boolean;
  deleteFilesDisabledReason: string;
  createdAt: string;
  updatedAt: string;
};
export type SiteInput = Pick<
  Website,
  | 'name'
  | 'domains'
  | 'type'
  | 'projectType'
  | 'root'
  | 'proxyRoutes'
  | 'phpVersion'
  | 'remark'
  | 'runtimeVersion'
  | 'runtimeEnvironment'
  | 'runtimeLaunch'
  | 'runtimeEntry'
  | 'runtimeCommand'
  | 'runtimePort'
  | 'runtimeWorkers'
  | 'runtimeThreads'
  | 'runtimeTimeout'
  | 'runtimeFile'
  | 'runtimeAutoStart'
  | 'runtimePackageManager'
  | 'runtimeScript'
  | 'runtimeEnv'
>;
export const restartRuntime = (id: string) =>
  request(`/api/websites/${id}/runtime-restart`, { method: 'POST' });
export const projectService = (id: string, signal?: AbortSignal) =>
  request<{
    configuredPort: number;
    target: string;
    pending: boolean;
    container: {
      id: string;
      name: string;
      image: string;
      status: string;
      running: boolean;
      pid: number;
      startedAt: string;
      ports: Record<string, string> | null;
    };
  }>(`/api/websites/${id}/runtime-service`, { signal, silentError: true });
export const runtimeLogs = (id: string) =>
  request<{ content: string; running: boolean; failedAttempt: boolean }>(
    `/api/websites/${id}/runtime-logs`,
    { silentError: true },
  );
export const phpVersions = () =>
  request<{
    list: { version: string; installed: boolean; running: boolean }[];
  }>('/api/websites/php-versions', { silentError: true });
export type Environment = {
  application: Application;
  installed: boolean;
  version: string | null;
  running: boolean | null;
  manageable: boolean;
  activeTaskId: string | null;
};
export const environment = () =>
  request<Environment>('/api/websites/env', { silentError: true });
export const websites = (
  page: number,
  size: number,
  keyword: string,
  signal?: AbortSignal,
  projectType?: Website['projectType'],
) =>
  request<{ list: Website[]; total: number }>('/api/websites', {
    params: { page, page_size: size, keyword, projectType },
    signal,
    silentError: true,
  });
export const website = (id: string) =>
  request<Website>(`/api/websites/${encodeURIComponent(id)}`, {
    silentError: true,
  });
export const saveWebsite = (id: string | undefined, data: SiteInput) =>
  request<Website>(
    id ? `/api/websites/${encodeURIComponent(id)}` : '/api/websites',
    { method: id ? 'PUT' : 'POST', data, silentError: true },
  );
export const toggleWebsite = (id: string, enabled: boolean) =>
  request<Website>(
    `/api/websites/${encodeURIComponent(id)}/${enabled ? 'enable' : 'disable'}`,
    { method: 'POST' },
  );
export const deleteWebsite = (
  id: string,
  deleteFiles: boolean,
  confirmName: string,
) =>
  request<{ taskId?: string }>(`/api/websites/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    params: { deleteFiles, confirmName },
    silentError: true,
  });
export const saveSSL = (
  id: string,
  cert: string,
  key: string,
  forceHttps: boolean,
  certificateId?: string,
  allowDomainMismatch = false,
) =>
  request<Website>(`/api/websites/${encodeURIComponent(id)}/ssl`, {
    method: 'POST',
    data: { cert, key, forceHttps, certificateId, allowDomainMismatch },
    silentError: true,
  });
export const checkSSLCertificate = (id: string, certificateId: string) =>
  request<{ uncoveredDomains: string[] }>(
    `/api/websites/${encodeURIComponent(id)}/ssl/check`,
    { method: 'POST', data: { certificateId }, silentError: true },
  );
export const disableSSL = (id: string) =>
  request<Website>(`/api/websites/${encodeURIComponent(id)}/ssl`, {
    method: 'DELETE',
    silentError: true,
  });
export const siteConfig = (id: string) =>
  request<{ content: string }>(
    `/api/websites/${encodeURIComponent(id)}/config`,
    { silentError: true },
  );
export const reloadNginx = () =>
  request<{ running: boolean }>('/api/websites/nginx/reload', {
    method: 'POST',
  });
export const installNginx = () =>
  request<{ taskId: string }>('/api/software/install', {
    method: 'POST',
    data: { id: 'nginx' },
  });
export const nginxService = (action: string) =>
  request<{ running: boolean }>('/api/software/service', {
    method: 'POST',
    data: { id: 'nginx', action },
  });
export const siteCertificate = (id: string) =>
  request<{ certificate: string; privateKey: string }>(
    `/api/websites/${encodeURIComponent(id)}/ssl`,
    { silentError: true },
  );
export const fixWebsitePermissions = (id: string) =>
  request<{ owner: string; mode: string }>(
    `/api/websites/${encodeURIComponent(id)}/fix-permissions`,
    { method: 'POST' },
  );
export const saveRewrite = (id: string, rule: string) =>
  request<Website>(`/api/websites/${encodeURIComponent(id)}/rewrite`, {
    method: 'PUT',
    data: { rule },
    silentError: true,
  });
export const saveRedirect = (
  id: string,
  data: { enabled: boolean; target: string; code: number; keepUri: boolean },
) =>
  request<Website>(`/api/websites/${encodeURIComponent(id)}/redirect`, {
    method: 'PUT',
    data,
    silentError: true,
  });
export const siteLogs = (
  id: string,
  type: 'access' | 'error',
  lines: number,
  keyword?: string,
) =>
  request<{
    path: string;
    content: string;
    truncated: boolean;
  }>(`/api/websites/${encodeURIComponent(id)}/logs`, {
    params: { type, lines, keyword },
    silentError: true,
  });
export const clearSiteLog = (id: string, type: 'access' | 'error') =>
  request(`/api/websites/${encodeURIComponent(id)}/logs`, {
    method: 'DELETE',
    params: { type },
    silentError: true,
  });

type Visits = { day: string; requests: number; errors: number; bytes: number };
export const websiteStatistics = (
  id: string,
  days: number,
  signal?: AbortSignal,
) =>
  request<{
    total: Visits;
    uniqueIPs: number;
    days: Visits[];
    topPaths: { path: string; requests: number }[];
    partial: boolean;
  }>(`/api/websites/${id}/statistics`, {
    params: { days },
    signal,
    silentError: true,
  });

export const websiteColumns = () =>
  request<{ columns: string[] }>('/api/websites/columns', {
    silentError: true,
  });
export const saveWebsiteColumns = (columns: string[]) =>
  request<{ columns: string[] }>('/api/websites/columns', {
    method: 'PUT',
    data: { columns },
    silentError: true,
  });

export const projectRegistry = (id: string, signal?: AbortSignal) =>
  request<{ url: string }>(`/api/websites/${id}/runtime-registry`, {
    signal,
    silentError: true,
  });
export const saveProjectRegistry = (id: string, url: string) =>
  request<{ url: string }>(`/api/websites/${id}/runtime-registry`, {
    method: 'PUT',
    data: { url },
  });

export type PythonPackage = {
  name: string;
  version: string;
  requirement?: string;
};
export const pythonPackages = (id: string, signal?: AbortSignal) =>
  request<{ file: string; fileExists: boolean; list: PythonPackage[] }>(
    `/api/websites/${id}/python-packages`,
    { signal, silentError: true },
  );
export const savePythonRequirements = (id: string, file: string) =>
  request<{ file: string }>(`/api/websites/${id}/python-packages`, {
    method: 'PUT',
    data: { file },
  });
export const changePythonPackage = (
  id: string,
  data: { action: 'install' | 'uninstall'; name: string; version?: string },
) =>
  request<{ taskId: string }>(`/api/websites/${id}/python-packages`, {
    method: 'POST',
    data,
  });

export type NodeManifest = {
  exists: boolean;
  packageManager: string;
  scripts: string[];
  dependencies: Record<string, string> | null;
  devDependencies: Record<string, string> | null;
};
export const nodePackages = (id: string, signal?: AbortSignal) =>
  request<NodeManifest>(`/api/websites/${id}/node-packages`, {
    signal,
    silentError: true,
  });
export const changeNodePackage = (
  id: string,
  data: {
    action: 'install' | 'update' | 'uninstall' | 'sync';
    name: string;
    version?: string;
  },
) =>
  request<{ taskId: string }>(`/api/websites/${id}/node-packages`, {
    method: 'POST',
    data,
  });

export type RuntimeProcess = { pid: number; name: string };
export type RuntimeConnection = {
  fd: number;
  family: number;
  type: number;
  localaddr: { ip: string; port: number };
  remoteaddr: { ip: string; port: number };
  status: string;
};
export type RuntimeOpenFile = {
  path: string;
  fd: number;
  position: string;
  flags: string;
  mode: string;
};
export const runtimeLoad = (id: string, pid?: number, signal?: AbortSignal) =>
  request<{
    processes: RuntimeProcess[] | null;
    process:
      | (RuntimeProcess & {
          parent: number;
          user: string;
          status: string[] | null;
          started: number;
          threads: number;
          sockets: number;
          cpu: number;
          rss: number;
          read: number;
          write: number;
          command: string;
        })
      | null;
    connections: RuntimeConnection[] | null;
    connectionError?: string;
    openFiles: RuntimeOpenFile[] | null;
  }>(`/api/websites/${id}/runtime-load`, {
    params: { pid },
    signal,
    silentError: true,
  });

export type DefaultPageKind = 'index' | '404' | 'not_found' | 'stopped';
export type WebsiteAdvancedSettings = {
  pages: Record<DefaultPageKind, string>;
  defaultSiteId: string;
  tlsProtocols: string[];
  sites: {
    id: string;
    name: string;
    enabled: boolean;
    sslEnabled: boolean;
  }[];
};
export const websiteAdvanced = () =>
  request<WebsiteAdvancedSettings>('/api/websites/advanced', {
    silentError: true,
  });
export const saveWebsiteDefaultPage = (
  kind: DefaultPageKind,
  content: string,
  reset = false,
) =>
  request<{ kind: DefaultPageKind; content: string }>(
    `/api/websites/advanced/pages/${kind}`,
    {
      method: 'PUT',
      data: { content, reset },
    },
  );
export const saveWebsiteDefaultSite = (siteId: string) =>
  request<{ defaultSiteId: string }>('/api/websites/advanced/default-site', {
    method: 'PUT',
    data: { siteId },
  });
export const saveWebsiteTLSProtocols = (protocols: string[]) =>
  request<{ tlsProtocols: string[] }>('/api/websites/advanced/tls', {
    method: 'PUT',
    data: { protocols },
  });
