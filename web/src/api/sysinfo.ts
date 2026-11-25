import { request } from './client';
export type NetworkPoint = {
  name: string;
  up: number | null;
  down: number | null;
  sent: number;
  recv: number;
};
export type DiskIOPoint = {
  readLatency: number | null;
  writeLatency: number | null;
  name: string;
  read: number | null;
  write: number | null;
  readOps: number | null;
  writeOps: number | null;
  latency: number | null;
};
export type Point = {
  diskReadLatency: number | null;
  diskWriteLatency: number | null;
  diskIO: DiskIOPoint[];
  diskReadOps: number | null;
  diskWriteOps: number | null;
  diskLatency: number | null;
  ts: string;
  cpu: number | null;
  memPercent: number | null;
  load1: number | null;
  netUp: number | null;
  netDown: number | null;
  netSent: number | null;
  netRecv: number | null;
  networks: NetworkPoint[];
  diskRead: number | null;
  diskWrite: number | null;
};
export type Disk = {
  mount: string;
  device: string;
  fstype: string;
  total: number;
  used: number;
  free: number;
  usedPercent: number;
  inodesTotal: number;
  inodesUsed: number;
  inodesFree: number;
  inodesUsedPercent: number;
};
export type Overview = {
  hostname: string;
  os: string;
  platform: string;
  platformName: string;
  platformVersion: string;
  release: string;
  kernel: string;
  arch: string;
  uptime: number | null;
  bootTime: string | null;
  ips: string[];
  panelVersion: string;
  cpu: {
    model: string;
    cores: number | null;
    physicalCores: number | null;
    frequencyMHz: number;
    usage: number | null;
    perCore: { name: string; usage: number }[] | null;
    breakdown: Record<string, number> | null;
  };
  load: { l1: number; l5: number; l15: number } | null;
  mem: {
    total: number;
    used: number;
    free: number;
    available: number;
    shared: number;
    cached: number;
    usedPercent: number;
    swapTotal: number | null;
    swapUsed: number | null;
  } | null;
  disks: Disk[];
};
export type SummaryCount = { value: number | null; error?: string };
export type Summary = {
  websites: SummaryCount;
  databases: SummaryCount;
  docker: SummaryCount;
  crons: SummaryCount;
};
export type Process = {
  pid: number;
  ppid: number;
  name: string;
  user: string;
  cpu: number | null;
  mem: number | null;
  memoryBytes: number | null;
  status: string[] | null;
  started: number;
  protected: boolean;
  protectedReason: string;
};
export type ProcessDetail = Process & {
  exe: string;
  command: string;
  cwd: string;
  startedAt: string | null;
  threads: number | null;
};
export const overview = (signal?: AbortSignal) =>
  request<Overview>('/api/sysinfo/overview', { signal, silentError: true });
export const historyPoints = (
  range: { start: number; end: number },
  signal?: AbortSignal,
) =>
  request<Point[]>('/api/sysinfo/history', {
    params: range,
    signal,
    silentError: true,
  });
export const restartSystem = (target: 'server' | 'panel') =>
  request<null>('/api/sysinfo/restart', { method: 'POST', data: { target } });
export const summary = (signal?: AbortSignal) =>
  request<Summary>('/api/sysinfo/summary', { signal, silentError: true });
export const processes = (
  sort: 'cpu' | 'mem',
  keyword: string,
  signal?: AbortSignal,
  limit = 100,
) =>
  request<Process[]>('/api/sysinfo/processes', {
    params: { sort, limit, keyword: keyword || undefined },
    signal,
    silentError: true,
  });
export const processDetail = (
  pid: number,
  started: number,
  signal?: AbortSignal,
) =>
  request<ProcessDetail>(`/api/sysinfo/processes/${pid}`, {
    params: { started },
    signal,
    silentError: true,
  });
export const terminateProcess = (pid: number, started: number) =>
  request<{ message: string }>(`/api/sysinfo/processes/${pid}/terminate`, {
    method: 'POST',
    data: { started },
  });

export type NetworkProcess = {
  pid: number;
  started: number;
  name: string;
  listenIPs: string[];
  ports: string[];
  ipCount: number;
  connectionCount: number;
  upload: number | null;
  download: number | null;
  rateNote: string;
};
export type NetworkConnection = {
  protocol: string;
  localIP: string;
  localPort: number;
  remoteIP: string;
  remotePort: number;
  state: string;
  upload: number | null;
  download: number | null;
};
export type NetworkProcessList = {
  list: NetworkProcess[];
  total: number;
  sampledAt: string;
  notice: string;
};
export type NetworkProcessDetail = {
  process: NetworkProcess;
  connections: NetworkConnection[];
  total: number;
  sampledAt: string;
  notice: string;
};
export const networkProcesses = (
  keyword: string,
  page: number,
  pageSize: number,
  signal?: AbortSignal,
) =>
  request<NetworkProcessList>('/api/sysinfo/network', {
    params: { keyword: keyword || undefined, page, page_size: pageSize },
    signal,
    silentError: true,
  });
export const networkProcessDetail = (
  pid: number,
  started: number,
  page: number,
  pageSize: number,
  signal?: AbortSignal,
) =>
  request<NetworkProcessDetail>(`/api/sysinfo/network/${pid}`, {
    params: { started, page, page_size: pageSize },
    signal,
    silentError: true,
  });

export type MonitorProcessFilter = { sort: 'cpu' | 'mem'; keyword: string };
export type MonitorNetworkFilter = {
  keyword: string;
  page: number;
  pageSize: number;
  pid: number;
  started: number;
  detailPage: number;
  detailPageSize: number;
};
export type MonitorNetworkFeed = {
  filter: MonitorNetworkFilter;
  list?: NetworkProcessList;
  detail?: NetworkProcessDetail;
  error?: string;
  detailError?: string;
};
export type MonitorFrame = {
  point?: Point;
  overview?: Overview;
  top?: Record<MonitorProcessFilter['sort'], Process[]>;
  processes?: { filter: MonitorProcessFilter; list: Process[] };
  network?: MonitorNetworkFeed;
};

export type PanelStatus = {
  panelVersion: string;
  publicIp: string;
  repoUrl: string;
  latestVersion: string;
  latestTag: string;
  hasUpdate: boolean;
  releaseUrl: string;
  releaseNotes?: string;
  publishedAt?: string;
  checkedAt?: string;
  lastUpdateError?: string;
};

export const panelStatus = (signal?: AbortSignal) =>
  request<PanelStatus>('/api/sysinfo/panel-status', {
    signal,
    silentError: true,
  });

export const checkPanelUpdate = () =>
  request<PanelStatus>('/api/sysinfo/panel-update/check', {
    method: 'POST',
  });

export const applyPanelUpdate = (tag?: string) =>
  request<{ updating: boolean; targetVersion: string; targetTag: string }>(
    '/api/sysinfo/panel-update/apply',
    {
      method: 'POST',
      data: tag ? { tag } : {},
      timeout: 360000,
    },
  );

export const dismissPanelUpdateError = () =>
  request<null>('/api/sysinfo/panel-update/error', {
    method: 'DELETE',
    silentError: true,
  });
