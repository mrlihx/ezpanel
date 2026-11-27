import { request } from './client';

export type RsyncSender = {
  id: string;
  name: string;
  source: string;
  destination: string;
  username?: string;
  password?: string;
  ruleId?: string;
  mode: 'incremental' | 'full';
  bandwidthKb: number;
  schedule: 'realtime' | 'interval';
  checkMinutes: number;
};

export type RsyncRule = {
  id: string;
  name: string;
  patterns: string;
};

export type RsyncWorkspace = {
  revision: string;
  senders: RsyncSender[];
  rules: RsyncRule[];
};

export const rsyncWorkspaceQuery = {
  queryKey: ['rsync-workspace'],
  queryFn: () => request<RsyncWorkspace>('/api/software/rsync/workspace'),
};

export const saveRsyncWorkspace = (data: RsyncWorkspace) =>
  request<RsyncWorkspace>('/api/software/rsync/workspace', {
    method: 'PUT',
    data,
  });

export const runRsyncSender = (id: string) =>
  request<{ taskId: string }>(`/api/software/rsync/send/${id}/run`, {
    method: 'POST',
  });

export type RsyncReceiver = {
  name: string;
  path: string;
  username: string;
  password?: string;
  enabled: boolean;
  allow: string;
};
