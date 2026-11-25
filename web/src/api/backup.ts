import { request } from './client';
export type BackupScope =
  | { kind: 'site'; websiteId: string }
  | { kind: 'database'; serverId?: string; database?: string }
  | { kind: 'mongodb'; connectionId: string; database: string };
export type BackupFile = {
  id: string;
  fileName: string;
  storageType: 'local' | 'remote';
  localCopy?: boolean;
  size: number;
  createdAt: string;
  method: 'manual' | 'automatic';
};
export const listBackups = (scope: BackupScope, page: number, size: number) => {
  const { kind, ...filters } = scope;
  return request<{ list: BackupFile[]; total: number }>(
    `/api/backups/${kind}`,
    { params: { ...filters, page, page_size: size } },
  );
};
export const deleteBackupFiles = (kind: BackupScope['kind'], ids: string[]) =>
  request<{ deletedIds: string[]; failed: { id: string; message: string }[] }>(
    `/api/backups/${kind}/delete`,
    { method: 'POST', data: { ids } },
  );
export const manualBackup = (scope: BackupScope) =>
  scope.kind === 'site'
    ? request<{ taskId: string }>(
        `/api/backups/site/${scope.websiteId}/create`,
        { method: 'POST' },
      )
    : scope.kind === 'mongodb'
      ? request<{ taskId: string }>(
          `/api/db/nosql/mongodb/connections/${scope.connectionId}/batch`,
          {
            method: 'POST',
            data: { action: 'backup', databases: [scope.database] },
          },
        )
      : request<{ taskId: string }>(
          `/api/db/servers/${scope.serverId}/backups`,
          {
            method: 'POST',
            data: { database: scope.database },
          },
        );
