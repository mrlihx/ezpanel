import { lazy, Suspense, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Descriptions,
  Form,
  Grid,
  Input,
  InputNumber,
  Modal,
  Segmented,
  Select,
  Skeleton,
  Space,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import ContentSizedTable from '../../components/ContentSizedTable';
import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import { containerOwnership } from './containerOwnership';
import { request } from '../../api/client';
import {
  inspectContainer,
  networks,
  registries,
  type ContainerInspection,
} from '../../api/docker';
import { saveWebsite, website } from '../../api/website';
import { LAYOUT } from '../../styles/tokens';
import { formatTime } from '../../utils/format';
import ContainerEditor from './ContainerEditor';
import ContainerLogs from './ContainerLogs';
import type { Operation } from './DockerOperationForm';
import './container-manager.css';

const CodeEditor = lazy(() => import('../../components/CodeEditor'));
const ContainerMonitor = lazy(() => import('./ContainerMonitor'));
const restartOptions = [
  { value: 'no', label: '不自动重启' },
  { value: 'on-failure', label: '异常退出时重启' },
  { value: 'unless-stopped', label: '自动重启（手动停止除外）' },
  { value: 'always', label: '始终重启' },
];
export const containerStates: Record<string, string> = {
  created: '已创建',
  running: '运行中',
  exited: '已停止',
  paused: '已暂停',
  restarting: '正在重启',
  dead: '异常退出',
  removing: '正在删除',
};

function validTime(value?: string) {
  return value && !value.startsWith('0001-') ? formatTime(value) : '—';
}

function ContainerJSON({
  id,
  item,
}: {
  id: string;
  item: ContainerInspection;
}) {
  return (
    <div className="docker-container-code">
      <Suspense fallback={<Skeleton active />}>
        <CodeEditor
          path={`container-${id}.json`}
          editorTheme="vs-dark"
          readOnly
          value={JSON.stringify(item, null, 2)}
          options={{ wordWrap: 'on' }}
        />
      </Suspense>
    </div>
  );
}

export default function ContainerManager({
  id,
  name,
  initialTab,
  onTabChange,
  onClose,
  onNavigate,
  onChanged,
  onTask,
  onTerminal,
  onOperation,
}: {
  id: string;
  name: string;
  initialTab: string;
  onTabChange?: (tab: string) => void;
  onClose?: () => void;
  onNavigate?: () => void;
  onChanged: (name?: string) => void;
  onTask: (taskId: string) => void;
  onTerminal: () => void;
  onOperation: (operation: Operation) => void;
}) {
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const screens = Grid.useBreakpoint();
  const [localTab, setTab] = useState(initialTab);
  const requestedTab = onTabChange ? initialTab : localTab;
  const [busy, setBusy] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const detail = useQuery({
    queryKey: ['docker-container-detail', id],
    queryFn: ({ signal }) => inspectContainer(id, signal),
    refetchInterval:
      requestedTab === 'status' || requestedTab === 'stats' ? 5000 : false,
  });
  const item = detail.data;
  const currentName = item?.Name.replace(/^\//, '') || name;
  const owner = containerOwnership(item?.Config.Labels, currentName);
  const replaceable =
    !owner && !item?.HostConfig.AutoRemove && !item?.State.Paused;
  const tab =
    ((requestedTab === 'edit' || requestedTab === 'upgrade') && !replaceable) ||
    (requestedTab === 'rename' && owner)
      ? 'status'
      : requestedTab;
  const siteId = owner?.siteId ?? '';
  const site = useQuery({
    queryKey: ['website', siteId],
    queryFn: () => website(siteId),
    enabled: Boolean(siteId) && tab === 'status',
    retry: false,
  });
  const [networkForm] = Form.useForm();
  const [proxyForm] = Form.useForm();
  const networkList = useQuery({
    queryKey: ['docker-networks'],
    queryFn: networks,
    enabled: tab === 'network',
  });
  const registryList = useQuery({
    queryKey: ['docker-registries'],
    queryFn: registries,
    enabled: tab === 'commit' || tab === 'upgrade',
  });
  const running =
    item?.State.Running &&
    !item.State.Paused &&
    item.State.Status === 'running';
  const attachments = Object.entries(item?.NetworkSettings.Networks ?? {}).map(
    ([name, value]) => ({ name, ...value }),
  );
  const ports = Object.entries(item?.NetworkSettings.Ports ?? {});
  const names = attachments.map((entry) => entry.name);
  const path = `/api/docker/containers/${id}`;
  const initialImage = item?.Config.Image ?? '';
  const registryOptions =
    registryList.data?.map((entry) => ({
      value: entry.id,
      label: entry.name,
    })) ?? [];
  const targets = [
    ...new Set(
      ports.flatMap(([key, bindings]) =>
        key.endsWith('/tcp')
          ? (bindings ?? []).flatMap(({ HostIp, HostPort }) => {
              if (!HostPort) return [];
              const host =
                !HostIp || HostIp === '0.0.0.0' || HostIp === '::'
                  ? '127.0.0.1'
                  : HostIp;
              return [
                `http://${host.includes(':') ? `[${host}]` : host}:${HostPort}`,
              ];
            })
          : [],
      ),
    ),
  ];
  async function change(job: () => Promise<unknown>, success?: string) {
    setBusy(true);
    try {
      await job();
      if (success) void message.success(success);
      await detail.refetch();
      onChanged();
    } finally {
      setBusy(false);
    }
  }
  const action = (action: string, label: string) => {
    const run = () =>
      change(
        () => request(`${path}/action`, { method: 'POST', data: { action } }),
        `已${label}容器`,
      );
    if (action === 'stop' || action === 'restart')
      modal.confirm({ title: `${label}容器 ${currentName}？`, onOk: run });
    else void run().catch(() => {});
  };
  const enqueue = (kind: 'commit' | 'recreate', data: object) =>
    change(async () => {
      const result = await request<{ taskId: string }>(`${path}/${kind}`, {
        method: 'POST',
        data,
      });
      onTask(result.taskId);
    });
  const recreate = (data: object, upgrade = false) =>
    modal.confirm({
      title: upgrade ? '升级此容器？' : '保存容器配置并重建？',
      content: '运行中的容器会短暂中断。旧容器将保留为停止状态，数据卷会保留。',
      onOk: () => enqueue('recreate', data),
    });
  const host = item?.HostConfig;
  const resourceValues = {
    memoryMB: host?.Memory ? Math.ceil(host.Memory / 1048576) : undefined,
    cpus: host?.NanoCpus
      ? host.NanoCpus / 1e9
      : host?.CpuQuota && host.CpuQuota > 0
        ? host.CpuQuota / (host.CpuPeriod || 100000)
        : undefined,
    restart: host?.RestartPolicy.Name || 'no',
    retries: host?.RestartPolicy.MaximumRetryCount ?? 0,
  };
  const portLabels = (
    ports.length ? ports : Object.entries(item?.HostConfig.PortBindings ?? {})
  ).flatMap(([port, bindings]) =>
    bindings?.length
      ? bindings.map(
          ({ HostIp, HostPort }) =>
            `${HostIp ? `${HostIp}:` : ''}${HostPort} → ${port}`,
        )
      : [port],
  );
  const tags = (values: string[]) =>
    values.length ? (
      <Space size={[4, 4]} wrap>
        {values.map((value) => (
          <Tag key={value}>{value}</Tag>
        ))}
      </Space>
    ) : (
      '—'
    );
  const content = (
    <>
      {detail.error ? (
        <Alert
          showIcon
          type="error"
          message={detail.error.message}
          action={<Button onClick={() => void detail.refetch()}>重试</Button>}
        />
      ) : !item ? (
        <Skeleton active />
      ) : (
        <Tabs
          className="docker-container-tabs"
          tabPosition={screens.md ? 'left' : 'top'}
          activeKey={tab}
          onChange={onTabChange ?? setTab}
          destroyOnHidden
          items={[
            {
              key: 'status',
              label: '容器状态',
              children: (
                <Space
                  direction="vertical"
                  size="large"
                  style={{ width: '100%' }}
                >
                  <Space wrap>
                    <Tag
                      color={
                        running
                          ? 'success'
                          : item.State.Paused
                            ? 'warning'
                            : 'default'
                      }
                    >
                      {containerStates[item.State.Status] ?? item.State.Status}
                    </Tag>
                    {item.State.Paused ? (
                      <Button
                        disabled={busy}
                        onClick={() => action('unpause', '恢复')}
                      >
                        恢复
                      </Button>
                    ) : null}
                    {item.State.Running ||
                    item.State.Status === 'restarting' ? (
                      <Button
                        disabled={busy}
                        onClick={() => action('stop', '停止')}
                      >
                        停止
                      </Button>
                    ) : (
                      <Button
                        type="primary"
                        disabled={busy}
                        onClick={() => action('start', '启动')}
                      >
                        启动
                      </Button>
                    )}
                    <Button
                      disabled={busy || item.State.Paused}
                      onClick={() => action('restart', '重启')}
                    >
                      重启
                    </Button>
                  </Space>
                  {item.State.Error && (
                    <Alert showIcon type="error" message={item.State.Error} />
                  )}
                  <Descriptions
                    bordered
                    size="small"
                    column={1}
                    items={[
                      { label: '容器名称', children: currentName },
                      {
                        label: '配置归属',
                        children: site.data ? (
                          <Link
                            to={`/websites/${site.data.projectType}`}
                            state={{ openWebsiteSettings: site.data.id }}
                            onClick={onNavigate}
                          >
                            {site.data.name}
                          </Link>
                        ) : (
                          (owner?.label ?? '独立容器')
                        ),
                      },
                      {
                        label: '容器 ID',
                        children: (
                          <Typography.Text copyable={{ text: item.Id }}>
                            {item.Id.slice(0, 12)}
                          </Typography.Text>
                        ),
                      },
                      { label: '使用的镜像', children: initialImage },
                      { label: '创建时间', children: validTime(item.Created) },
                      {
                        label: '启动时间',
                        children: validTime(item.State.StartedAt),
                      },
                      {
                        label: '运行时间',
                        children:
                          item.State.Running &&
                          validTime(item.State.StartedAt) !== '—'
                            ? (() => {
                                const minutes = Math.max(
                                  0,
                                  Math.floor(
                                    (detail.dataUpdatedAt -
                                      Date.parse(item.State.StartedAt)) /
                                      60000,
                                  ),
                                );
                                return minutes >= 60
                                  ? `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分钟`
                                  : `${minutes} 分钟`;
                              })()
                            : '—',
                      },
                      { label: '端口', children: tags(portLabels) },
                      {
                        label: 'IPv4',
                        children: tags(
                          attachments
                            .map((entry) => entry.IPAddress)
                            .filter(Boolean),
                        ),
                      },
                      {
                        label: 'IPv6',
                        children: tags(
                          attachments
                            .map((entry) => entry.GlobalIPv6Address)
                            .filter(Boolean),
                        ),
                      },
                    ].map((entry) => ({ ...entry, key: entry.label }))}
                  />
                </Space>
              ),
            },
            {
              key: 'terminal',
              label: '容器终端',
              children: (
                <Space wrap>
                  <Button
                    type="primary"
                    disabled={!running}
                    onClick={onTerminal}
                  >
                    打开容器终端
                  </Button>
                  <Button
                    disabled={!running}
                    onClick={() =>
                      onOperation({
                        kind: 'exec',
                        id,
                        values: { command: '["sh", "-lc", "id"]' },
                      })
                    }
                  >
                    执行命令
                  </Button>
                  {!running && (
                    <Typography.Text type="secondary">
                      仅运行中的容器可以打开终端
                    </Typography.Text>
                  )}
                </Space>
              ),
            },
            {
              key: 'detail',
              label: '容器详情',
              children: (
                <Tabs
                  size="small"
                  destroyOnHidden
                  items={[
                    {
                      key: 'visual',
                      label: '配置概览',
                      children: (
                        <Space direction="vertical" style={{ width: '100%' }}>
                          <Descriptions
                            bordered
                            size="small"
                            column={1}
                            items={[
                              {
                                label: '镜像',
                                children: initialImage || item.Image,
                              },
                              { label: '端口配置', children: tags(portLabels) },
                              {
                                label: 'CMD',
                                children: item.Config.Cmd?.join(' ') || '—',
                              },
                              {
                                label: 'ENTRYPOINT',
                                children:
                                  item.Config.Entrypoint?.join(' ') || '—',
                              },
                            ].map((entry) => ({ ...entry, key: entry.label }))}
                          />
                          <ContentSizedTable
                            fillColumn="value"
                            rowKey="index"
                            size="small"
                            pagination={false}
                            dataSource={(item.Config.Env ?? []).map(
                              (env, index) => {
                                const split = env.indexOf('=');
                                return {
                                  index,
                                  name: split < 0 ? env : env.slice(0, split),
                                  value: split < 0 ? '' : env.slice(split + 1),
                                };
                              },
                            )}
                            columns={[
                              {
                                title: '环境变量',
                                dataIndex: 'name',
                                width: 240,
                              },
                              { title: '值', dataIndex: 'value' },
                            ]}
                          />
                        </Space>
                      ),
                    },
                    {
                      key: 'file',
                      label: '原始 JSON',
                      children: <ContainerJSON id={id} item={item} />,
                    },
                  ]}
                />
              ),
            },
            {
              key: 'volumes',
              label: '数据存储卷',
              children: (
                <ContentSizedTable
                  fillColumn="Source"
                  rowKey="Destination"
                  size="small"
                  pagination={false}
                  dataSource={item.Mounts ?? []}
                  columns={[
                    {
                      title: '数据卷 / 宿主机路径',
                      dataIndex: 'Source',
                      render: (_, row) => row.Name || row.Source,
                    },
                    { title: '容器目录', dataIndex: 'Destination', width: 260 },
                    { title: '类型', dataIndex: 'Type', width: 100 },
                    {
                      title: '访问',
                      dataIndex: 'RW',
                      render: (value) => (value ? '可读写' : '只读'),
                    },
                  ]}
                />
              ),
            },
            {
              key: 'network',
              label: '容器网络',
              children: (
                <Space
                  direction="vertical"
                  size="middle"
                  style={{ width: '100%' }}
                >
                  {networkList.error && (
                    <Alert type="error" message={networkList.error.message} />
                  )}
                  <Form
                    form={networkForm}
                    layout="inline"
                    onFinish={({ network }) =>
                      void change(
                        () =>
                          request(`/api/docker/networks/${network}/connect`, {
                            method: 'POST',
                            data: { container: id },
                          }),
                        '已加入网络',
                      )
                        .then(() => networkForm.resetFields())
                        .catch(() => {})
                    }
                  >
                    <Form.Item
                      name="network"
                      rules={[{ required: true, message: '请选择网络' }]}
                    >
                      <Select
                        aria-label="选择容器网络"
                        placeholder="选择可加入的网络"
                        style={{ width: 220, maxWidth: '100%' }}
                        loading={networkList.isFetching}
                        options={networkList.data
                          ?.filter(
                            (network) =>
                              !names.includes(network.Name) &&
                              !['host', 'none'].includes(network.Name),
                          )
                          .map((network) => ({
                            value: network.Id,
                            label: network.Name,
                          }))}
                      />
                    </Form.Item>
                    <Button
                      type="primary"
                      htmlType="submit"
                      disabled={Boolean(owner) || busy}
                    >
                      加入网络
                    </Button>
                  </Form>
                  <ContentSizedTable
                    fillColumn="name"
                    rowKey="name"
                    size="small"
                    pagination={false}
                    dataSource={attachments}
                    columns={[
                      { title: '网络名', dataIndex: 'name' },
                      {
                        title: 'IPv4',
                        dataIndex: 'IPAddress',
                        render: (value) => value || '—',
                      },
                      {
                        title: 'IPv6',
                        dataIndex: 'GlobalIPv6Address',
                        render: (value) => value || '—',
                      },
                      {
                        title: '网关',
                        render: (_, row) =>
                          [row.Gateway, row.IPv6Gateway]
                            .filter(Boolean)
                            .join(' / ') || '—',
                      },
                      {
                        title: 'MAC 地址',
                        dataIndex: 'MacAddress',
                        render: (value) => value || '—',
                      },
                      {
                        title: '操作',
                        render: (_, row) => (
                          <Button
                            type="link"
                            disabled={
                              Boolean(owner) ||
                              busy ||
                              ['host', 'none'].includes(row.name)
                            }
                            onClick={() =>
                              modal.confirm({
                                title: `退出网络 ${row.name}？`,
                                content: '此网络上的连接会中断。',
                                onOk: () =>
                                  change(
                                    () =>
                                      request(
                                        `/api/docker/networks/${row.NetworkID}/connect`,
                                        {
                                          method: 'POST',
                                          data: {
                                            container: id,
                                            disconnect: true,
                                          },
                                        },
                                      ),
                                    '已退出网络',
                                  ),
                              })
                            }
                          >
                            退出网络
                          </Button>
                        ),
                      },
                    ]}
                  />
                </Space>
              ),
            },
            {
              key: 'restart',
              label: '资源与重启策略',
              children: (
                <ApplicationSettingsForm<typeof resourceValues>
                  className="docker-container-form"
                  initialValues={resourceValues}
                  onFinish={(values) => {
                    const data = {
                      memoryMB:
                        values.memoryMB === resourceValues.memoryMB
                          ? undefined
                          : (values.memoryMB ?? undefined),
                      cpus:
                        values.cpus === resourceValues.cpus
                          ? undefined
                          : (values.cpus ?? undefined),
                      restart:
                        !host?.AutoRemove &&
                        (values.restart !== resourceValues.restart ||
                          (values.restart === 'on-failure' &&
                            values.retries !== resourceValues.retries))
                          ? values.restart
                          : undefined,
                      retries:
                        values.restart === 'on-failure' ? values.retries : 0,
                    };
                    if (
                      data.memoryMB === undefined &&
                      data.cpus === undefined &&
                      !data.restart
                    )
                      return;
                    void change(async () => {
                      const result = await request<{ Warnings?: string[] }>(
                        `${path}/update`,
                        {
                          method: 'POST',
                          data,
                        },
                      );
                      if (result.Warnings?.length)
                        void message.warning(result.Warnings.join('，'));
                      else void message.success('容器资源和重启策略已更新');
                    }).catch(() => {});
                  }}
                >
                  <Form.Item
                    name="cpus"
                    label="CPU 限额"
                    rules={[{ type: 'number', min: 0, max: 1024 }]}
                  >
                    <InputNumber
                      min={0}
                      max={1024}
                      step={0.1}
                      placeholder="留空保留原值"
                      addonAfter="核"
                    />
                  </Form.Item>
                  <Form.Item
                    name="memoryMB"
                    label="内存限额"
                    rules={[{ type: 'integer', min: 0, max: 1048576 }]}
                  >
                    <InputNumber
                      min={0}
                      max={1048576}
                      precision={0}
                      placeholder="留空保留原值"
                      addonAfter="MiB"
                    />
                  </Form.Item>
                  <Form.Item
                    name="restart"
                    label="重启策略"
                    extra={host?.AutoRemove && '自动删除的容器不能设置重启策略'}
                  >
                    <Select
                      disabled={host?.AutoRemove}
                      options={restartOptions}
                    />
                  </Form.Item>
                  <Form.Item
                    noStyle
                    shouldUpdate={(before, after) =>
                      before.restart !== after.restart
                    }
                  >
                    {({ getFieldValue }) =>
                      !host?.AutoRemove &&
                      getFieldValue('restart') === 'on-failure' && (
                        <Form.Item
                          name="retries"
                          label="最大重试次数"
                          extra="0 表示不限次数"
                        >
                          <InputNumber min={0} max={1000} precision={0} />
                        </Form.Item>
                      )
                    }
                  </Form.Item>
                  {owner && (
                    <Alert
                      type="info"
                      showIcon
                      message="此页设置作用于当前容器。归属配置重新部署时会重新应用。"
                    />
                  )}
                  <Form.Item className="application-settings-actions">
                    <Button type="primary" htmlType="submit" loading={busy}>
                      应用热更新
                    </Button>
                  </Form.Item>
                </ApplicationSettingsForm>
              ),
            },
            {
              key: 'commit',
              label: '创建镜像',
              children: (
                <Form
                  layout="vertical"
                  className="docker-container-form"
                  initialValues={{ tag: 'latest', registry: '' }}
                  onFinish={(values) => {
                    const address = registryList.data?.find(
                      (entry) => entry.id === values.registry,
                    )?.address;
                    const registry = address
                      ?.replace(/^https?:\/\//, '')
                      .replace(/\/$/, '');
                    const name = values.name.trim();
                    const image = `${registry && !name.startsWith(`${registry}/`) ? `${registry}/` : ''}${name}:${values.tag?.trim() || 'latest'}`;
                    void enqueue('commit', {
                      image,
                      comment: values.comment,
                      author: values.author,
                      export: values.export,
                    }).catch(() => {});
                  }}
                >
                  <Form.Item label="创建方式">
                    <Segmented
                      value={advanced ? 'advanced' : 'simple'}
                      onChange={(value) => setAdvanced(value === 'advanced')}
                      options={[
                        { value: 'simple', label: '简单模式' },
                        { value: 'advanced', label: '高级模式' },
                      ]}
                    />
                  </Form.Item>
                  <Form.Item
                    name="name"
                    label="镜像名称"
                    rules={[{ required: true, whitespace: true }]}
                  >
                    <Input placeholder="例如 my-project" />
                  </Form.Item>
                  <Form.Item name="tag" label="镜像标签">
                    <Input placeholder="latest" />
                  </Form.Item>
                  {advanced && (
                    <>
                      <Form.Item
                        name="registry"
                        label="仓库地址"
                        preserve={false}
                      >
                        <Select
                          options={[
                            { value: '', label: 'docker.io' },
                            ...registryOptions.filter(
                              (entry) => entry.value !== 'dockerhub',
                            ),
                          ]}
                        />
                      </Form.Item>
                      <Form.Item name="comment" label="描述" preserve={false}>
                        <Input maxLength={1024} />
                      </Form.Item>
                      <Form.Item name="author" label="作者" preserve={false}>
                        <Input maxLength={256} />
                      </Form.Item>
                      <Form.Item
                        name="export"
                        valuePropName="checked"
                        preserve={false}
                      >
                        <Checkbox>生成后导出 tar.gz</Checkbox>
                      </Form.Item>
                    </>
                  )}
                  <Button type="primary" htmlType="submit" loading={busy}>
                    生成镜像
                  </Button>
                </Form>
              ),
            },
            ...(replaceable
              ? [
                  {
                    key: 'edit',
                    label: '编辑容器',
                    children: (
                      <ContainerEditor
                        container={item}
                        busy={busy}
                        onResource={() => setTab('restart')}
                        onSubmit={recreate}
                      />
                    ),
                  },
                  {
                    key: 'upgrade',
                    label: '升级容器',
                    children: (
                      <Form
                        layout="vertical"
                        className="docker-container-form"
                        initialValues={{ image: initialImage, pull: true }}
                        onFinish={(values) =>
                          recreate({ ...values, upgrade: true }, true)
                        }
                      >
                        <Form.Item label="当前镜像">
                          <Typography.Text>{initialImage}</Typography.Text>
                        </Form.Item>
                        <Form.Item
                          name="image"
                          label="目标镜像"
                          rules={[{ required: true, whitespace: true }]}
                        >
                          <Input placeholder="镜像名称:标签" />
                        </Form.Item>
                        <Form.Item name="registryId" label="仓库凭据">
                          <Select
                            allowClear
                            placeholder="无需认证"
                            options={registryOptions}
                          />
                        </Form.Item>
                        <Form.Item name="pull" valuePropName="checked">
                          <Checkbox>拉取目标镜像</Checkbox>
                        </Form.Item>
                        <Form.Item name="force" valuePropName="checked">
                          <Checkbox>镜像相同时仍重建容器</Checkbox>
                        </Form.Item>
                        <Button type="primary" htmlType="submit" loading={busy}>
                          升级容器
                        </Button>
                      </Form>
                    ),
                  },
                ]
              : []),
            ...(!owner
              ? [
                  {
                    key: 'rename',
                    label: '重命名',
                    children: (
                      <Form
                        key={currentName}
                        layout="vertical"
                        className="docker-container-form"
                        initialValues={{ name: currentName }}
                        onFinish={({ name }) =>
                          void change(async () => {
                            await request(`${path}/action`, {
                              method: 'POST',
                              data: { action: 'rename', name: name.trim() },
                            });
                            onChanged(name.trim());
                          }, '容器已重命名').catch(() => {})
                        }
                      >
                        <Form.Item
                          name="name"
                          label="容器名称"
                          rules={[{ required: true, whitespace: true }]}
                        >
                          <Input />
                        </Form.Item>
                        <Button type="primary" htmlType="submit" loading={busy}>
                          保存名称
                        </Button>
                      </Form>
                    ),
                  },
                ]
              : []),
            {
              key: 'stats',
              label: '实时监控',
              children: (
                <Suspense fallback={<Skeleton active />}>
                  <ContainerMonitor
                    id={id}
                    active={!!running && tab === 'stats'}
                  />
                </Suspense>
              ),
            },
            {
              key: 'logs',
              label: '容器日志',
              children: <ContainerLogs id={id} name={currentName} />,
            },
            {
              key: 'proxy',
              label: '反向代理',
              children: (
                <Form
                  form={proxyForm}
                  layout="vertical"
                  className="docker-container-form"
                  initialValues={{ target: targets[0] }}
                  onFinish={(values) =>
                    void change(async () => {
                      const site = await saveWebsite(undefined, {
                        name: '',
                        type: 'proxy',
                        projectType: 'proxy',
                        root: '',
                        remark: '',
                        phpVersion: '',
                        domains: values.domains
                          .split(/\r?\n/)
                          .map((entry: string) => entry.trim())
                          .filter(Boolean),
                        proxyRoutes: [
                          {
                            name: currentName,
                            path: '/',
                            target: values.target,
                            host: '',
                            enabled: true,
                          },
                        ],
                      });
                      proxyForm.setFieldValue('domains', '');
                      void client.invalidateQueries({ queryKey: ['websites'] });
                      void message.success(`已创建反向代理 ${site.name}`);
                    }).catch(() => {})
                  }
                >
                  <Form.Item
                    name="domains"
                    label="访问域名或 IP"
                    rules={[{ required: true, whitespace: true }]}
                  >
                    <Input.TextArea
                      rows={2}
                      placeholder="每行一个，可指定端口"
                    />
                  </Form.Item>
                  <Form.Item
                    name="target"
                    label="代理目标"
                    rules={[{ required: true }]}
                    extra={
                      targets.length
                        ? undefined
                        : '容器发布 TCP 端口后，可用它作为代理目标。'
                    }
                  >
                    <Select
                      placeholder="选择已发布的 TCP 端口"
                      options={targets.map((value) => ({
                        label: value,
                        value,
                      }))}
                    />
                  </Form.Item>
                  <Space wrap>
                    <Button
                      type="primary"
                      htmlType="submit"
                      loading={busy}
                      disabled={!targets.length}
                    >
                      创建反向代理
                    </Button>
                    <Link to="/websites/proxy" onClick={onNavigate}>
                      管理反向代理站点
                    </Link>
                  </Space>
                </Form>
              ),
            },
          ]}
        />
      )}
    </>
  );
  return (
    <Modal
      open
      title={`容器 · ${currentName}`}
      width={LAYOUT.toolboxWidth}
      centered
      footer={null}
      maskClosable={false}
      onCancel={onClose}
      styles={{ body: { height: '72dvh', display: 'flex', minWidth: 0 } }}
    >
      {content}
    </Modal>
  );
}
