import { request } from './client';
export type Container = {
  Id: string;
  Names: string[];
  Labels?: Record<string, string>;
  Image: string;
  ImageID: string;
  State: string;
  Status: string;
  SizeRw?: number;
  Mounts?: {
    Type: string;
    Name?: string;
    Source: string;
    Destination: string;
  }[];
  Created: number;
  Ports: {
    IP?: string;
    PrivatePort: number;
    PublicPort?: number;
    Type: string;
  }[];
  NetworkSettings: { Networks: Record<string, { IPAddress: string }> };
};
export type Image = {
  Id: string;
  RepoTags: string[] | null;
  Size: number;
  Created: number;
  Containers: number;
};
export type Network = {
  Id: string;
  Name: string;
  Driver: string;
  Scope: string;
  Created?: string;
  IPAM: { Config?: { Subnet?: string; Gateway?: string }[] | null };
  Containers?: Record<string, { Name: string; IPv4Address: string }>;
};
export type Volume = {
  Name: string;
  Driver: string;
  Mountpoint: string;
  CreatedAt?: string;
  Labels: Record<string, string> | null;
  UsageData?: { Size: number; RefCount: number };
};
export type Registry = {
  id: string;
  name: string;
  address: string;
  username: string;
  remark: string;
  hasPassword: boolean;
  builtin: boolean;
};
export type Overview = {
  installed: boolean;
  running: boolean;
  notice?: string;
  info?: {
    Name: string;
    NCPU: number;
    MemTotal: number;
    DockerRootDir: string;
    Driver: string;
    Containers: number;
    ContainersRunning: number;
    ContainersPaused: number;
    ContainersStopped: number;
    Images: number;
    ServerVersion: string;
    OperatingSystem: string;
    Architecture: string;
  };
  version?: { Version: string; ApiVersion: string; MinAPIVersion: string };
  usage?: {
    LayersSize: number;
    Images: Image[];
    Volumes: Volume[];
    Containers: Container[];
  };
};
export const dockerOverview = () =>
  request<Overview>('/api/docker/overview', { silentError: true });
export const containers = () =>
  request<Container[]>('/api/docker/containers', { silentError: true });
export const images = () =>
  request<Image[]>('/api/docker/images', { silentError: true });
export const networks = () =>
  request<Network[]>('/api/docker/networks', { silentError: true });
export const volumes = () =>
  request<{ Volumes: Volume[] | null }>('/api/docker/volumes', {
    silentError: true,
  });
export const registries = () =>
  request<Registry[]>('/api/docker/registries', { silentError: true });

export type ComposeTemplateCategory =
  'database' | 'website' | 'ai' | 'tool' | 'custom';
export type ComposeTemplate = {
  id: string;
  name: string;
  remark: string;
  category: ComposeTemplateCategory;
  envKeys?: string[];
};
export type ComposeTemplateInput = Omit<ComposeTemplate, 'id'> & {
  yaml: string;
};
export type ComposeTemplateFormat = 'json' | 'yaml';
const composeTemplatePath = '/api/docker/compose/templates';
export const composeTemplates = () =>
  request<ComposeTemplate[]>(composeTemplatePath, { silentError: true });
export const composeTemplate = (id: string) =>
  request<ComposeTemplate & { yaml: string; variables: string[] }>(
    `${composeTemplatePath}/${id}`,
  );
export const saveComposeTemplate = (data: ComposeTemplateInput, id?: string) =>
  request<ComposeTemplate>(
    id ? `${composeTemplatePath}/${id}` : composeTemplatePath,
    {
      method: id ? 'PUT' : 'POST',
      data,
    },
  );
export const deleteComposeTemplate = (id: string) =>
  request(`${composeTemplatePath}/${id}`, { method: 'DELETE' });
export const importComposeTemplate = (data: {
  format: ComposeTemplateFormat;
  name: string;
  content: string;
}) =>
  request<ComposeTemplate>(`${composeTemplatePath}/import`, {
    method: 'POST',
    data,
  });
export const exportComposeTemplate = (
  id: string,
  format: ComposeTemplateFormat,
) =>
  request<{ filename: string; content: string }>(
    `${composeTemplatePath}/${id}/export`,
    { params: { format } },
  );
export const saveComposeProjectTemplate = (
  projectName: string,
  data: Pick<ComposeTemplate, 'name' | 'remark' | 'category'>,
) =>
  request<ComposeTemplate>(`${composeTemplatePath}/from-project`, {
    method: 'POST',
    data: { projectName, ...data },
  });

export const searchImages = (term: string) =>
  request<
    {
      name: string;
      description: string;
      star_count: number;
      is_official: boolean;
    }[]
  >('/api/docker/images/search', { params: { term }, silentError: true });
export const pullImage = (image: string, registryId?: string) =>
  request<{ taskId: string }>('/api/docker/images/pull', {
    method: 'POST',
    data: { image, registryId },
  });
export const pushImage = (image: string, registryId?: string) =>
  request<{ taskId: string }>('/api/docker/images/push', {
    method: 'POST',
    data: { image, registryId },
  });

type CPUStats = {
  cpu_usage: { total_usage: number; percpu_usage?: number[] };
  system_cpu_usage?: number;
  online_cpus?: number;
};
export type DockerStats = {
  cpu_stats: CPUStats;
  precpu_stats: CPUStats;
  memory_stats: {
    usage: number;
    limit: number;
    stats?: {
      inactive_file?: number;
      total_inactive_file?: number;
      cache?: number;
      file?: number;
    };
  };
  pids_stats?: { current?: number };
  networks?: Record<string, { rx_bytes: number; tx_bytes: number }>;
  blkio_stats?: {
    io_service_bytes_recursive?: { op: string; value: number }[] | null;
  };
};

type DockerPortBinding = { HostIp: string; HostPort: string };
export type ContainerInspection = {
  Id: string;
  Name: string;
  Image: string;
  Created: string;
  State: {
    Status: string;
    Running: boolean;
    Paused: boolean;
    StartedAt: string;
    FinishedAt: string;
    Error?: string;
  };
  Config: {
    Image: string;
    Cmd?: string[] | null;
    Entrypoint?: string[] | null;
    Env?: string[] | null;
    Labels?: Record<string, string> | null;
  };
  HostConfig: {
    NetworkMode: string;
    PortBindings?: Record<string, DockerPortBinding[] | null> | null;
    PublishAllPorts: boolean;
    RestartPolicy: { Name: string; MaximumRetryCount: number };
    Memory: number;
    NanoCpus: number;
    CpuPeriod?: number;
    CpuQuota?: number;
    VolumesFrom?: string[] | null;
    AutoRemove: boolean;
  };
  Mounts?: {
    Name?: string;
    Type: string;
    Source: string;
    Destination: string;
    RW: boolean;
  }[];
  NetworkSettings: {
    Ports?: Record<string, DockerPortBinding[] | null>;
    Networks: Record<
      string,
      {
        NetworkID: string;
        IPAddress: string;
        GlobalIPv6Address: string;
        Gateway: string;
        IPv6Gateway: string;
        MacAddress: string;
      }
    >;
  };
};

export const inspectContainer = (id: string, signal?: AbortSignal) =>
  request<ContainerInspection>(
    `/api/docker/containers/${encodeURIComponent(id)}`,
    {
      signal,
      silentError: true,
    },
  );
