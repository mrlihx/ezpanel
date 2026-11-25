import { request } from './client';
import type { Container } from './docker';
import { downloadLogFile } from './oplog';

export type ComposeStatus = 'running' | 'partial' | 'stopped' | 'error';
export type ComposeSource = 'managed' | 'external';

export type ComposeProject = {
  id: string;
  name: string;
  source: ComposeSource;
  workDir: string;
  composeFile: string;
  missingComposeFile: boolean;
  composeFiles: string[];
  envFile: string;
  remark: string;
  templateId?: string;
  createdAt?: string;
  updatedAt?: string;
  status: ComposeStatus;
  running: number;
  total: number;
  registered: boolean;
  canRemoveFiles: boolean;
};

export type ComposeContainer = Pick<
  Container,
  'Id' | 'Names' | 'Image' | 'State' | 'Status' | 'Ports'
> & {
  Labels?: Record<string, string>;
};

export type ComposeProjectDetail = ComposeProject & {
  yaml: string;
  env: string;
  containers: ComposeContainer[];
  fileError?: string;
};

export type ComposeProjectInput = {
  name: string;
  source?: ComposeSource;
  workDir?: string;
  composeFile?: string;
  yaml?: string;
  env?: string;
  remark?: string;
  templateId?: string;
  deploy?: boolean;
  pull?: boolean;
  removeOrphans?: boolean;
  forceRecreate?: boolean;
};

export type ComposeProjectAction =
  'up' | 'start' | 'stop' | 'restart' | 'down' | 'pull' | 'rebuild' | 'pull-up';

export type ComposeDeleteOptions = {
  removeContainers: boolean;
  removeVolumes: boolean;
  removeFiles: boolean;
  allowMissingConfig?: boolean;
  confirmName?: string;
};

export type ComposeRevision = {
  id: string;
  createdAt: string;
  createdBy: string;
};

const projectsPath = '/api/docker/compose/projects';
const projectPath = (name: string) =>
  `${projectsPath}/${encodeURIComponent(name)}`;
const revisionPath = (name: string, id: string) =>
  `${projectPath(name)}/revisions/${encodeURIComponent(id)}`;

export const listComposeProjects = (signal?: AbortSignal) =>
  request<ComposeProject[]>(projectsPath, { signal, silentError: true });

export const composeProject = (
  name: string,
  signal?: AbortSignal,
  revealEnv = false,
) =>
  request<ComposeProjectDetail>(projectPath(name), {
    params: { revealEnv },
    signal,
    silentError: !revealEnv,
  });

export const createComposeProject = (data: ComposeProjectInput) =>
  request<{ taskId: string }>(projectsPath, { method: 'POST', data });

export const validateComposeProject = (data: ComposeProjectInput) =>
  request<{ valid: boolean; previewOnly: boolean }>(
    `${projectsPath}/validate`,
    {
      method: 'POST',
      data,
    },
  );

export const saveComposeFiles = (
  name: string,
  data: { yaml?: string; env?: string; apply?: boolean },
) =>
  request<{ taskId: string }>(`${projectPath(name)}/files`, {
    method: 'PUT',
    data,
  });

export const composeProjectAction = (
  name: string,
  data: {
    action: ComposeProjectAction;
    removeVolumes?: boolean;
    removeImages?: boolean;
  },
) =>
  request<{ taskId: string }>(`${projectPath(name)}/action`, {
    method: 'POST',
    data,
  });

export const deleteComposeProject = (
  name: string,
  params: ComposeDeleteOptions,
) =>
  request<{ taskId: string }>(projectPath(name), {
    method: 'DELETE',
    params,
  });

type ComposeLogFilters = {
  service?: string;
  tail: number;
  since?: string;
  timestamps: boolean;
  keyword?: string;
};

export type ComposeLogView = {
  text: string;
  truncated: boolean;
  unavailable: number;
  interrupted?: number;
};

export function composeProjectLogStreamURL(
  name: string,
  filters: ComposeLogFilters,
) {
  const params = new URLSearchParams({
    tail: String(filters.tail),
    timestamps: String(filters.timestamps),
  });
  if (filters.service) params.set('service', filters.service);
  if (filters.since) params.set('since', filters.since);
  if (filters.keyword) params.set('keyword', filters.keyword);
  return `${projectPath(name)}/logs/stream?${params}`;
}

export const composeProjectLogs = (
  name: string,
  params: ComposeLogFilters,
  signal?: AbortSignal,
) =>
  request<ComposeLogView>(`${projectPath(name)}/logs`, {
    params,
    signal,
    silentError: true,
  });

export const downloadComposeProjectLogs = (
  name: string,
  { service, tail, since, timestamps, keyword }: ComposeLogFilters,
) =>
  downloadLogFile(
    `${projectPath(name)}/logs`,
    `${name}${service ? `-${service}` : ''}.log`,
    {
      service,
      tail: String(tail),
      since,
      timestamps: String(timestamps),
      keyword,
      download: 'true',
    },
  );

export const composeRevisions = (name: string, signal?: AbortSignal) =>
  request<ComposeRevision[]>(`${projectPath(name)}/revisions`, {
    signal,
    silentError: true,
  });

export const composeRevision = (
  name: string,
  id: string,
  signal?: AbortSignal,
) =>
  request<ComposeRevision & { yaml: string; env: string }>(
    revisionPath(name, id),
    { signal, silentError: true },
  );

export const rollbackComposeRevision = (
  name: string,
  id: string,
  apply: boolean,
) =>
  request<{ taskId: string }>(`${revisionPath(name, id)}/rollback`, {
    method: 'POST',
    data: { apply },
  });
