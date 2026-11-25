import { request } from './client';

export type WAFEngineStatus = {
  openRestyInstalled: boolean;
  openRestyRunning: boolean;
  engineMounted: boolean;
  configVersion: string;
  logPath: string;
};

export type WAFGlobalConfig = {
  enabled: boolean;
  mode: 'block' | 'observe';
  ccEnabled: boolean;
  ccWindowSeconds: number;
  ccMaxRequests: number;
  ccAction: 'block' | 'observe' | 'challenge';
  autoBanEnabled: boolean;
  autoBanWindow: number;
  autoBanThreshold: number;
  autoBanDuration: number;
  syncFirewall: boolean;
  retentionDays: number;
  allowIPs: string[];
  denyIPs: string[];
  allowPaths: string[];
  blockHtml: string;
  disabledRuleGroups: string[];
};

export type WAFOverview = {
  enabled: boolean;
  mode: 'block' | 'observe';
  engine: WAFEngineStatus;
  todayBlocked: number;
  todayObserved: number;
  weekBlocked: number;
  activeBans: number;
  totalSites: number;
  protectedSites: number;
  trend: { ts: number; blocked: number; observed: number }[];
  ruleGroups: { group: string; label: string; count: number }[];
  topSites: { siteId: string; name: string; count: number }[];
  topIPs: { ip: string; location: string; count: number }[];
};

export type WAFBuiltinRule = {
  id: string;
  group: string;
  groupLabel: string;
  name: string;
  description: string;
  target: string;
  pattern: string;
  enabled: boolean;
  action: 'block' | 'observe';
};

export type WAFCustomRule = {
  id: string;
  name: string;
  siteId: string;
  enabled: boolean;
  priority: number;
  action: 'block' | 'observe' | 'allow';
  matchField:
    'uri' | 'query' | 'args' | 'user_agent' | 'method' | 'ip' | 'header';
  matchOperator: 'contains' | 'prefix' | 'exact' | 'regex';
  matchValue: string;
  headerName: string;
  remark: string;
  createdAt?: string;
  updatedAt?: string;
};

export type WAFSitePolicy = {
  siteId: string;
  inheritGlobal: boolean;
  mode: 'off' | 'observe' | 'block';
  ccEnabled: boolean;
  ccWindowSeconds: number;
  ccMaxRequests: number;
  ccAction: 'block' | 'observe' | 'challenge';
  allowIPs: string[];
  denyIPs: string[];
  allowPaths: string[];
  denyPaths: string[];
  disabledGroups: string[];
};

export type WAFSiteRow = {
  siteId: string;
  name: string;
  domains: string[];
  siteEnabled: boolean;
  todayBlocked: number;
  policy: WAFSitePolicy;
};

export type WAFBan = {
  id: number;
  ip: string;
  siteId: string;
  location: string;
  reason: string;
  source: 'auto' | 'manual';
  nftSynced: boolean;
  expiresAt: number;
  createdAt: number;
};

export type WAFEvent = {
  id: number;
  occurredAt: number;
  siteId: string;
  domain: string;
  clientIP: string;
  location: string;
  method: string;
  uri: string;
  queryString: string;
  userAgent: string;
  ruleGroup: string;
  ruleId: string;
  ruleName: string;
  action: 'block' | 'observe' | 'challenge';
  matchedData: string;
  statusCode: number;
};

export type WAFExemption = {
  id: string;
  siteId: string;
  ruleId: string;
  uri: string;
  remark: string;
  createdAt: string;
};

export const getWAFOverview = (range: '24h' | '7d' = '24h') =>
  request<WAFOverview>('/api/waf/overview', { params: { range } });

export const getWAFConfig = () =>
  request<{ config: WAFGlobalConfig; engine: WAFEngineStatus }>(
    '/api/waf/config',
  );

export const saveWAFConfig = (data: WAFGlobalConfig) =>
  request<{ config: WAFGlobalConfig; engine: WAFEngineStatus }>(
    '/api/waf/config',
    { method: 'PUT', data },
  );

export const syncWAFEngine = () =>
  request<WAFEngineStatus>('/api/waf/engine/sync', { method: 'POST' });

export const listWAFRules = () =>
  request<{ builtin: WAFBuiltinRule[]; custom: WAFCustomRule[] }>(
    '/api/waf/rules',
  );

export const updateWAFBuiltinRule = (
  id: string,
  data: { enabled: boolean; action: 'block' | 'observe' },
) => request(`/api/waf/rules/builtin/${id}`, { method: 'PUT', data });

export const createWAFCustomRule = (
  data: Omit<WAFCustomRule, 'id' | 'createdAt' | 'updatedAt'>,
) => request<WAFCustomRule>('/api/waf/rules/custom', { method: 'POST', data });

export const updateWAFCustomRule = (
  id: string,
  data: Omit<WAFCustomRule, 'id' | 'createdAt' | 'updatedAt'>,
) =>
  request<WAFCustomRule>(`/api/waf/rules/custom/${id}`, {
    method: 'PUT',
    data,
  });

export const deleteWAFCustomRule = (id: string) =>
  request(`/api/waf/rules/custom/${id}`, { method: 'DELETE' });

export const listWAFSites = () =>
  request<{ list: WAFSiteRow[] }>('/api/waf/sites');

export const getWAFSitePolicy = (siteId: string) =>
  request<WAFSitePolicy>(`/api/waf/sites/${siteId}`);

export const updateWAFSitePolicy = (siteId: string, data: WAFSitePolicy) =>
  request<WAFSitePolicy>(`/api/waf/sites/${siteId}`, { method: 'PUT', data });

export const listWAFBans = (ip = '') =>
  request<{ list: WAFBan[] }>('/api/waf/bans', {
    params: ip ? { ip } : undefined,
  });

export const createWAFBan = (data: {
  ip: string;
  siteId?: string;
  reason?: string;
  duration: number;
  syncFirewall?: boolean;
}) => request<WAFBan>('/api/waf/bans', { method: 'POST', data });

export const deleteWAFBan = (id: number) =>
  request(`/api/waf/bans/${id}`, { method: 'DELETE' });

export const syncWAFBanFirewall = (id: number) =>
  request<WAFBan>(`/api/waf/bans/${id}/firewall`, { method: 'POST' });

export const listWAFEvents = (params: {
  page?: number;
  pageSize?: number;
  siteId?: string;
  ip?: string;
  ruleGroup?: string;
  action?: string;
  keyword?: string;
}) =>
  request<{ list: WAFEvent[]; total: number; page: number; pageSize: number }>(
    '/api/waf/events',
    { params },
  );

export const clearWAFEvents = () =>
  request('/api/waf/events', { method: 'DELETE' });

export const allowWAFEvent = (id: number) =>
  request<WAFExemption>(`/api/waf/events/${id}/allow`, { method: 'POST' });

export const listWAFExemptions = () =>
  request<{ list: WAFExemption[] }>('/api/waf/exemptions');

export const deleteWAFExemption = (id: string) =>
  request(`/api/waf/exemptions/${id}`, { method: 'DELETE' });
