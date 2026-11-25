import { request } from './client';
export type FirewallRule = {
  location?: string;
  id: string;
  kind: 'port' | 'ip' | 'forward' | 'region';
  protocol: 'tcp' | 'udp' | 'both';
  ports: string;
  peer: string;
  policy: 'allow' | 'deny';
  direction: 'in' | 'out' | 'both';
  targetIP: string;
  targetPort: number;
  country: string;
  remark: string;
  enabled: boolean;
  createdAt: string;
  expiresAt: string | null;
  readOnly?: boolean;
  origin?: 'builtin' | 'website' | 'manual' | 'imported' | 'external';
  detail?: string;
};
export type FirewallConfig = {
  enabled: boolean;
  blockPing: boolean;
  scanProtection: boolean;
  trustedIP: string;
  rules: FirewallRule[];
};
export type FirewallState = {
  config: FirewallConfig;
  rules: FirewallRule[];
  inventoryNotice: string;
  active: boolean;
  available: boolean;
  panelPort: number;
  sshPorts: number[];
  listeners: {
    protocol: string;
    port: number;
    address: string;
    pid: number;
    command: string;
  }[];
  listenersKnown: boolean;
  externalFirewalls: string[];
};
export const firewall = () =>
  request<FirewallState>('/api/security/firewall', { silentError: true });
export const saveFirewall = ({
  enabled,
  blockPing,
  scanProtection,
}: Pick<FirewallConfig, 'enabled' | 'blockPing' | 'scanProtection'>) =>
  request('/api/security/firewall', {
    method: 'PUT',
    data: { enabled, blockPing, scanProtection },
  });
export const saveFirewallRule = (
  data: Omit<FirewallRule, 'id' | 'createdAt'>,
  id?: string,
) => {
  const {
    kind,
    protocol,
    ports,
    peer,
    policy,
    direction,
    targetIP,
    targetPort,
    country,
    remark,
    enabled,
    expiresAt,
  } = data;
  return request(`/api/security/firewall/rules${id ? `/${id}` : ''}`, {
    method: id ? 'PUT' : 'POST',
    data: {
      kind,
      protocol,
      ports,
      peer,
      policy,
      direction,
      targetIP,
      targetPort,
      country,
      remark,
      enabled,
      expiresAt,
    },
  });
};
export const deleteFirewallRule = (id: string) =>
  request(`/api/security/firewall/rules/${id}`, { method: 'DELETE' });
export const importFirewallRules = (
  kind: FirewallRule['kind'],
  rules: FirewallRule[],
) =>
  request('/api/security/firewall/import', {
    method: 'POST',
    data: { kind, rules },
  });
export type SSHState = {
  values: Record<string, string>;
  revision: string;
  unit: string;
  running: boolean;
  socketActivated: boolean;
  timezone: string;
};
export const sshState = () =>
  request<SSHState>('/api/security/ssh', { silentError: true });
export const saveSSH = (data: {
  password: boolean;
  pubkey: boolean;
  port: number;
  rootLogin: string;
  revision: string;
}) =>
  request<{ keyGenerated: boolean }>('/api/security/ssh', {
    method: 'PUT',
    data,
  });
export const sshService = (enabled: boolean) =>
  request('/api/security/ssh/service', { method: 'POST', data: { enabled } });
export type SSHUser = {
  name: string;
  uid: number;
  home: string;
  shell: string;
  system: boolean;
};
export const sshUsers = () =>
  request<{ list: SSHUser[] }>('/api/security/ssh/users', {
    silentError: true,
  });
export const createSSHUser = (data: {
  name: string;
  password: string;
  shell: string;
}) => request('/api/security/ssh/users', { method: 'POST', data });
export const deleteSSHUser = (
  name: string,
  confirmName: string,
  removeHome: boolean,
) =>
  request(`/api/security/ssh/users/${encodeURIComponent(name)}`, {
    method: 'DELETE',
    data: { confirmName, removeHome },
  });
export const changeSSHPassword = (name: string, password: string) =>
  request(`/api/security/ssh/users/${encodeURIComponent(name)}/password`, {
    method: 'PUT',
    data: { password },
  });
export type SSHKeys = { privateKey: string };
export const sshKeys = (name: string) =>
  request<SSHKeys>(`/api/security/ssh/users/${encodeURIComponent(name)}/keys`, {
    silentError: true,
  });
export const generateSSHKey = (name: string, regenerate = false) =>
  request<SSHKeys>(`/api/security/ssh/users/${encodeURIComponent(name)}/keys`, {
    method: 'POST',
    data: { regenerate },
  });

export const batchFirewallRules = (ids: string[], action: string) =>
  request('/api/security/firewall/rules/batch', {
    method: 'POST',
    data: { ids, action },
  });

export const updateFirewallIPPools = () =>
  request<{ count: number }>('/api/security/firewall/ip-pools/update', {
    method: 'POST',
    data: {},
  });
