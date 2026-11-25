import { useNavigate } from 'react-router-dom';
import { useState, useEffect, useMemo, useRef } from 'react';
import { Alert, App, Form, Input, theme, type MenuProps, Grid } from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams, useSearchParams } from 'react-router-dom';
import { request } from '../../api/client';
import { applicationGuard } from '../../api/software';
import {
  containers,
  dockerOverview,
  images,
  networks,
  volumes,
  registries,
  searchImages,
  pullImage,
  pushImage,
  type Container,
} from '../../api/docker';
import { type Operation } from './DockerOperationForm';
import { listTasks } from '../../api/task';
import { formatSize } from '../../utils/format';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import { containerOwnership } from './containerOwnership';
export default function useDockerPage() {
  const navigate = useNavigate();
  const fixedActions = Grid.useBreakpoint().lg ? ('right' as const) : undefined;
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const { token } = theme.useToken();
  const [imageTask, setImageTask] = useState<'load' | 'build'>();
  const [params] = useSearchParams();
  const { section, view, resource, detailTab } = useParams();
  const legacy = params.get('tab');
  const legacyImage = legacy === 'online' ? 'search' : params.get('image');
  const route = useRoutedTab(
    'docker',
    '/docker',
    section,
    [
      'overview',
      'containers',
      'images',
      'networks',
      'volumes',
      'registries',
      'compose',
      'settings',
    ],
    {
      keys: !section ? ['tab', 'image'] : ['tab'],
      tab: legacy === 'online' ? 'images' : legacy,
      suffix:
        (legacy === 'images' || legacy === 'online') &&
        legacyImage &&
        ['local', 'search'].includes(legacyImage)
          ? `/${legacyImage}`
          : undefined,
    },
  );
  const tab = route.tab;
  const imageRoute = useRoutedTab(
    'dockerImages',
    '/docker/images',
    section === 'images' ? view : undefined,
    ['local', 'search'],
    { keys: ['image'], tab: params.get('image') },
    section !== 'images' || route.loading,
  );
  const imageView = imageRoute.tab;
  const settingsRoute = useRoutedTab(
    'dockerSettings',
    '/docker/settings',
    section === 'settings' ? view : undefined,
    ['basic', 'full'],
    undefined,
    section !== 'settings' || route.loading,
  );
  const [keyword, setKeyword] = useState('');
  const [terminal, setTerminal] = useState<string>();
  const [term, setTerm] = useState('');
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [busy, setBusy] = useState(false);
  const [operation, setOperation] = useState<Operation>();
  const [form] = Form.useForm();
  const [task, setTask] = useState<string>();
  const [detail, setDetail] = useState<{
    id: string;
    name: string;
    tab: string;
  }>();
  const [jsonDetail, setJsonDetail] = useState<{
    title: string;
    content: unknown;
  }>();
  const overview = useQuery({
    queryKey: ['docker-overview'],
    queryFn: dockerOverview,
  });
  const online = !!overview.data?.running;
  const tasks = useQuery({
    queryKey: ['docker-tasks'],
    queryFn: ({ signal }) =>
      listTasks({ module: 'docker', status: 'running' }, signal),
    enabled: online,
    refetchInterval: (query) =>
      !query.state.error && (query.state.data?.total ?? 0) > 0 ? 3000 : false,
  });
  const containerList = useQuery({
    queryKey: ['docker-containers'],
    queryFn: containers,
    enabled:
      online &&
      (tab === 'containers' ||
        (tab === 'images' && imageView === 'local') ||
        tab === 'volumes' ||
        operation?.kind === 'connect'),
    refetchInterval: (query) =>
      !query.state.error && tab === 'containers' ? 15000 : false,
  });
  const imageList = useQuery({
    queryKey: ['docker-images'],
    queryFn: images,
    enabled:
      online &&
      (tab === 'containers' ||
        (tab === 'images' && imageView === 'local') ||
        operation?.kind === 'container'),
  });
  const tagsByImage = useMemo(
    () => new Map(imageList.data?.map((image) => [image.Id, image.RepoTags])),
    [imageList.data],
  );
  const containersByImage = useMemo(() => {
    const rows = new Map<string, Container[]>();
    for (const container of containerList.data ?? []) {
      const matches = rows.get(container.ImageID);
      if (matches) matches.push(container);
      else rows.set(container.ImageID, [container]);
    }
    return rows;
  }, [containerList.data]);
  const containerImageName = (container: Container) => {
    const [reference] = container.Image.split('@');
    const imageID = container.ImageID.replace(/^sha256:/, '');
    const hasName =
      reference &&
      !reference.startsWith('sha256:') &&
      !(imageID.startsWith(reference) && /^[a-f0-9]+$/.test(reference));
    const tags = tagsByImage.get(container.ImageID);
    if (hasName) {
      if (reference.lastIndexOf(':') > reference.lastIndexOf('/'))
        return reference;
      return (
        tags?.find((tag) => tag === `${reference}:latest`) ||
        tags?.find((tag) => tag.startsWith(`${reference}:`)) ||
        reference
      );
    }
    return (
      tags?.[0] ||
      (imageList.isPending
        ? '读取中…'
        : imageList.isError
          ? '读取失败'
          : '未标记镜像')
    );
  };
  const networkList = useQuery({
    queryKey: ['docker-networks'],
    queryFn: networks,
    enabled:
      online &&
      (tab === 'networks' ||
        tab === 'overview' ||
        operation?.kind === 'container'),
  });
  const volumeList = useQuery({
    queryKey: ['docker-volumes'],
    queryFn: volumes,
    enabled: online && tab === 'volumes',
  });
  const registryList = useQuery({
    queryKey: ['docker-registries'],
    queryFn: registries,
    enabled:
      tab === 'registries' ||
      operation?.kind === 'pull' ||
      operation?.kind === 'push',
  });
  const search = useQuery({
    queryKey: ['docker-search', term],
    queryFn: () => searchImages(term),
    enabled: online && tab === 'images' && imageView === 'search' && !!term,
  });
  const guard = useQuery({
    queryKey: ['app-guard', 'docker'],
    queryFn: () => applicationGuard('docker'),
    enabled: tab === 'settings' && !!overview.data?.installed,
  });
  const refresh = () => {
    for (const queryKey of [
      ['docker-overview'],
      ['docker-containers'],
      ['docker-images'],
      ['docker-networks'],
      ['docker-volumes'],
      ['docker-registries'],
      ['docker-container-detail'],
      ['docker-tasks'],
      ['compose-projects'],
      ['compose-project-detail'],
      ['compose-project-logs'],
      ['compose-project-revisions'],
      ['app-guard', 'docker'],
    ])
      void client.invalidateQueries({ queryKey });
  };
  const run = async (job: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await job();
    } finally {
      refresh();
      setBusy(false);
    }
  };
  const open = (next: Operation) => {
    form.resetFields();
    form.setFieldsValue({
      autoStart: true,
      restart: 'unless-stopped',
      memoryMB: 0,
      cpus: 0,
      network: 'bridge',
      driver: 'bridge',
      disconnect: false,
      registryId: 'dockerhub',
      tag: 'latest',
      ports: [],
      mounts: [],
      ...next.values,
    });
    setOperation(next);
  };
  const filter = (
    <Input.Search
      placeholder="搜索名称或ID"
      allowClear
      style={{ width: 280, maxWidth: '100%' }}
      value={keyword}
      onChange={(event) => setKeyword(event.target.value)}
    />
  );
  const match = (text: string) =>
    text.toLowerCase().includes(keyword.toLowerCase());
  const destroy = (
    kind: string,
    id: string,
    name: string,
    owner?: ReturnType<typeof containerOwnership>,
  ) => {
    const dialog = modal.confirm({
      title: `删除${name}？`,
      content: owner ? (
        <Alert
          type={owner.label === '应用商店' ? 'error' : 'warning'}
          showIcon
          message={owner.removal}
          description={
            owner.link && (
              <a
                href={owner.link}
                onClick={(event) => {
                  event.preventDefault();
                  dialog.destroy();
                  void navigate(owner.link!);
                }}
              >
                {owner.action}
              </a>
            )
          }
        />
      ) : kind === 'containers' ? (
        '容器将删除，保留挂载的存储卷；运行中的容器需先停止。'
      ) : (
        '此操作不可撤销，正在使用的资源由 Docker 拒绝删除。'
      ),
      okText: owner ? '仍然删除' : '删除',
      okButtonProps: { danger: true },
      onOk: () =>
        run(async () => {
          await request(`/api/docker/${kind}/${encodeURIComponent(id)}`, {
            method: 'DELETE',
          });
          if (kind === 'containers') {
            client.setQueryData<Container[]>(['docker-containers'], (rows) =>
              rows?.filter((item) => item.Id !== id),
            );
          } else if (kind === 'images') {
            client.setQueryData<{ Id: string }[]>(['docker-images'], (rows) =>
              rows?.filter((item) => item.Id !== id),
            );
          } else if (kind === 'volumes') {
            client.setQueryData<{ Name: string }[]>(
              ['docker-volumes'],
              (rows) => rows?.filter((item) => item.Name !== id),
            );
          } else if (kind === 'networks') {
            client.setQueryData<{ Id: string }[]>(['docker-networks'], (rows) =>
              rows?.filter((item) => item.Id !== id),
            );
          }
        }),
    });
  };
  const prune = (kind: string) =>
    modal.confirm({
      title: '清理未使用的资源？',
      content:
        kind === 'volumes'
          ? '未被容器引用的存储卷及数据将永久删除。'
          : kind === 'containers'
            ? '仅清理已停止的独立容器，挂载的存储卷会保留。'
            : '仅清理 Docker 判定未使用的资源。',
      okButtonProps: { danger: true },
      onOk: () =>
        run(async () => {
          const result = await request(`/api/docker/prune/${kind}`, {
            method: 'POST',
          });
          setJsonDetail({ title: '清理结果', content: result });
        }),
    });
  async function transfer(
    kind: 'pull' | 'push',
    image: string,
    registryId?: string,
  ) {
    const result = await (kind === 'pull'
      ? pullImage(image, registryId)
      : pushImage(image, registryId));
    setTask(result.taskId);
    void client.invalidateQueries({ queryKey: ['docker-tasks'] });
  }
  async function action(row: Container, name: string) {
    await run(() =>
      request(`/api/docker/containers/${row.Id}/action`, {
        method: 'POST',
        data: { action: name },
      }),
    );
  }
  const itemName = (row: Container) =>
    row.Names?.map((name) => name.replace(/^\//, '')).join(', ') ||
    row.Id.slice(0, 12);
  const actions = (row: Container): MenuProps['items'] => [
    ...(!['running', 'paused', 'restarting'].includes(row.State)
      ? [{ key: 'start', label: '启动' }]
      : []),
    ...(['running', 'paused', 'restarting'].includes(row.State)
      ? [{ key: 'stop', label: '停止' }]
      : []),
    { key: 'restart', label: '重启' },
    ...(['running', 'paused', 'restarting'].includes(row.State)
      ? [{ key: 'kill', label: '强制停止', danger: true }]
      : []),
    ...(row.State === 'running'
      ? [{ key: 'pause', label: '暂停' }]
      : row.State === 'paused'
        ? [{ key: 'unpause', label: '恢复' }]
        : []),
  ];
  const previousTasks = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!tasks.data) return;
    const current = new Set(tasks.data.list.map((row) => row.taskId));
    if ([...previousTasks.current].some((id) => !current.has(id))) {
      void client.invalidateQueries({ queryKey: ['docker-images'] });
      void client.invalidateQueries({ queryKey: ['docker-overview'] });
      void client.invalidateQueries({ queryKey: ['compose-projects'] });
      void client.invalidateQueries({ queryKey: ['compose-project-detail'] });
      void client.invalidateQueries({
        queryKey: ['compose-project-revisions'],
      });
    }
    previousTasks.current = current;
  }, [tasks.data, client]);
  const usage = overview.data?.usage;
  const containerBytes = usage?.Containers?.every(
    (row) => row.SizeRw !== undefined && row.SizeRw >= 0,
  )
    ? usage.Containers.reduce((size, row) => size + row.SizeRw!, 0)
    : undefined;
  const volumeBytes = usage?.Volumes?.every(
    (row) => row.UsageData && row.UsageData.Size >= 0,
  )
    ? usage.Volumes.reduce((size, row) => size + row.UsageData!.Size, 0)
    : undefined;
  const space = (size: number | undefined) =>
    size === undefined || size < 0 ? '--' : formatSize(size);
  async function submit() {
    const values = await form.validateFields();
    if (!operation) return;
    await run(async () => {
      const { kind, id } = operation;
      if (kind === 'pull' || kind === 'push') {
        await transfer(kind, values.image, values.registryId);
      } else if (kind === 'container') {
        await request('/api/docker/containers', {
          method: 'POST',
          data: {
            ...values,
            env: values.env?.split(/\r?\n/).filter(Boolean) ?? [],
            command: values.command ? JSON.parse(values.command) : [],
            ports: values.ports ?? [],
            mounts: values.mounts ?? [],
          },
        });
        message.success('容器已创建');
      } else if (kind === 'network') {
        await request('/api/docker/networks', { method: 'POST', data: values });
      } else if (kind === 'volume') {
        await request('/api/docker/volumes', {
          method: 'POST',
          data: { name: values.name },
        });
      } else if (kind === 'registry') {
        await request('/api/docker/registries', {
          method: 'POST',
          data: { ...values, id, password: values.password || undefined },
        });
      } else if (kind === 'tag') {
        await request(`/api/docker/images/${id}/tag`, {
          method: 'POST',
          data: values,
        });
      } else if (kind === 'connect') {
        await request(`/api/docker/networks/${id}/connect`, {
          method: 'POST',
          data: values,
        });
      } else if (kind === 'rename') {
        await request(`/api/docker/containers/${id}/action`, {
          method: 'POST',
          data: { action: 'rename', name: values.name },
        });
      } else if (kind === 'exec') {
        const result = await request(`/api/docker/containers/${id}/exec`, {
          method: 'POST',
          data: { command: JSON.parse(values.command) },
        });
        setJsonDetail({ title: '命令执行结果', content: result });
      }
      setOperation(undefined);
    });
  }
  const registryOptions = registryList.data?.map((r) => ({
    value: r.id,
    label: r.name,
  }));
  const error =
    overview.error ||
    containerList.error ||
    imageList.error ||
    networkList.error ||
    volumeList.error ||
    registryList.error ||
    search.error;
  return {
    action,
    actions,
    busy,
    client,
    containerBytes,
    containerImageName,
    containerList,
    containersByImage,
    destroy,
    detail,
    error,
    filter,
    fixedActions,
    form,
    guard,
    imageList,
    imageTask,
    imageView,
    itemName,
    jsonDetail,
    match,
    message,
    modal,
    navigate,
    networkList,
    online,
    open,
    operation,
    overview,
    prune,
    refresh,
    registryList,
    registryOptions,
    run,
    search,
    selected,
    setDetail,
    setImageTask,
    setJsonDetail,
    setKeyword,
    setOperation,
    route,
    imageRoute,
    settingsRoute,
    view,
    resource,
    detailTab,
    setSelected,
    setTask,
    setTerm,
    setTerminal,
    space,
    submit,
    tab,
    task,
    tasks,
    terminal,
    token,
    usage,
    volumeBytes,
    volumeList,
  };
}
export type DockerPageState = ReturnType<typeof useDockerPage>;
