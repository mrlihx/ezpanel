import { request } from './client';

export type FRPRole = 'frps' | 'frpc';
export type FRPService = {
  role: FRPRole;
  installed: boolean;
  running: boolean;
  version: string;
  configPath: string;
  logPath: string;
};
export type FRPProxy = {
  originalName?: string;
  name: string;
  type: string;
  localIP: string;
  localPort: number;
  remotePort?: number;
  customDomains?: string[];
};
export type FRPValues = {
  bindAddr?: string;
  bindPort?: number;
  serverAddr?: string;
  serverPort?: number;
  user?: string;
  kcpBindPort?: number;
  quicBindPort?: number;
  vhostHTTPPort?: number;
  vhostHTTPSPort?: number;
  allowPorts?: { single?: number; start?: number; end?: number }[];
  auth?: { token?: string; method?: string; tokenSource?: unknown };
  transport?: { protocol?: string; tls?: { enable?: boolean } };
  webServer?: {
    addr?: string;
    port?: number;
    user?: string;
    password?: string;
  };
  log?: { level?: string; maxDays?: number };
  proxies?: FRPProxy[];
};
export type FRPConfig = {
  path: string;
  content: string;
  revision: string;
  values: FRPValues;
};
export const frpServices = () =>
  request<{ list: FRPService[] }>('/api/software/frp/services');
export const frpConfig = (role: FRPRole) =>
  request<FRPConfig>(`/api/software/frp/services/${role}/config`);
export const saveFRPConfig = (
  role: FRPRole,
  data: {
    revision: string;
    content?: string;
    values?: Record<string, unknown>;
  },
) =>
  request<{ mode: 'saved' | 'restart' | 'reload' }>(
    `/api/software/frp/services/${role}/config`,
    { method: 'PUT', data },
  );
export const frpAction = (role: FRPRole, action: string, version?: string) =>
  request<{ taskId: string } | { list: FRPService[] }>(
    `/api/software/frp/services/${role}/action`,
    {
      method: 'POST',
      data: { action, version },
    },
  );
