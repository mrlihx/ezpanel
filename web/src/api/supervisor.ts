import { request } from './client';

export type SupervisorProcess = {
  name: string;
  pid: number;
  state: string;
  description: string;
  stdout: string;
  stderr: string;
};

export type SupervisorProgram = {
  name: string;
  group: string;
  command: string;
  user: string;
  directory: string;
  numprocs: number;
  priority: number;
  autostart: boolean;
  autorestart: 'true' | 'false' | 'unexpected';
  remark: string;
  path: string;
  content: string;
  revision: string;
  editable: boolean;
  processes: SupervisorProcess[];
};

export type SupervisorValues = Pick<
  SupervisorProgram,
  | 'command'
  | 'user'
  | 'directory'
  | 'numprocs'
  | 'priority'
  | 'autostart'
  | 'autorestart'
  | 'remark'
>;

export const supervisorPrograms = (signal?: AbortSignal) =>
  request<{
    list: SupervisorProgram[];
    users: string[];
    warning: string;
    mainLog: string;
  }>('/api/software/supervisor/programs', { signal, silentError: true });

export const saveSupervisorProgram = (data: {
  name: string;
  revision?: string;
  values?: SupervisorValues;
  content?: string;
}) => request('/api/software/supervisor/programs', { method: 'PUT', data });

export const supervisorAction = (data: {
  name: string;
  action: 'start' | 'stop' | 'restart' | 'delete';
  revision?: string;
  removeLogs?: boolean;
}) =>
  request('/api/software/supervisor/programs/action', { method: 'POST', data });

export function supervisorLogURL(
  name?: string,
  process?: string,
  kind = 'stdout',
) {
  const params = new URLSearchParams({ kind });
  if (name) params.set('name', name);
  if (process) params.set('process', process);
  return `/api/software/supervisor/log?${params}`;
}
