import { request } from './client';
import type { DatabaseInstance } from './software';

export type PGDatabase = {
  name: string;
  owner: string;
  username?: string;
  hasPassword?: boolean;
  remark?: string;
  encoding: string;
  sizeBytes: number;
  backupCount?: number;
};

export type PGBackup = {
  fileName: string;
  size: number;
  createdAt: string;
  path: string;
};

export type PGUser = {
  name: string;
  superuser: boolean;
  createdb: boolean;
  canLogin: boolean;
};

export type PGTable = {
  schema: string;
  name: string;
  type: 'table' | 'view';
  rowEstimate: number;
  sizeBytes: number;
};

export type PGColumn = {
  name: string;
  type: string;
  nullable: boolean;
  primaryKey: boolean;
};

export type PGRowsResult = {
  columns: PGColumn[];
  rows: Record<string, unknown>[];
  total: number;
  page: number;
  pageSize: number;
};

export type PGQueryResult = {
  columns: string[];
  rows: Record<string, unknown>[];
  message: string;
};

export const pgInstances = (signal?: AbortSignal) =>
  request<{ list: DatabaseInstance[] }>('/api/db/postgresql/instances', {
    signal,
  });

export const pgDatabases = (instance: string, signal?: AbortSignal) =>
  request<{ list: PGDatabase[] }>(
    `/api/db/postgresql/${encodeURIComponent(instance)}/databases`,
    { signal },
  );

export const pgCreateDatabase = (
  instance: string,
  data: {
    name: string;
    username?: string;
    password?: string;
    encoding?: string;
    reuseRole?: boolean;
  },
) =>
  request(`/api/db/postgresql/${encodeURIComponent(instance)}/databases`, {
    method: 'POST',
    data,
  });

export const pgDropDatabase = (
  instance: string,
  name: string,
  silentError = false,
) =>
  request(
    `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(name)}`,
    { method: 'DELETE', silentError },
  );

export const pgUsers = (instance: string, signal?: AbortSignal) =>
  request<{ list: PGUser[] }>(
    `/api/db/postgresql/${encodeURIComponent(instance)}/users`,
    { signal },
  );

export const pgSetUserPassword = (
  instance: string,
  name: string,
  password: string,
) =>
  request(
    `/api/db/postgresql/${encodeURIComponent(instance)}/users/${encodeURIComponent(name)}/password`,
    { method: 'PUT', data: { password } },
  );

export const pgTables = (
  instance: string,
  dbName: string,
  signal?: AbortSignal,
) =>
  request<{ list: PGTable[] }>(
    `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(dbName)}/tables`,
    { signal },
  );

export const pgTableRows = (
  instance: string,
  dbName: string,
  schema: string,
  table: string,
  page: number,
  pageSize: number,
  signal?: AbortSignal,
) =>
  request<PGRowsResult>(
    `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(dbName)}/tables/${encodeURIComponent(schema)}/${encodeURIComponent(table)}/rows`,
    { params: { page, pageSize }, signal },
  );

export const pgQuery = (instance: string, dbName: string, sql: string) =>
  request<PGQueryResult>(
    `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(dbName)}/query`,
    { method: 'POST', data: { sql } },
  );

export const pgBackups = (
  instance: string,
  dbName: string,
  signal?: AbortSignal,
) =>
  request<{ list: PGBackup[] }>(
    `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(dbName)}/backups`,
    { signal },
  );

export const pgCreateBackup = (
  instance: string,
  dbName: string,
  silentError = false,
) =>
  request<PGBackup>(
    `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(dbName)}/backups`,
    { method: 'POST', silentError },
  );

export const pgDeleteBackup = (
  instance: string,
  dbName: string,
  filename: string,
) =>
  request(
    `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(dbName)}/backups/${encodeURIComponent(filename)}`,
    { method: 'DELETE' },
  );

export const pgDownloadBackupUrl = (
  instance: string,
  dbName: string,
  filename: string,
) =>
  `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(dbName)}/backups/${encodeURIComponent(filename)}/download`;

export const pgRevealPasswordUrl = (instance: string, dbName: string) =>
  `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(dbName)}/password/reveal`;

export const pgSaveDatabaseRemark = (
  instance: string,
  dbName: string,
  remark: string,
) =>
  request(
    `/api/db/postgresql/${encodeURIComponent(instance)}/databases/${encodeURIComponent(dbName)}/metadata`,
    { method: 'PUT', data: { remark } },
  );

export type PGExtension = {
  name: string;
  defaultVersion: string;
  installedVersion: string;
  comment: string;
};

export const pgExtensions = (
  instance: string,
  db = 'postgres',
  signal?: AbortSignal,
) =>
  request<{ list: PGExtension[]; databases: string[]; database: string }>(
    `/api/db/postgresql/${encodeURIComponent(instance)}/extensions`,
    { params: { db }, signal },
  );

export const pgInstallExtension = (
  instance: string,
  db: string,
  name: string,
) =>
  request(`/api/db/postgresql/${encodeURIComponent(instance)}/extensions`, {
    method: 'POST',
    data: { db, name },
  });

export const pgUninstallExtension = (
  instance: string,
  db: string,
  name: string,
) =>
  request(
    `/api/db/postgresql/${encodeURIComponent(instance)}/extensions/${encodeURIComponent(name)}`,
    { method: 'DELETE', params: { db } },
  );
