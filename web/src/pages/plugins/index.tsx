import { useState, type ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  App,
  Alert,
  Button,
  Card,
  Checkbox,
  Flex,
  Form,
  Modal,
  Popconfirm,
  Space,
  Tag,
  Tooltip,
  Typography,
  Upload,
} from 'antd';
import { useQueryClient } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  inspectPlugin,
  pluginAction,
  pluginDetail,
  pluginStatusLabel,
  usePluginList,
  type Detail,
  type Inspection,
  type Plugin,
} from '../../api/plugin';
import { request } from '../../api/client';
import TaskDrawer from '../../components/TaskDrawer';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';
import PluginSettings from './PluginSettings';
import PluginAccessReview from './PluginAccessReview';

type Mode = 'install' | 'enable' | 'uninstall';
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
  const [confirmedUploadId, setConfirmedUploadId] = useState<string>();
  const [signature, setSignature] = useState<File>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [settingsPluginId, setSettingsPluginId] = useState<string>();
  const [task, setTask] = useState<{
    id: string;
    action: string;
    pluginId?: string;
  }>();
  const [installed, setInstalled] = useState<string>();
  const [form] = Form.useForm();
  const existingPlugin =
    mode === 'install' && inspection
      ? query.data?.find((item) => item.id === inspection.manifest.id)
      : undefined;
  const isUpgrade = Boolean(
    mode === 'install' &&
    inspection &&
    (inspection.installedVersion || existingPlugin),
  );
  const currentVersion =
    inspection?.installedVersion || existingPlugin?.version || '';
  const needsRiskConfirmation = mode === 'install';
  const riskConfirmed =
    !!inspection && inspection.uploadId === confirmedUploadId;
  const refresh = () => client.invalidateQueries({ queryKey: ['plugins'] });
  const open = async (next: Mode, plugin?: Plugin) => {
    setMode(next);
    setSelected(plugin);
    setDetail(undefined);
    setInspection(undefined);
    setConfirmedUploadId(undefined);
    setSignature(undefined);
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
    setConfirmedUploadId(undefined);
    setDetail(undefined);
    setSelected(undefined);
    try {
      const result = await inspectPlugin(file, signature);
      const existing = query.data?.find(
        (item) => item.id === result.manifest.id,
      );
      if (result.installedVersion || existing) {
        setSelected(existing);
      }
      setInspection(result);
    } catch (e) {
      setError(e instanceof Error ? e.message : '检查失败');
    } finally {
      setBusy(false);
    }
  };
  const submit = async (values: { purgeData?: boolean }) => {
    if (!mode || busy || (needsRiskConfirmation && !riskConfirmed)) return;
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
            : { uploadId: inspection!.uploadId };
        const targetId = inspection?.manifest.id ?? selected?.id ?? '';
        const result =
          mode === 'install'
            ? isUpgrade
              ? await pluginAction<{ taskId: string }>(
                  targetId,
                  'upgrade',
                  data,
                )
              : await request<{ taskId: string }>('/api/plugins/install', {
                  method: 'POST',
                  data,
                  silentError: true,
                })
            : await pluginAction<{ taskId: string }>(selected!.id, mode, data);
        setTask({
          id: result.taskId,
          action: isUpgrade ? 'upgrade' : mode,
          pluginId: targetId,
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
  const settingsPlugin = query.data?.find(
    (plugin) => plugin.id === settingsPluginId,
  );
  const rollback = async (plugin: Plugin) => {
    try {
      const previous = await request<{
        version: string;
        revokedGrants?: string[];
      }>(`/api/plugins/${encodeURIComponent(plugin.id)}/rollback`, {
        silentError: true,
      });
      modal.confirm({
        title: `恢复「${plugin.name}」至 ${previous.version}？`,
        content: (
          <>
            <p>将以快照还原插件包、配置、KV 和插件私有数据。</p>
            {!!previous.revokedGrants?.length && (
              <p>
                需要重新授权或设置的项目 · {previous.revokedGrants.join('、')}
              </p>
            )}
          </>
        ),
        okText: '恢复',
        okButtonProps: { danger: true },
        onOk: async () => {
          try {
            const result = await pluginAction<{ taskId: string }>(
              plugin.id,
              'rollback',
              { version: previous.version },
            );
            setTask({
              id: result.taskId,
              action: '恢复插件版本',
              pluginId: plugin.id,
            });
          } catch (error) {
            void message.error(
              error instanceof Error ? error.message : '恢复失败',
            );
            throw error;
          }
        },
      });
    } catch (error) {
      void message.error(
        error instanceof Error ? error.message : '读取快照失败',
      );
    }
  };
  const reviewed = mode === 'enable' ? detail : inspection;
  const permissionChanges = inspection?.permissionChanges;
  const hasPermissionChanges = !!(
    permissionChanges?.permissions?.length ||
    permissionChanges?.execAllow?.length ||
    permissionChanges?.fsRoots?.length ||
    inspection?.revokedGrants?.length
  );
  const pluginControls = (p: Plugin) => {
    const disabledByTask = !!p.activeTaskId;
    return (
      <Space wrap>
        {p.activeTaskId && (
          <Button
            onClick={() => setTask({ id: p.activeTaskId, action: '插件任务' })}
          >
            查看任务
          </Button>
        )}
        {p.runtimeStatus === 'running' && (
          <Button
            disabled={disabledByTask}
            onClick={() => {
              const target = `/p/${encodeURIComponent(p.id)}`;
              if (p.openMode === 'new_tab') {
                window.open(target, '_blank', 'noopener,noreferrer');
              } else {
                setSettingsPluginId(undefined);
                navigate(target);
              }
            }}
          >
            打开
          </Button>
        )}
        {p.enabled ? (
          <Popconfirm
            title="确认停止插件并断开其连接？"
            onConfirm={() => disable(p)}
          >
            <Button disabled={disabledByTask}>停止</Button>
          </Popconfirm>
        ) : (
          <Button
            type="primary"
            disabled={disabledByTask}
            onClick={() => void open('enable', p)}
          >
            启动
          </Button>
        )}
        {p.type === 'process' && (
          <Button
            disabled={disabledByTask || !p.enabled}
            onClick={() =>
              modal.confirm({
                title:
                  p.runtimeStatus === 'failed'
                    ? '确认重试启动插件？'
                    : '确认重启插件？',
                onOk: async () => {
                  try {
                    await pluginAction(p.id, 'restart');
                    await refresh();
                    message.success('插件已重启');
                  } catch (e) {
                    message.error(e instanceof Error ? e.message : '重启失败');
                  }
                },
              })
            }
          >
            {p.runtimeStatus === 'failed' ? '重试启动' : '重启'}
          </Button>
        )}
        <Button disabled={disabledByTask} onClick={() => void rollback(p)}>
          恢复历史版本
        </Button>
        <Button
          danger
          disabled={disabledByTask}
          onClick={() => void open('uninstall', p)}
        >
          卸载
        </Button>
      </Space>
    );
  };
  const actions = (plugin: Plugin) => (
    <Space size={12}>
      <Button
        type="link"
        style={{ padding: 0 }}
        onClick={() => setSettingsPluginId(plugin.id)}
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
                  <Tooltip title={p.runtimeError}>
                    <Tag
                      color={
                        p.runtimeStatus === 'running'
                          ? 'success'
                          : p.runtimeStatus === 'failed'
                            ? 'error'
                            : 'default'
                      }
                    >
                      {pluginStatusLabel[p.runtimeStatus] ?? p.runtimeStatus}
                    </Tag>
                  </Tooltip>
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
            ? isUpgrade
              ? `升级插件 · ${currentVersion} → ${inspection?.manifest.version ?? '待检查'}`
              : '导入扩展应用'
            : mode === 'enable'
              ? '启用插件'
              : '卸载插件'
        }
        open={!!mode}
        onCancel={() => {
          if (!busy) setMode(undefined);
        }}
        centered
        footer={
          <Flex
            align="center"
            wrap
            gap={SPACING.sm}
            style={{ textAlign: 'start' }}
          >
            {needsRiskConfirmation && inspection && (
              <Checkbox
                checked={riskConfirmed}
                disabled={busy}
                onChange={(event) =>
                  setConfirmedUploadId(
                    event.target.checked ? inspection.uploadId : undefined,
                  )
                }
              >
                我已查看插件权限和风险提示，确认继续。
              </Checkbox>
            )}
            <Button
              type="primary"
              danger={mode === 'uninstall'}
              htmlType="submit"
              form="plugin-operation-form"
              loading={busy}
              disabled={
                (needsRiskConfirmation && !riskConfirmed) ||
                (mode === 'enable' && !detail)
              }
              style={{ marginInlineStart: 'auto' }}
            >
              确认
              {mode === 'enable'
                ? '启用'
                : mode === 'uninstall'
                  ? '卸载'
                  : isUpgrade
                    ? '升级'
                    : '安装'}
            </Button>
          </Flex>
        }
        destroyOnHidden
        width={
          mode === 'uninstall' ? LAYOUT.modalWidth : LAYOUT.databaseModalWidth
        }
        styles={{
          body: {
            maxHeight: 'min(68dvh, calc(100dvh - 150px))',
            overflowY: 'auto',
          },
        }}
      >
        <Form
          id="plugin-operation-form"
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
          {mode === 'install' && (
            <Space wrap>
              <Upload
                beforeUpload={(file) => {
                  setSignature(file);
                  setInspection(undefined);
                  setError('');
                  return false;
                }}
                accept=".sig"
                showUploadList={false}
                disabled={busy}
              >
                <Button>选择签名文件（可选）</Button>
              </Upload>
              {signature && (
                <Tag
                  closable={!busy}
                  onClose={() => {
                    setSignature(undefined);
                    setInspection(undefined);
                    setError('');
                  }}
                >
                  {signature.name}
                </Tag>
              )}
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
              {inspection?.signatureVerified && (
                <Tag color="success">签名已验证</Tag>
              )}
            </Space>
          )}
          {inspection && isUpgrade && (
            <Alert
              type="info"
              showIcon
              message={`检测到「${inspection.manifest.name}」已安装（当前版本 ${currentVersion}），本次导入将执行升级并自动保留历史快照。`}
              style={{ marginTop: SPACING.md, marginBottom: SPACING.md }}
            />
          )}
          {reviewed && (
            <PluginAccessReview
              manifest={reviewed.manifest}
              preview={reviewed.accessPreview}
              startCollapsed={needsRiskConfirmation}
            />
          )}
          {inspection && isUpgrade && hasPermissionChanges && (
            <Alert
              type="info"
              message="本次授权变化"
              description={
                <>
                  {!!permissionChanges?.permissions?.length && (
                    <div>
                      <Typography.Text strong>新增权限</Typography.Text>{' '}
                      {permissionChanges.permissions
                        ?.map(
                          (code) =>
                            inspection.accessPreview.permissions.find(
                              (item) => item.code === code,
                            )?.label ?? code,
                        )
                        .join('、')}
                    </div>
                  )}
                  {!!permissionChanges?.execAllow?.length && (
                    <div>
                      <Typography.Text strong>新增命令</Typography.Text>{' '}
                      {permissionChanges.execAllow.join('、')}
                    </div>
                  )}
                  {!!permissionChanges?.fsRoots?.length && (
                    <div>
                      <Typography.Text strong>新增或扩大的目录</Typography.Text>{' '}
                      {permissionChanges.fsRoots.join('、')}
                    </div>
                  )}
                  {!!inspection.revokedGrants?.length && (
                    <div>
                      需要重新授权或设置的项目 ·{' '}
                      {inspection.revokedGrants.join('、')}
                    </div>
                  )}
                </>
              }
              style={{ marginBottom: SPACING.lg }}
            />
          )}
          {inspection && isUpgrade && (
            <Typography.Text type="secondary">
              升级快照仅覆盖面板托管的插件包、配置和数据；插件设置项可在升级完成后前往「设置
              → 配置」修改。
            </Typography.Text>
          )}
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
        </Form>
      </Modal>
      {settingsPlugin && (
        <PluginSettings
          key={`${settingsPlugin.id}:${settingsPlugin.version}`}
          plugin={settingsPlugin}
          serviceActions={pluginControls(settingsPlugin)}
          onClose={() => setSettingsPluginId(undefined)}
          onChanged={refresh}
        />
      )}
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
          } else {
            if (
              task?.action === 'uninstall' &&
              task.pluginId === settingsPluginId
            )
              setSettingsPluginId(undefined);
            message.success('插件任务已完成');
          }
        }}
      />
    </>
  );
}
