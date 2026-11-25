import LogText from '../../components/LogText';
import { useEffect, useState, type ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  App,
  Alert,
  Button,
  Card,
  Checkbox,
  Descriptions,
  Drawer,
  Dropdown,
  Empty,
  Flex,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  Spin,
  Tag,
  Typography,
  Upload,
} from 'antd';
import { useQueryClient } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  inspectPlugin,
  pluginAction,
  pluginDetail,
  usePluginList,
  type Detail,
  type Inspection,
  type Manifest,
  type Plugin,
} from '../../api/plugin';
import { request } from '../../api/client';
import TaskDrawer from '../../components/TaskDrawer';
import { LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';

type Mode = 'install' | 'upgrade' | 'enable' | 'uninstall';
const trust =
  '插件前端拥有当前管理员访问能力；后端降权但插件间不隔离，请仅启用可信来源插件。execAllow 授予命令及参数可达到的 root 能力，fsRoots 仅限制 Host 文件入口，并非插件的全部能力边界。';
const statusLabel: Record<string, string> = {
  disabled: '未启用',
  starting: '启动中',
  running: '运行中',
  stopping: '停止中',
  crashed: '已崩溃',
  failed: '启动失败',
};
function ManifestSummary({ manifest }: { manifest: Manifest }) {
  return (
    <Descriptions
      column={1}
      size="small"
      items={[
        {
          key: 'name',
          label: '插件',
          children: `${manifest.name} / ${manifest.id}`,
        },
        {
          key: 'version',
          label: '版本 / 作者',
          children: `${manifest.version} / ${manifest.author || '未提供'}`,
        },
        {
          key: 'permissions',
          label: '权限',
          children: manifest.permissions?.join('、') || '默认 KV / 日志',
        },
        {
          key: 'exec',
          label: 'root 命令白名单',
          children: manifest.execAllow?.join('、') || '无',
        },
        {
          key: 'fs',
          label: 'root 目录白名单',
          children: manifest.fsRoots?.join('、') || '无',
        },
      ]}
    />
  );
}
export default function PluginsPage({
  keyword = '',
  renderList,
}: {
  keyword?: string;
  renderList?: (
    rows: Plugin[],
    actions: (p: Plugin) => ReactNode,
    importApp: () => void,
  ) => ReactNode;
}) {
  const { message, modal } = App.useApp();
  const navigate = useNavigate();
  const client = useQueryClient();
  const query = usePluginList();
  const [mode, setMode] = useState<Mode>();
  const [selected, setSelected] = useState<Plugin>();
  const [detail, setDetail] = useState<Detail>();
  const [inspection, setInspection] = useState<Inspection>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [configOpen, setConfigOpen] = useState(false);
  const [configPluginId, setConfigPluginId] = useState<string>();
  const [configDetail, setConfigDetail] = useState<Detail>();
  const [configError, setConfigError] = useState('');
  const [configBusy, setConfigBusy] = useState(false);
  const [logs, setLogs] = useState<Plugin>();
  const [task, setTask] = useState<{
    id: string;
    action: string;
    pluginId?: string;
  }>();
  const [installed, setInstalled] = useState<string>();
  const [form] = Form.useForm();
  const [configForm] = Form.useForm();
  const refresh = () => client.invalidateQueries({ queryKey: ['plugins'] });
  const open = async (next: Mode, plugin?: Plugin) => {
    setMode(next);
    setSelected(plugin);
    setDetail(undefined);
    setInspection(undefined);
    setError('');
    form.resetFields();
    if (plugin) {
      setBusy(true);
      try {
        setDetail(await pluginDetail(plugin.id));
      } catch (e) {
        setError(e instanceof Error ? e.message : '详情加载失败');
      } finally {
        setBusy(false);
      }
    }
  };
  const inspect = async (file: File) => {
    setBusy(true);
    setError('');
    setInspection(undefined);
    try {
      const result = await inspectPlugin(file);
      if (mode === 'upgrade' && result.manifest.id !== selected?.id)
        throw new Error('升级包 id 与当前插件不匹配');
      setInspection(result);
    } catch (e) {
      setError(e instanceof Error ? e.message : '检查失败');
    } finally {
      setBusy(false);
    }
  };
  const submit = async (values: {
    purgeData?: boolean;
    values?: Record<string, string>;
  }) => {
    if (!mode) return;
    setBusy(true);
    setError('');
    try {
      if (mode === 'enable') {
        await pluginAction(selected!.id, 'enable');
        await refresh();
        setMode(undefined);
        message.success('插件已运行');
      } else {
        const data =
          mode === 'uninstall'
            ? { purgeData: !!values.purgeData }
            : {
                uploadId: inspection!.uploadId,
                ...(mode === 'upgrade' ? { values: values.values } : {}),
              };
        const result =
          mode === 'install'
            ? await request<{ taskId: string }>('/api/plugins/install', {
                method: 'POST',
                data,
                silentError: true,
              })
            : await pluginAction<{ taskId: string }>(selected!.id, mode, data);
        setTask({
          id: result.taskId,
          action: mode,
          pluginId: inspection?.manifest.id ?? selected?.id,
        });
        setMode(undefined);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : '操作失败');
      if (mode === 'enable' && selected?.type === 'process') {
        const tail = await request<{ content: string }>(
          `/api/plugins/${selected.id}/logs`,
          { params: { lines: 30 }, silentError: true },
        ).catch(() => null);
        if (tail?.content) setError((current) => `${current}\n${tail.content}`);
      }
    } finally {
      setBusy(false);
    }
  };
  const disable = async (plugin: Plugin) => {
    try {
      await pluginAction(plugin.id, 'disable');
      await refresh();
      message.success('插件已停用');
    } catch (e) {
      message.error(e instanceof Error ? e.message : '停用失败');
    }
  };
  const configure = async (plugin: Plugin) => {
    setConfigPluginId(plugin.id);
    setConfigError('');
    setConfigBusy(true);
    setConfigOpen(true);
    setConfigDetail(undefined);
    try {
      const d = await pluginDetail(plugin.id);
      setConfigDetail(d);
    } catch (e) {
      setConfigError(e instanceof Error ? e.message : '配置读取失败');
    } finally {
      setConfigBusy(false);
    }
  };
  const saveConfig = async (input: {
    values?: Record<string, string>;
    clear?: Record<string, boolean>;
  }) => {
    if (!configDetail) return;
    const values = { ...input.values };
    for (const field of configDetail.manifest.env ?? []) {
      if (field.secret) {
        if (input.clear?.[field.key]) values[field.key] = '';
        else if (!values[field.key]) delete values[field.key];
      }
    }
    setConfigBusy(true);
    setConfigError('');
    try {
      await request(`/api/plugins/${configPluginId}/config`, {
        method: 'PUT',
        data: { values },
        silentError: true,
      });
      await refresh();
      setConfigOpen(false);
      message.success('配置已保存，运行状态已恢复');
    } catch (e) {
      setConfigError(e instanceof Error ? e.message : '保存失败');
    } finally {
      setConfigBusy(false);
    }
  };
  const configPlugin = query.data?.find((p) => p.id === configPluginId);
  const manifest = mode === 'enable' ? detail?.manifest : inspection?.manifest;
  const pluginControls = (p: Plugin) => (
    <Space>
      {p.activeTaskId ? (
        <Button
          type="link"
          onClick={() => setTask({ id: p.activeTaskId, action: '插件任务' })}
        >
          查看任务
        </Button>
      ) : p.enabled ? (
        <Popconfirm
          title="确认停用插件并断开其连接？"
          onConfirm={() => disable(p)}
        >
          <Button type="link">停用</Button>
        </Popconfirm>
      ) : (
        <Button type="link" onClick={() => void open('enable', p)}>
          启用
        </Button>
      )}
      {p.runtimeStatus === 'running' && (
        <Button type="link" onClick={() => navigate(`/p/${p.id}`)}>
          打开
        </Button>
      )}
      {p.type === 'process' && (
        <>
          <Button
            type="link"
            disabled={!!p.activeTaskId}
            onClick={() => void configure(p)}
          >
            配置
          </Button>
          <Button type="link" onClick={() => setLogs(p)}>
            日志
          </Button>
        </>
      )}
      <Dropdown
        menu={{
          items: [
            ...(p.type === 'process' && p.enabled
              ? [
                  {
                    key: 'restart',
                    label: p.runtimeStatus === 'failed' ? '重试启动' : '重启',
                  },
                ]
              : []),
            { key: 'upgrade', label: '升级' },
            { key: 'uninstall', label: '卸载', danger: true },
          ],
          onClick: ({ key }) => {
            if (key === 'restart')
              modal.confirm({
                title: '确认重启插件？',
                onOk: async () => {
                  try {
                    await pluginAction(p.id, 'restart');
                    await refresh();
                    message.success('插件已重启');
                  } catch (e) {
                    message.error(e instanceof Error ? e.message : '重启失败');
                  }
                },
              });
            else void open(key as Mode, p);
          },
        }}
        disabled={!!p.activeTaskId}
      >
        <Button type="link">更多</Button>
      </Dropdown>
    </Space>
  );
  const actions = (plugin: Plugin) =>
    renderList ? (
      <Space size={12}>
        <Button
          type="link"
          style={{ padding: 0 }}
          disabled={!!plugin.activeTaskId}
          onClick={() => void configure(plugin)}
        >
          设置
        </Button>
        <Button
          type="link"
          danger
          style={{ padding: 0 }}
          disabled={!!plugin.activeTaskId}
          onClick={() => void open('uninstall', plugin)}
        >
          卸载
        </Button>
      </Space>
    ) : (
      pluginControls(plugin)
    );
  return (
    <>
      {!renderList && (
        <Flex
          justify="space-between"
          align="center"
          style={{ marginBottom: SPACING.lg }}
        >
          <Button type="primary" onClick={() => void open('install')}>
            导入扩展应用
          </Button>
        </Flex>
      )}
      {query.error && (
        <Alert
          type="error"
          message={query.error.message}
          action={<Button onClick={() => void query.refetch()}>重试</Button>}
          style={{ marginBottom: SPACING.lg }}
        />
      )}
      {installed && (
        <Alert
          type="success"
          message="已安装，尚未启用"
          action={
            <Button
              onClick={() => {
                const p = query.data?.find((row) => row.id === installed);
                if (p) void open('enable', p);
              }}
            >
              启用插件
            </Button>
          }
          closable
          onClose={() => setInstalled(undefined)}
          style={{ marginBottom: SPACING.lg }}
        />
      )}
      {renderList ? (
        renderList(query.data ?? [], actions, () => void open('install'))
      ) : (
        <Card>
          <ContentSizedTable<Plugin>
            fillColumn="description"
            growColumns={['name']}
            rowKey="id"
            dataSource={(query.data ?? []).filter((item) =>
              `${item.name} ${item.id} ${item.description}`
                .toLowerCase()
                .includes(keyword.toLowerCase()),
            )}
            loading={query.isPending}
            pagination={false}
            locale={{ emptyText: '尚未安装插件' }}
            columns={[
              {
                title: '名称 / id',
                key: 'name',
                render: (_, p) => (
                  <>
                    <Typography.Text strong>{p.name}</Typography.Text>
                    <br />
                    <Typography.Text type="secondary">{p.id}</Typography.Text>
                  </>
                ),
              },
              { title: '版本', dataIndex: 'version' },
              {
                title: '运行状态',
                key: 'status',
                render: (_, p) => (
                  <Tag
                    color={
                      p.runtimeStatus === 'running'
                        ? 'success'
                        : p.runtimeStatus === 'failed'
                          ? 'error'
                          : 'default'
                    }
                  >
                    {statusLabel[p.runtimeStatus] ?? p.runtimeStatus}
                  </Tag>
                ),
              },
              { title: '描述', dataIndex: 'description' },
              {
                title: '安装时间',
                dataIndex: 'installedAt',
                render: formatTime,
              },
              {
                title: '操作',
                key: 'actions',
                render: (_, p) => actions(p),
              },
            ]}
          />
        </Card>
      )}
      <Modal
        title={
          mode === 'install'
            ? '安装插件'
            : mode === 'upgrade'
              ? `升级插件 · ${selected?.version} → ${inspection?.manifest.version ?? '待检查'}`
              : mode === 'enable'
                ? '启用插件'
                : '卸载插件'
        }
        open={!!mode}
        onCancel={() => {
          if (!busy) setMode(undefined);
        }}
        footer={null}
        destroyOnHidden
        width={LAYOUT.modalWidth}
      >
        <Form
          form={form}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
          onFinish={submit}
        >
          {error && (
            <Alert
              type="error"
              showIcon
              message={
                <pre style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{error}</pre>
              }
              style={{ marginBottom: SPACING.lg }}
            />
          )}
          {(mode === 'install' || mode === 'upgrade') && (
            <Upload
              beforeUpload={(file) => {
                void inspect(file);
                return false;
              }}
              accept=".zip"
              showUploadList={false}
              disabled={busy}
            >
              <Button loading={busy}>选择 ZIP 并检查</Button>
            </Upload>
          )}
          {manifest && (
            <Space
              direction="vertical"
              size={SPACING.lg}
              style={{ width: '100%', marginBlock: SPACING.lg }}
            >
              <ManifestSummary manifest={manifest} />
              <Alert
                type="warning"
                showIcon
                message="仅安装和启用可信插件"
                description={trust}
              />
            </Space>
          )}
          {inspection && mode === 'upgrade' && (
            <Alert
              type="warning"
              message="本次新增或扩大的授权"
              description={
                <>
                  <div>
                    权限：
                    {inspection.permissionChanges.permissions?.join('、') ||
                      '无'}
                  </div>
                  <div>
                    命令：
                    {inspection.permissionChanges.execAllow?.join('、') || '无'}
                  </div>
                  <div>
                    目录：
                    {inspection.permissionChanges.fsRoots?.join('、') || '无'}
                  </div>
                  <div>
                    快照仅覆盖托管数据，无法回滚外部数据库、文件或网络副作用。
                  </div>
                </>
              }
              style={{ marginBottom: SPACING.lg }}
            />
          )}
          {mode === 'upgrade' &&
            (inspection?.manifest.env ?? []).map((env) => (
              <Form.Item
                key={env.key}
                name={['values', env.key]}
                label={env.label || env.key}
                extra={
                  env.secret
                    ? '秘密不回显；省略保留，显式填写后替换。秘密属性变化必须填写。'
                    : `现值：${detail?.config[env.key]?.value ?? '未设置'}；新默认值：${env.default || '空'}`
                }
                rules={[
                  {
                    required:
                      env.required &&
                      !detail?.config[env.key]?.value &&
                      !detail?.config[env.key]?.isSet &&
                      !env.default,
                    message: '请填写新增必填配置',
                  },
                ]}
              >
                {env.secret ? (
                  <Input.Password autoComplete="new-password" />
                ) : (
                  <Input />
                )}
              </Form.Item>
            ))}
          {mode === 'uninstall' && (
            <>
              <Alert
                type="warning"
                showIcon
                message={`确认卸载自定义应用「${selected?.name ?? selected?.id}」？`}
                description="仅删除面板托管包、配置与 KV，不卸载外部服务；失败残留将在任务中显示。"
                style={{ marginBottom: SPACING.lg }}
              />
              <Form.Item
                name="purgeData"
                valuePropName="checked"
                initialValue={false}
              >
                <Checkbox>同时删除插件私有数据（不可恢复）</Checkbox>
              </Form.Item>
            </>
          )}
          <Button
            type="primary"
            danger={mode === 'uninstall'}
            htmlType="submit"
            loading={busy}
            disabled={
              ((mode === 'install' || mode === 'upgrade') && !inspection) ||
              (mode === 'enable' && !detail)
            }
          >
            确认
            {mode === 'enable'
              ? '启用'
              : mode === 'uninstall'
                ? '卸载'
                : mode === 'upgrade'
                  ? '升级'
                  : '安装'}
          </Button>
        </Form>
      </Modal>
      <Drawer
        title={`配置 · ${configPlugin?.name ?? ''}`}
        open={configOpen}
        onClose={() => {
          if (!configBusy) setConfigOpen(false);
        }}
        width={LAYOUT.pluginConfigWidth}
        destroyOnHidden
      >
        {renderList && configPlugin && (
          <div style={{ marginBottom: 16 }}>{pluginControls(configPlugin)}</div>
        )}
        {configError && (
          <Alert
            type="error"
            message={configError}
            style={{ marginBottom: SPACING.lg }}
          />
        )}
        {configBusy && !configDetail ? (
          <Flex justify="center" style={{ padding: 32 }}>
            <Spin />
          </Flex>
        ) : !configDetail?.manifest.env?.length ? (
          <Empty description="无配置项" />
        ) : (
          <Form
            key={configDetail.manifest.id}
            form={configForm}
            layout="horizontal"
            labelCol={{ flex: LAYOUT.formLabelWidth }}
            wrapperCol={{ flex: 1 }}
            colon={false}
            initialValues={{
              values: Object.fromEntries(
                (configDetail.manifest.env ?? [])
                  .filter((v) => !v.secret)
                  .map((v) => [v.key, configDetail.config[v.key]?.value ?? '']),
              ),
            }}
            onFinish={saveConfig}
          >
            {configDetail.manifest.env.map((env) => (
              <div key={env.key}>
                <Form.Item
                  name={['values', env.key]}
                  label={env.label || env.key}
                  extra={
                    env.secret
                      ? `${configDetail.config[env.key]?.isSet ? '已设置' : '未设置'}；留空保持原值`
                      : undefined
                  }
                >
                  {env.secret ? (
                    <Input.Password autoComplete="new-password" />
                  ) : (
                    <Input />
                  )}
                </Form.Item>
                {env.secret && (
                  <Form.Item name={['clear', env.key]} valuePropName="checked">
                    <Checkbox>显式清空此秘密</Checkbox>
                  </Form.Item>
                )}
              </div>
            ))}
            <Button type="primary" htmlType="submit" loading={configBusy}>
              保存配置
            </Button>
          </Form>
        )}
      </Drawer>
      {logs && <PluginLogs plugin={logs} close={() => setLogs(undefined)} />}
      <TaskDrawer
        taskId={task?.id ?? null}
        open={!!task}
        title={task?.action ?? '插件任务'}
        onClose={() => setTask(undefined)}
        onSettled={() => void refresh()}
        onSuccess={() => {
          if (task?.action === 'install') {
            setInstalled(task.pluginId);
            message.success('已安装，尚未启用');
          } else message.success('插件任务已完成');
        }}
      />
    </>
  );
}
function PluginLogs({ plugin, close }: { plugin: Plugin; close: () => void }) {
  const [content, setContent] = useState('');
  const [truncated, setTruncated] = useState(false);
  const [error, setError] = useState('');
  const [auto, setAuto] = useState(true);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      if (document.hidden) {
        timer = setTimeout(() => void refresh(), 3000);
        return;
      }
      try {
        const data = await request<{ content: string; truncated: boolean }>(
          `/api/plugins/${plugin.id}/logs`,
          { signal: controller.signal, silentError: true },
        );
        if (controller.signal.aborted) return;
        setContent(data.content);
        setTruncated(data.truncated);
        setError('');
        if (auto) timer = setTimeout(() => void refresh(), 3000);
      } catch (e) {
        if (!controller.signal.aborted)
          setError(e instanceof Error ? e.message : '读取失败');
      }
    };
    void refresh();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [plugin.id, auto, attempt]);
  return (
    <Drawer
      title={`日志 · ${plugin.name}`}
      open
      onClose={close}
      width={LAYOUT.logDrawerWidth}
    >
      <Flex gap={SPACING.lg} style={{ marginBottom: SPACING.lg }}>
        <Button onClick={() => setAttempt((n) => n + 1)}>刷新</Button>
        <Checkbox checked={auto} onChange={(e) => setAuto(e.target.checked)}>
          每 3 秒刷新
        </Checkbox>
      </Flex>
      {error && <Alert type="error" message={error} />}
      {truncated && (
        <Alert type="info" message="仅显示日志尾部，较早内容已截断" />
      )}
      {content ? (
        <LogText
          style={{
            fontFamily: MONO_FONT,
            maxHeight: '65dvh',
            overflow: 'auto',
            whiteSpace: 'pre-wrap',
            overflowWrap: 'anywhere',
          }}
        >
          {content}
        </LogText>
      ) : (
        <Empty description="暂无进程日志" />
      )}
    </Drawer>
  );
}
