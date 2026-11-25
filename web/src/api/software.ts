import { request } from './client';

export type Application = {
  installDir?: string;
  dataDir?: string;
  initialPasswordPath?: string;
  id: string;
  name: string;
  category: string;
  description: string;
  installed: boolean;
  running: boolean | null;
  version: string | null;
  installedVersions?: string[];
  instanceCount: number;
  runningInstances?: number;
  pkgManager: string;
  manageable: boolean;
  installable: boolean;
  uninstallable: boolean;
  usedBySites?: string[];
  reason: string;
  updateable?: boolean;
  updateVersion?: string;
  updateNotice?: string;
};
export type Catalog = {
  list: Application[];
  projectInstanceCounts: Record<string, number>;
  projectRunningCounts: Record<string, number>;
  activeTasks: {
    taskId: string;
    resourceId: string;
    target: string;
    action: string;
    status: string;
  }[];
  activeTask: {
    taskId: string;
    target: string;
    action: string;
    status: string;
  } | null;
};
export const catalog = (signal?: AbortSignal) =>
  request<Catalog>('/api/software/catalog', { signal, silentError: true });
export type SourceSelection = { source: string; mirror?: string };
export const applicationSources = (id: string) =>
  request<{
    list: { value: string; label: string }[];
    current: SourceSelection;
    container?: boolean;
    mirrors?: string[];
  }>(`/api/software/${id}/sources`, { silentError: true });
export const installApplication = (
  id: string,
  version?: string,
  selection?: SourceSelection,
  instance?: { newInstance: boolean; instanceName?: string },
) =>
  request<{ taskId: string }>('/api/software/install', {
    method: 'POST',
    data: { id, version, ...selection, ...instance },
  });
export const uninstallApplication = (
  id: string,
  confirmName: string,
  environmentId?: string,
) =>
  request<{ taskId: string }>('/api/software/uninstall', {
    method: 'POST',
    data: { id, confirmName, environmentId },
  });
export type RuntimeEnvironment = {
  id: string;
  version: string;
  projects: { id: string; name: string }[];
};
export const runtimeEnvironments = (id: string, signal?: AbortSignal) =>
  request<{ list: RuntimeEnvironment[] }>(`/api/software/${id}/environments`, {
    signal,
    silentError: true,
  });
export function runtimeEnvironmentLabel(environment: RuntimeEnvironment) {
  return environment.id === environment.version
    ? `${environment.version}（原环境）`
    : `${environment.version} · ${environment.id.slice(0, 6)}`;
}
export type DatabaseInstance = {
  id: string;
  name: string;
  version: string;
  updateVersion?: string;
  port: number;
  pendingPort?: number;
  remoteAccess?: boolean;
  pendingRemoteAccess?: boolean;
  running: boolean;
  dataDir: string;
  errorLogPath?: string;
  configPath?: string;
  containerHost: string;
  containerPort: number;
  networkReady: boolean;
};
export const databaseInstances = (id: string, signal?: AbortSignal) =>
  request<{ list: DatabaseInstance[] }>(`/api/software/${id}/instances`, {
    signal,
    silentError: true,
  });
export const databaseInstance = (
  engine: string,
  instance: string,
  signal?: AbortSignal,
) =>
  request<DatabaseInstance>(`/api/software/${engine}/instances/${instance}`, {
    signal,
    silentError: true,
  });
export const databaseInstanceAction = (
  id: string,
  instance: string,
  action: 'start' | 'stop' | 'restart' | 'remove',
) =>
  request(`/api/software/${id}/instances/${instance}/action`, {
    method: 'POST',
    data: { action },
  });
export const databaseInstancePassword = (id: string, instance: string) =>
  request<{ password: string }>(
    `/api/software/${id}/instances/${instance}/password`,
  );

export const applicationService = (
  id: string,
  action: 'start' | 'stop' | 'restart' | 'reload',
) =>
  request<{ running: boolean }>('/api/software/service', {
    method: 'POST',
    data: { id, action },
  });

export type Release = {
  version: string;
  source: string;
  method: 'compile' | 'binary' | 'docker' | 'package';
  platform?: string;
  unavailableReason?: string;
};
export const applicationVersions = (
  id: string,
  refresh = false,
  selection?: SourceSelection,
  signal?: AbortSignal,
) =>
  request<{ list: Release[]; syncedAt: string; notice: string }>(
    `/api/software/${id}/versions`,
    {
      params: { refresh, ...selection },
      signal,
      silentError: true,
    },
  );
export type ApplicationSettings = {
  path: string;
  content: string;
  revision: string;
  missing?: boolean;
};
export const databaseInstanceSettings = (
  engine: string,
  instance: string,
  signal?: AbortSignal,
) =>
  request<ApplicationSettings>(
    `/api/software/${engine}/instances/${instance}/settings`,
    { signal, silentError: true },
  );
export const saveDatabaseInstanceSettings = (
  engine: string,
  instance: string,
  content: string,
  revision: string,
) =>
  request<ApplicationSettings>(
    `/api/software/${engine}/instances/${instance}/settings`,
    { method: 'PUT', data: { content, revision } },
  );
export const applicationSettings = (id: string, kind?: string) =>
  request<ApplicationSettings>(`/api/software/${id}/settings`, {
    params: { kind },
    silentError: true,
  });
export const saveApplicationSettings = (
  id: string,
  content: string,
  revision: string,
  kind?: string,
) =>
  request(`/api/software/${id}/settings`, {
    method: 'PUT',
    params: { kind },
    data: { content, revision },
  });
export const scanClamAV = () =>
  request<{ taskId: string }>('/api/software/clamav/scan', { method: 'POST' });
export const applicationLogs = (id: string) =>
  request<{ content: string }>(`/api/software/${id}/logs`, {
    silentError: true,
  });

export const updateApplication = (
  id: string,
  version: string,
  selection?: SourceSelection,
  instanceId?: string,
) =>
  request<{ taskId: string }>('/api/software/update', {
    method: 'POST',
    data: { id, version, instanceId, ...selection },
  });
export const checkApplicationUpdates = (category?: string) =>
  request<{ list: { id: string; notice: string }[] }>(
    '/api/software/check-updates',
    { method: 'POST', params: category ? { category } : undefined },
  );

export const switchApplication = (
  id: string,
  version: string,
  selection: SourceSelection,
) =>
  request<{ taskId: string }>('/api/software/switch', {
    method: 'POST',
    data: { id, version, ...selection },
  });

export type ApplicationGuard = {
  enabled: boolean;
  policy: string;
  restartSeconds: number;
};
export const applicationGuard = (id: string) =>
  request<ApplicationGuard>(`/api/software/${id}/guard`, { silentError: true });
export const saveApplicationGuard = (
  id: string,
  enabled: boolean,
  restartSeconds: number,
) =>
  request(`/api/software/${id}/guard`, {
    method: 'PUT',
    data: { enabled, restartSeconds },
  });

const instancePath = (engine: string, id: string) =>
  `/api/software/${engine}/instances/${id}`;

export const saveDatabaseInstancePort = (
  engine: string,
  id: string,
  port: number,
) =>
  request(instancePath(engine, id) + '/port', {
    method: 'PUT',
    data: { port },
  });

export const saveDatabaseInstanceRemoteAccess = (
  engine: string,
  id: string,
  enabled: boolean,
) =>
  request<DatabaseInstance>(instancePath(engine, id) + '/remote-access', {
    method: 'PUT',
    data: { enabled },
  });

export type DatabaseInstanceOptions = {
  revision: string;
  values: Record<string, string>;
};

export const databaseInstanceOptions = (
  engine: string,
  id: string,
  signal?: AbortSignal,
) =>
  request<DatabaseInstanceOptions>(instancePath(engine, id) + '/options', {
    signal,
    silentError: true,
  });

export const saveDatabaseInstanceOptions = (
  engine: string,
  id: string,
  revision: string,
  values: Record<string, string>,
) =>
  request<DatabaseInstanceOptions>(instancePath(engine, id) + '/options', {
    method: 'PUT',
    data: { revision, values },
  });

export const databaseInstanceLog = (
  engine: string,
  id: string,
  signal?: AbortSignal,
) =>
  request<import('./oplog').TextLog>(instancePath(engine, id) + '/log', {
    signal,
    silentError: true,
  });

export const clearMySQLInstanceErrorLog = (id: string) =>
  request(instancePath('mysql', id) + '/log', { method: 'DELETE' });
