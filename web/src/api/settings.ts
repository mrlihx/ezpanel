import { request } from './client';
export type MenuGroup = {
  id: string;
  title: string;
  icon: string;
  items: string[];
};
export type PanelSettings = {
  alias: string;
  idleMinutes: number;
  siteRoot: string;
  backupRoot: string;
  hiddenMenus: string[];
  menuOrder: string[];
  menuGroups: MenuGroup[];
  username: string;
  boundDomain: string;
  sslEnabled: boolean;
  certificate: string;
  privateKey?: string;
  certificateId: string;
  certificateInfo: {
    domains: string[];
    issuer: string;
    notAfter: string;
  } | null;
  keyConfigured: boolean;
  port: number;
  entry: string;
  publicUrl: string;
  userAgentEnabled: boolean;
  userAgentAllow: string;
  unauthStatus: number;
  unauthHtml: string;
};
export const panelSettings = () =>
  request<PanelSettings>('/api/settings', { silentError: true });
export const saveBasic = (
  data: Pick<
    PanelSettings,
    'alias' | 'idleMinutes' | 'siteRoot' | 'backupRoot'
  >,
) => request<PanelSettings>('/api/settings/basic', { method: 'PUT', data });
export const savePanelMenus = (data: {
  hiddenMenus?: string[];
  menuOrder?: string[];
  menuGroups?: MenuGroup[];
}) => request<PanelSettings>('/api/settings/menus', { method: 'PUT', data });
export const saveAccount = (data: {
  username: string;
  currentPassword?: string;
  newPassword?: string;
}) =>
  request<{ loginUrl: string }>('/api/settings/account', {
    method: 'PUT',
    data,
  });
export type NetworkSettings = Partial<
  Pick<PanelSettings, 'boundDomain' | 'sslEnabled' | 'certificate'>
> & {
  certificateId?: string;
  privateKey?: string;
  port?: number;
  entry?: string;
  userAgentEnabled?: boolean;
  userAgentAllow?: string;
  unauthStatus?: number;
  unauthHtml?: string;
};
export const saveNetwork = (data: NetworkSettings) =>
  request<{ loginUrl: string; loginRequired: boolean }>(
    '/api/settings/network',
    {
      method: 'PUT',
      data,
    },
  );

export type BackupChannel = {
  id: string;
  name: string;
  protocol: 'webdav' | 'ftp' | 'sftp' | `plugin:${string}`;
  endpoint: string;
  username: string;
  port?: number;
  directory?: string;
  enabled: boolean;
  provider?: string;
  unavailable?: boolean;
  fields?: Record<string, string>;
};
export const backupChannels = () =>
  request<BackupChannel[]>('/api/backups/channels');
export type BackupChannelInput = Omit<
  BackupChannel,
  'id' | 'provider' | 'unavailable'
> & {
  password?: string;
};
export type BackupChannelProvider = {
  protocol: `plugin:${string}`;
  name: string;
  fields: {
    key: string;
    label: string;
    type: 'text' | 'password' | 'number' | 'select';
    required?: boolean;
    options?: string[];
  }[];
};
export const backupChannelProviders = () =>
  request<BackupChannelProvider[]>('/api/backups/channels/providers');
export const saveBackupChannel = (
  id: string | undefined,
  data: BackupChannelInput,
) =>
  request<BackupChannel>(
    id ? `/api/backups/channels/${id}` : '/api/backups/channels',
    {
      method: id ? 'PUT' : 'POST',
      data,
    },
  );
export const deleteBackupChannel = (id: string) =>
  request(`/api/backups/channels/${id}`, { method: 'DELETE' });
export const testBackupChannel = (data: {
  id?: string;
  config?: BackupChannelInput;
}) => request('/api/backups/channels/test', { method: 'POST', data });
export const toggleBackupChannel = (id: string, enabled: boolean) =>
  request(`/api/backups/channels/${id}`, {
    method: 'PATCH',
    data: { enabled },
  });

export const terminalSettings = () =>
  request<{ fontSize: number }>('/api/settings/terminal');
export const saveTerminalSettings = (fontSize: number) =>
  request<{ fontSize: number }>('/api/settings/terminal', {
    method: 'PUT',
    data: { fontSize },
  });
