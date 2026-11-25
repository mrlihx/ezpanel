import { request } from './client';
import type { ApplicationSettings } from './software';
export type PHPConfig = ApplicationSettings & {
  values: Record<string, string>;
};
export type PHPConfigKind = 'ini' | 'fpm';
export const phpConfig = (id: string, kind: PHPConfigKind) =>
  request<PHPConfig>(`/api/software/${id}/php/config/${kind}`, {
    silentError: true,
  });
export const savePHPConfig = (
  id: string,
  kind: PHPConfigKind,
  revision: string,
  change: { content: string } | { values: Record<string, string> },
) =>
  request(`/api/software/${id}/php/config/${kind}`, {
    method: 'PUT',
    data: { revision, ...change },
  });
export type PHPExtension = {
  module: string;
  source: string;
  name: string;
  version: string;
  builtin: boolean;
  managed: boolean;
};
export type PHPExtensionOption = {
  module: string;
  source: string;
  notice?: string;
  disabled: boolean;
  name: string;
  category: string;
  description: string;
};
export const phpExtensions = (id: string) =>
  request<{
    latestTaskId: string;
    installed: PHPExtension[];
    catalog: PHPExtensionOption[];
  }>(`/api/software/${id}/php/extensions`, { silentError: true });
export const changePHPExtension = (
  id: string,
  name: string,
  action: 'install' | 'uninstall',
  version = '',
  acceptedLicense = false,
) =>
  request<{ taskId: string }>(`/api/software/${id}/php/extensions`, {
    method: 'POST',
    data: { name, version, action, acceptedLicense },
  });
export const phpLoad = (id: string) =>
  request<{ enabled: boolean; values?: Record<string, string | number> }>(
    `/api/software/${id}/php/load`,
    { silentError: true },
  );
export const phpInfo = (id: string) =>
  request<{ content: string }>(`/api/software/${id}/php/info`, {
    silentError: true,
  });
export type PHPCLIVersion = {
  id: string;
  branch: string;
  version: string;
  command: string;
  binPath: string;
  running: boolean;
};
export type PHPCLIState = {
  defaultId: string;
  defaultBin: string;
  installed: PHPCLIVersion[];
};
export const phpCLI = () =>
  request<PHPCLIState>('/api/software/php/cli', { silentError: true });
export const setDefaultPHPCLI = (id: string) =>
  request<PHPCLIState>('/api/software/php/cli', {
    method: 'PUT',
    data: { id },
  });
