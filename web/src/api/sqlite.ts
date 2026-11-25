import { request } from './client';

export type SQLiteFile = {
  id: string;
  name: string;
  path: string;
  addedAt: string;
  size: number;
  exists: boolean;
  updatedAt?: string;
  backupCount?: number;
};

export type SQLiteBackup = {
  fileName: string;
  size: number;
  createdAt: string;
  path: string;
};

export type SQLiteTable = {
  name: string;
  type: 'table' | 'view';
  rowCount: number;
  sql: string;
};

export type SQLiteColumn = {
  name: string;
  type: string;
  notNull: boolean;
  primaryKey: boolean;
  defaultValue: string;
};

export type SQLiteRow = { key: string; values: Record<string, unknown> };

export type SQLiteRowsResult = {
  columns: SQLiteColumn[];
  hasPK: boolean;
  rows: SQLiteRow[];
  total: number;
  page: number;
  pageSize: number;
};

export type SQLiteQueryResult = {
  columns: string[];
  rows: Record<string, unknown>[];
  affected?: number;
  message: string;
};

export const sqliteFiles = (signal?: AbortSignal) =>
  request<{ list: SQLiteFile[] }>('/api/db/sqlite/files', { signal });

export const sqliteAddFile = (data: { name?: string; path: string }) =>
  request<SQLiteFile>('/api/db/sqlite/files', { method: 'POST', data });

export const sqliteRemoveFile = (id: string) =>
  request(`/api/db/sqlite/files/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });

export const sqliteTables = (id: string, signal?: AbortSignal) =>
  request<{ list: SQLiteTable[] }>(
    `/api/db/sqlite/files/${encodeURIComponent(id)}/tables`,
    { signal },
  );

export const sqliteTableRows = (
  id: string,
  table: string,
  page: number,
  pageSize: number,
  signal?: AbortSignal,
) =>
  request<SQLiteRowsResult>(
    `/api/db/sqlite/files/${encodeURIComponent(id)}/tables/${encodeURIComponent(table)}/rows`,
    { params: { page, pageSize }, signal },
  );

export const sqliteInsertRow = (
  id: string,
  table: string,
  values: Record<string, unknown>,
) =>
  request(
    `/api/db/sqlite/files/${encodeURIComponent(id)}/tables/${encodeURIComponent(table)}/rows`,
    { method: 'POST', data: { values } },
  );

export const sqliteUpdateRow = (
  id: string,
  table: string,
  key: string,
  values: Record<string, unknown>,
) =>
  request(
    `/api/db/sqlite/files/${encodeURIComponent(id)}/tables/${encodeURIComponent(table)}/rows`,
    { method: 'PUT', data: { key, values } },
  );

export const sqliteDeleteRow = (id: string, table: string, key: string) =>
  request(
    `/api/db/sqlite/files/${encodeURIComponent(id)}/tables/${encodeURIComponent(table)}/rows`,
    { method: 'DELETE', data: { key } },
  );

export const sqliteQuery = (id: string, sql: string) =>
  request<SQLiteQueryResult>(
    `/api/db/sqlite/files/${encodeURIComponent(id)}/query`,
    { method: 'POST', data: { sql } },
  );

export const sqliteBackups = (id: string, signal?: AbortSignal) =>
  request<{ list: SQLiteBackup[] }>(
    `/api/db/sqlite/files/${encodeURIComponent(id)}/backups`,
    { signal },
  );

export const sqliteCreateBackup = (id: string) =>
  request<SQLiteBackup>(
    `/api/db/sqlite/files/${encodeURIComponent(id)}/backups`,
    { method: 'POST' },
  );

export const sqliteDeleteBackup = (id: string, filename: string) =>
  request(
    `/api/db/sqlite/files/${encodeURIComponent(id)}/backups/${encodeURIComponent(filename)}`,
    { method: 'DELETE' },
  );

export const sqliteDownloadBackupUrl = (id: string, filename: string) =>
  `/api/db/sqlite/files/${encodeURIComponent(id)}/backups/${encodeURIComponent(filename)}/download`;
