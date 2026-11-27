import { useEffect, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Badge,
  Button,
  Checkbox,
  Descriptions,
  Divider,
  Empty,
  Flex,
  Form,
  Grid,
  Modal,
  Skeleton,
  Space,
  Switch,
  Tabs,
  Typography,
  Upload,
} from 'antd';
import { ExternalLink } from 'lucide-react';
import LogText from '../../components/LogText';
import {
  pluginAction,
  pluginDetail,
  pluginStatusLabel,
  savePluginPermissions,
  type Plugin,
} from '../../api/plugin';
import { request } from '../../api/client';
import { MONO_FONT, SPACING } from '../../styles/tokens';
import { saveBlob } from '../../utils/download';
import PluginAccessReview from './PluginAccessReview';
import PluginCapabilities from './PluginCapabilities';
import PluginEnvInput from './PluginEnvInput';
import '../apps/apps.css';

export default function PluginSettings({
  plugin,
  serviceActions,
  onClose,
  onChanged,
}: {
  plugin: Plugin;
  serviceActions: ReactNode;
  onClose: () => void;
  onChanged: () => Promise<void>;
}) {
  const { md } = Grid.useBreakpoint();
  const queryClient = useQueryClient();
  const [tab, setTab] = useState('service');
  const { message, modal } = App.useApp();
  const [isolationBusy, setIsolationBusy] = useState(false);
  const [togglingPermission, setTogglingPermission] = useState<string>();
  const [configBusy, setConfigBusy] = useState(false);
  const [configError, setConfigError] = useState('');
  const [configForm] = Form.useForm();
  const detail = useQuery({
    queryKey: ['plugin-detail', plugin.id, plugin.version],
    queryFn: () => pluginDetail(plugin.id),
    enabled: tab === 'config' || tab === 'capabilities',
  });
  const configDetail = detail.data;
  const setIsolation = async (isolated: boolean) => {
    setIsolationBusy(true);
    try {
      await pluginAction(plugin.id, 'ui-isolation', { isolated });
      await Promise.all([onChanged(), detail.refetch()]);
      message.success(isolated ? '已开启前端隔离' : '已切换为原生嵌入模式');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '切换失败');
    } finally {
      setIsolationBusy(false);
    }
  };
  const togglePermission = async (code: string, enabled: boolean) => {
    if (!configDetail) return;
    const current = new Set(configDetail.disabledPermissions ?? []);
    if (enabled) current.delete(code);
    else current.add(code);
    setTogglingPermission(code);
    try {
      await savePluginPermissions(plugin.id, [...current]);
      await Promise.all([
        detail.refetch(),
        queryClient.invalidateQueries({
          queryKey: ['plugins', 'capabilities', plugin.id],
        }),
        queryClient.invalidateQueries({
          queryKey: ['plugins', 'site-actions'],
        }),
        queryClient.invalidateQueries({
          queryKey: ['plugins', 'resource-actions'],
        }),
        onChanged(),
      ]);
      message.success(enabled ? `已开启权限 ${code}` : `已关闭权限 ${code}`);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '权限切换失败');
    } finally {
      setTogglingPermission(undefined);
    }
  };
  const saveConfig = async (input: {
    values?: Record<string, string>;
    clear?: Record<string, boolean>;
  }) => {
    if (!configDetail) return;
    const values = { ...input.values };
    const fields = configDetail.manifest.env ?? [];
    for (const field of fields) {
      if (field.secret) {
        if (input.clear?.[field.key]) values[field.key] = '';
        else if (!values[field.key]) delete values[field.key];
      }
    }
    setConfigBusy(true);
    setConfigError('');
    try {
      await request(`/api/plugins/${encodeURIComponent(plugin.id)}/config`, {
        method: 'PUT',
        data: { values },
        silentError: true,
      });
      configForm.setFieldsValue({
        values: Object.fromEntries(
          fields
            .filter((field) => field.secret)
            .map((field) => [field.key, '']),
        ),
        clear: Object.fromEntries(
          fields
            .filter((field) => field.secret)
            .map((field) => [field.key, false]),
        ),
      });
      await Promise.all([onChanged(), detail.refetch()]);
      message.success('配置已保存');
    } catch (error) {
      setConfigError(error instanceof Error ? error.message : '保存失败');
    } finally {
      setConfigBusy(false);
    }
  };
  const exportConfig = () => {
    if (!configDetail) return;
    const values = Object.fromEntries(
      (configDetail.manifest.env ?? [])
        .filter((field) => !field.secret)
        .map((field) => [
          field.key,
          configDetail.config[field.key]?.value ?? '',
        ]),
    );
    saveBlob(
      JSON.stringify({ pluginId: configDetail.manifest.id, values }, null, 2),
      `${configDetail.manifest.id}-config.json`,
      'application/json',
    );
  };
  const importConfig = async (file: File) => {
    if (!configDetail) return false;
    try {
      if (file.size > 1 << 20) throw new Error('配置文件过大');
      const data = JSON.parse(await file.text()) as {
        pluginId?: string;
        values?: Record<string, unknown>;
      };
      if (
        data.pluginId !== configDetail.manifest.id ||
        !data.values ||
        Array.isArray(data.values) ||
        typeof data.values !== 'object'
      ) {
        throw new Error('请导入此插件的配置文件');
      }
      const values = Object.fromEntries(
        (configDetail.manifest.env ?? [])
          .filter(
            (field) =>
              !field.secret && typeof data.values?.[field.key] === 'string',
          )
          .map((field) => [field.key, data.values?.[field.key]]),
      );
      configForm.setFieldsValue({ values });
      void message.success('配置已填写，确认后保存');
    } catch (error) {
      void message.error(
        error instanceof Error ? error.message : '配置导入失败',
      );
    }
    return false;
  };

  const pending = detail.error ? (
    <Alert
      type="error"
      message={detail.error.message}
      action={<Button onClick={() => void detail.refetch()}>重试</Button>}
    />
  ) : (
    <Skeleton active />
  );
  return (
    <Modal
      title={`${plugin.name} · 设置`}
      open
      centered
      width={1040}
      footer={null}
      maskClosable={false}
      onCancel={() => {
        if (!configBusy) onClose();
      }}
      styles={{
        body: {
          height: 'min(clamp(480px, 72dvh, 760px), calc(100dvh - 104px))',
          display: 'flex',
          flexDirection: 'column',
          overflow: 'hidden',
          paddingTop: 10,
        },
      }}
    >
      <Tabs
        className="app-settings-tabs"
        tabPosition={md ? 'left' : 'top'}
        activeKey={tab}
        onChange={setTab}
        items={[
          {
            key: 'service',
            label: '服务',
            children: (
              <div className="app-settings-stack">
                <div className="app-settings-section">
                  <div className="app-settings-section-title">基本信息</div>
                  <Descriptions
                    size="small"
                    column={{ xs: 1, sm: 2 }}
                    items={[
                      {
                        key: 'state',
                        label: '运行状态',
                        children: (
                          <Badge
                            status={
                              plugin.runtimeStatus === 'running'
                                ? 'success'
                                : plugin.runtimeStatus === 'failed'
                                  ? 'error'
                                  : 'default'
                            }
                            text={
                              pluginStatusLabel[plugin.runtimeStatus] ??
                              plugin.runtimeStatus
                            }
                          />
                        ),
                      },
                      {
                        key: 'version',
                        label: '当前版本',
                        children: plugin.version,
                      },
                      { key: 'id', label: '插件 ID', children: plugin.id },
                      {
                        key: 'type',
                        label: '插件类型',
                        children:
                          plugin.type === 'process' ? '进程插件' : '静态插件',
                      },
                      {
                        key: 'author',
                        label: '作者',
                        children:
                          plugin.homepage &&
                          /^https?:\/\//i.test(plugin.homepage) ? (
                            <a
                              href={plugin.homepage}
                              target="_blank"
                              rel="noopener noreferrer"
                              style={{
                                display: 'inline-flex',
                                alignItems: 'center',
                                gap: 4,
                              }}
                            >
                              {plugin.author || plugin.homepage}
                              <ExternalLink size={12} strokeWidth={1.85} />
                            </a>
                          ) : (
                            plugin.author || '-'
                          ),
                      },
                    ]}
                  />
                </div>
                {plugin.runtimeError && (
                  <Alert type="info" message={plugin.runtimeError} />
                )}
                <div className="app-settings-section">
                  <div className="app-settings-section-title">服务控制</div>
                  {serviceActions}
                  <Divider style={{ marginBlock: 4 }} />
                  <div className="app-settings-row">
                    <div className="app-settings-row-info">
                      <span className="app-settings-row-title">前端隔离</span>
                      <Typography.Text type="secondary">
                        默认开启，在沙箱中隔离运行插件前端；关闭后以原生模式嵌入面板（仅建议对可信插件关闭）。
                      </Typography.Text>
                    </div>
                    <Switch
                      checked={plugin.uiMode !== 'legacy'}
                      loading={isolationBusy}
                      disabled={!!plugin.activeTaskId}
                      aria-label="前端隔离"
                      onChange={(checked) => {
                        if (checked) {
                          void setIsolation(true);
                        } else {
                          modal.confirm({
                            title: '关闭前端隔离？',
                            content:
                              '关闭后插件前端将与面板同源运行，仅建议对可信插件关闭。切换后重新进入插件页面生效。',
                            okText: '确认关闭',
                            okButtonProps: { danger: true },
                            onOk: () => setIsolation(false),
                          });
                        }
                      }}
                    />
                  </div>
                </div>
              </div>
            ),
          },
          {
            key: 'config',
            label: '配置',
            children: (
              <div className="app-settings-stack">
                {configError && <Alert type="error" message={configError} />}
                {!configDetail ? (
                  pending
                ) : !configDetail.manifest.env?.length ? (
                  <Empty description="无配置项" />
                ) : (
                  <div className="app-settings-section">
                    <div className="app-settings-section-title">
                      环境变量配置
                    </div>
                    <Form
                      className="plugin-config-form"
                      form={configForm}
                      disabled={configBusy || !!plugin.activeTaskId}
                      layout={md ? 'horizontal' : 'vertical'}
                      labelAlign="left"
                      labelWrap
                      labelCol={md ? { flex: '0 0 180px' } : undefined}
                      wrapperCol={md ? { flex: '1 1 0' } : undefined}
                      colon={false}
                      initialValues={{
                        values: Object.fromEntries(
                          (configDetail.manifest.env ?? [])
                            .filter((v) => !v.secret)
                            .map((v) => [
                              v.key,
                              configDetail.config[v.key]?.value ?? '',
                            ]),
                        ),
                      }}
                      onFinish={saveConfig}
                    >
                      {configDetail.manifest.env.map((env) => (
                        <Form.Item
                          key={env.key}
                          label={env.label || env.key}
                          required={env.required}
                          extra={
                            env.help ||
                            (env.secret
                              ? `${configDetail.config[env.key]?.isSet ? '已设置' : '未设置'}；留空保持原值`
                              : undefined)
                          }
                        >
                          <Form.Item name={['values', env.key]} noStyle>
                            <PluginEnvInput field={env} />
                          </Form.Item>
                          {env.secret && (
                            <div className="plugin-config-secret-clear">
                              <Form.Item
                                name={['clear', env.key]}
                                valuePropName="checked"
                                noStyle
                              >
                                <Checkbox>显式清空此秘密</Checkbox>
                              </Form.Item>
                            </div>
                          )}
                        </Form.Item>
                      ))}
                      <div className="plugin-config-actions">
                        <Space wrap>
                          <Button
                            type="primary"
                            htmlType="submit"
                            loading={configBusy}
                          >
                            保存配置
                          </Button>
                          <Button onClick={exportConfig}>导出配置</Button>
                          <Upload
                            accept="application/json,.json"
                            showUploadList={false}
                            beforeUpload={importConfig}
                          >
                            <Button>导入配置</Button>
                          </Upload>
                        </Space>
                      </div>
                    </Form>
                  </div>
                )}
              </div>
            ),
          },
          {
            key: 'authorization',
            label: '授权',
            children: <PluginCapabilities plugin={plugin} />,
          },
          {
            key: 'capabilities',
            label: '能力',
            children: configDetail ? (
              <PluginAccessReview
                startCollapsed={false}
                manifest={configDetail.manifest}
                preview={configDetail.accessPreview}
                onTogglePermission={togglePermission}
                togglingPermission={togglingPermission}
              />
            ) : (
              pending
            ),
          },
          {
            key: 'logs',
            label: '日志',
            children:
              tab === 'logs' &&
              (plugin.type === 'process' ? (
                <PluginLogs plugin={plugin} />
              ) : (
                <Empty description="此插件没有进程日志" />
              )),
          },
        ]}
      />
    </Modal>
  );
}

function PluginLogs({ plugin }: { plugin: Plugin }) {
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
    <div>
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
            whiteSpace: 'pre-wrap',
            overflowWrap: 'anywhere',
          }}
        >
          {content}
        </LogText>
      ) : (
        <Empty description="暂无进程日志" />
      )}
    </div>
  );
}
