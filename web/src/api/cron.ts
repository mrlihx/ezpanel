import { request } from './client';
import { websites, type Website } from './website';
import type { Task } from './task';
export type CronType =
  | 'shell'
  | 'backup_site'
  | 'backup_db'
  | 'backup_directory'
  | 'url'
  | 'free_memory'
  | 'restart_project'
  | 'renew_certificate';
export type URLSettings = {
  url?: string;
  urlMethod?: 'GET' | 'POST';
  userAgent?: string;
  urlParams?: { name: string; value: string }[];
};
export type CronInput = URLSettings & {
  name: string;
  type: CronType;
  schedule: string;
  script?: string;
  targetId?: string;
  targetName?: string;
  allSites?: boolean;
  allDatabases?: boolean;
  keepCount?: number;
  timeoutSec?: number;
  renewBeforeDays?: number;
  directoryPath?: string;
  backupStorageId?: string;
  backupDirectory?: string;
  keepLocal?: boolean;
  excludeRules?: string;
};
export type Cron = CronInput & {
  id: string;
  scheduleDesc: string;
  timezone: string;
  target: string;
  backupPath: string;
  backupTarget: string;
  enabled: boolean;
  scheduleInvalid?: boolean;
  disabledReason: string;
  lastRunAt: string | null;
  lastStatus: string | null;
  createdAt: string;
};
export type Run = Task & { runId: string };
export const cronTypes = [
  { value: 'shell', label: 'Shell 脚本' },
  { value: 'backup_site', label: '备份网站' },
  { value: 'backup_db', label: '备份数据库' },
  { value: 'backup_directory', label: '备份目录' },
  { value: 'url', label: '访问 URL' },
  { value: 'free_memory', label: '释放内存' },
  { value: 'renew_certificate', label: '自动续签' },
  { value: 'restart_project', label: '重启项目' },
];
export const listCrons = (
  page: number,
  size: number,
  keyword: string,
  type?: string,
  status?: string,
) =>
  request<{ list: Cron[]; total: number }>('/api/crons', {
    params: { page, page_size: size, keyword, type, status },
  });
export const getCron = (id: string) => request<Cron>(`/api/crons/${id}`);
export const backupStorages = () =>
  request<{ id: string; name: string }[]>('/api/backups/storages');
export const saveCron = (id: string | undefined, data: CronInput) =>
  request<{ id: string }>(`/api/crons${id ? `/${id}` : ''}`, {
    method: id ? 'PUT' : 'POST',
    data,
  });
export async function cronSites(projects: boolean) {
  const out: Website[] = [];
  let page = 1;
  for (;;) {
    const result = await websites(page++, 100, '');
    out.push(
      ...result.list.filter((site) =>
        projects
          ? site.runtimeConfigured && !!site.runtimeEnvironment
          : site.type === 'static',
      ),
    );
    if ((page - 1) * 100 >= result.total) break;
  }
  return out;
}

export const testCronURL = (data: URLSettings) =>
  request<{
    statusCode: number;
    durationMs: number;
    body?: string;
    truncated?: boolean;
    error?: string;
  }>('/api/crons/test-url', { method: 'POST', data, timeout: 35000 });
