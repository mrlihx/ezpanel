import { request } from './client';
import type { Application } from './software';
export type Server = {
  local: boolean;
  containerClient: boolean;
  instanceId?: string;
  version?: string;
  running?: boolean;
  id: string;
  name: string;
  type: 'mysql' | 'mariadb';
  network: 'tcp' | 'unix';
  host: string;
  port: number;
  socketPath: string;
  username: string;
  hasPassword: boolean;
  caConfigured: boolean;
  tls: { mode: 'verify' | 'disabled'; serverName: string };
};
export type ServerInput = Omit<
  Server,
  | 'id'
  | 'local'
  | 'containerClient'
  | 'hasPassword'
  | 'caConfigured'
  | 'instanceId'
  | 'version'
  | 'running'
> & {
  serverId?: string;
  password?: string;
  tls: Server['tls'] & { caCert?: string };
};
export type Database = {
  createdAt?: string;
  username: string;
  userHost: string;
  localAccess?: boolean;
  hasPassword: boolean;
  backupCount: number;
  remark: string;
  name: string;
  charset: string;
  collation: string;
  size: number;
  tableCount: number;
  system: boolean;
};
export type DBUser = {
  username: string;
  host: string;
  grants: string[];
  editable: boolean;
};
export type Backup = {
  id: string;
  serverId: string | null;
  serverName: string;
  database: string;
  size: number;
  createdAt: string;
};
export type DBEnv = {
  application: Application;
  localType: 'mysql' | 'mariadb' | null;
  localInstalled: boolean;
  localRunning: boolean | null;
  clientInstalled: boolean;
  backupClientInstalled: boolean;
  socketPath: string | null;
  activeTaskId: string | null;
};
export const servers = () => request<Server[]>('/api/db/servers');
export const environment = () => request<DBEnv>('/api/db/env');
export const databaseList = (id: string) =>
  request<Database[]>(`/api/db/servers/${id}/databases`);
export const userList = (id: string) =>
  request<DBUser[]>(`/api/db/servers/${id}/users`);
export const charsetList = (id: string) =>
  request<{ list: { charset: string; collations: string[] }[] }>(
    `/api/db/servers/${id}/charsets`,
  );
export const saveServer = (id: string | undefined, data: ServerInput) =>
  request<{ id: string }>(`/api/db/servers${id ? `/${id}` : ''}`, {
    method: id ? 'PUT' : 'POST',
    data,
  });
export const testServer = (data: ServerInput) =>
  request<{ version: string }>('/api/db/servers/test', {
    method: 'POST',
    data,
  });
export const install = (id: string) =>
  request<{ taskId: string }>('/api/software/install', {
    method: 'POST',
    data: { id },
  });

const diagnosticsPath = (engine: string, id: string, view: string) =>
  `/api/db/instances/${engine}/${id}/diagnostics/${view}`;

export type DatabaseMetrics = {
  metrics?: Record<string, string>;
  variables?: Record<string, string>;
  sampledAt?: number;
  binlogFile?: string;
  binlogPosition?: string;
};

export type DatabaseTuning = {
  hostMemory: number;
  available: number;
  instanceLimit: number;
  profiles: {
    id: 'conservative' | 'balanced';
    values: Record<string, string>;
  }[];
};

export type SlowQueryAnalysis = {
  source: 'FILE' | 'TABLE' | 'NONE';
  truncated: boolean;
  samples: number;
  totalSeconds: number;
  groups: {
    id: string;
    statement: string;
    count: number;
    totalSeconds: number;
    maxSeconds: number;
    rowsExamined: number;
  }[];
};
export type BinaryLog = { name: string; size: number; updatedAt?: string };

export const databaseDiagnostics = <T>(
  engine: string,
  id: string,
  view: string,
  signal?: AbortSignal,
) =>
  request<T>(diagnosticsPath(engine, id, view), { signal, silentError: true });

export const purgeMySQLBinaryLog = (id: string, name: string) =>
  request(diagnosticsPath('mysql', id, 'binlog'), {
    method: 'DELETE',
    data: { name },
  });

export type ImportFile = {
  name: string;
  modTime: string;
  size: number;
  backupId?: string;
};

export const databaseImportPath = (serverId: string, database: string) =>
  `/api/db/servers/${encodeURIComponent(serverId)}/databases/${encodeURIComponent(database)}/import-files`;
