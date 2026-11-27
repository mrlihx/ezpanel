import { request } from './client';
import type { OpLog } from './oplog';
export type DNSProvider = {
  id: string;
  name: string;
  primary: string;
  secondary: string;
};
export type DNSResult = DNSProvider & {
  latency: number | null;
  resolveMs: number | null;
  success: number;
  total: number;
  errors: string[];
};
export type MirrorProvider = {
  id: string;
  name: string;
  url: string;
  pip: string;
};
export type MirrorResult = {
  id: string;
  name: string;
  url: string;
  reachable: boolean;
  complete: boolean;
  latency: number | null;
  speed: number | null;
  error: string;
};
export type SavedCommand = { name: string; command: string };
export type IPFamilySettings = {
  enabled?: boolean;
  dhcp: boolean;
  addresses: string[];
  gateway: string;
  acceptRA: boolean | null;
};
export type IPSettingsData = {
  supported: boolean;
  list?: {
    id: string;
    name: string;
    current: string[];
    ipv4: IPFamilySettings;
    ipv6: IPFamilySettings;
  }[];
  trial?: { deadline: string } | null;
};
export type TimeSyncResult = {
  synchronized: boolean;
  service: string;
  message: string;
};
export type ToolboxData = {
  ip: IPSettingsData;
  disks: { list: DiskDevice[] };
  dns: { servers: string[]; content: string; providers: DNSProvider[] };
  swap: {
    total: number;
    used: number;
    free: number;
    ownMB: number;
    entries: string;
    path: string;
  };
  time: {
    now: string;
    zone: string;
    zones: string[];
    synchronized: boolean;
    service: string;
    serviceActive: boolean;
  };
  password: { users: string[] };
  ramdisk: { path: string; mounted: boolean; sizeMB: number; memory: number };
  hosts: { content: string; revision: string; path: string };
  mirrors: {
    os: string;
    codename: string;
    supported: boolean;
    files: { path: string; content: string }[];
    pip: string;
    systemSources: string[];
    systemProvider?: string;
    pipIndex: string;
    providers: MirrorProvider[];
  };
  commands: { list: SavedCommand[] };
  audit: {
    list: OpLog[];
    total: number;
    installed: boolean;
    enabled: boolean;
    rules: string;
    content: string;
  };
};
export const toolboxData = <K extends keyof ToolboxData>(
  section: K,
  page = 1,
) =>
  request<ToolboxData[K]>(`/api/toolbox/${section}`, {
    params: { page, page_size: 20 },
    silentError: true,
  });
export const saveToolbox = (section: keyof ToolboxData, data: unknown) =>
  request(`/api/toolbox/${section}`, { method: 'PUT', data });
export const tryIPSettings = (
  data: IPFamilySettings & { interface: string; family: 4 | 6 },
) =>
  request<{ deadline: string }>('/api/toolbox/ip', {
    method: 'PUT',
    data,
  });
export const finishIPSettings = (action: 'confirm' | 'cancel') =>
  request(`/api/toolbox/ip/${action}`, { method: 'POST' });
export const testDNS = (data: { servers?: string[]; recommend?: boolean }) =>
  request<{ list: DNSResult[] }>('/api/toolbox/dns/test', {
    method: 'POST',
    data,
  });
export const testMirrors = (kind: string) =>
  request<{ list: MirrorResult[]; notice: string }>(
    '/api/toolbox/mirrors/test',
    { method: 'POST', data: { kind } },
  );

export type DiskDevice = {
  path: string;
  model: string;
  size: number;
  type: string;
  fstype: string;
  uuid: string;
  mountpoints: string[] | null;
  system: boolean;
  mountable: boolean;
};
