import { useEffect, useRef, useState } from 'react';
import {
  Alert,
  AutoComplete,
  Button,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Switch,
} from 'antd';
import { FolderOpenOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  applicationVersions,
  installApplication,
  runtimeEnvironments,
  runtimeEnvironmentLabel,
  type RuntimeEnvironment,
} from '../../api/software';
import { waitForTask } from '../../api/task';
import { useMessageBox } from '../../stores/messageBox';
import {
  nodePackages,
  type ProjectRuntime,
  type RuntimeLaunch,
} from '../../api/website';
import ProjectEntryPicker from './ProjectEntryPicker';

const goBinaryCommand = 'exec "$PROJECT_FILE"';
const goSourceCommand =
  'GO111MODULE="${GO111MODULE:-auto}" exec go run "${PROJECT_FILE%/*}"';

export default function RuntimeFields({
  runtime,
  projectRoot,
  siteId,
}: {
  runtime: ProjectRuntime;
  projectRoot?: string;
  siteId?: string;
}) {
  const form = Form.useFormInstance();
  const client = useQueryClient();
  const [version, setVersion] = useState<string>();
  const [selectingEntry, setSelectingEntry] = useState(false);
  const selectedEnvironment = Form.useWatch<string | undefined>(
    'runtimeEnvironment',
    form,
  );
  const launch =
    Form.useWatch<RuntimeLaunch>('runtimeLaunch', form) || 'command';
  const autoPort = runtime === 'python' && launch === 'command';
  const configuredRoot = Form.useWatch<string>('root', form);
  const entryRoot = configuredRoot || projectRoot;
  const goFile = Form.useWatch<string>('runtimeFile', form);
  function selectGoFile(file: string) {
    const command = form.getFieldValue('runtimeCommand')?.trim();
    form.setFieldValue('runtimeFile', file);
    if (
      !command ||
      command === goBinaryCommand ||
      command === goSourceCommand
    ) {
      const defaultCommand = file.endsWith('.go')
        ? goSourceCommand
        : goBinaryCommand;
      form.setFieldValue('runtimeCommand', file ? defaultCommand : '');
    }
  }
  const manifest = useQuery({
    queryKey: ['node-project-manifest', siteId],
    queryFn: ({ signal }) => nodePackages(siteId!, signal),
    enabled: !!siteId && runtime === 'nodejs',
  });
  useEffect(() => {
    if (
      manifest.data?.packageManager &&
      !form.getFieldValue('runtimePackageManager')
    ) {
      form.setFieldValue('runtimePackageManager', manifest.data.packageManager);
    }
  }, [form, manifest.data?.packageManager]);
  const [submitting, setSubmitting] = useState(false);
  const [taskId, setTaskId] = useState<string>();
  const applied = useRef<string>();
  const environments = useQuery({
    queryKey: ['runtime-environments', runtime],
    queryFn: ({ signal }) => runtimeEnvironments(runtime, signal),
  });
  const releases = useQuery({
    queryKey: ['app-versions', runtime, 'official', ''],
    queryFn: ({ signal }) =>
      applicationVersions(runtime, false, { source: 'official' }, signal),
  });
  const progress = useQuery({
    queryKey: ['runtime-install-task', taskId],
    queryFn: ({ signal }) => waitForTask(taskId!, signal),
    enabled: Boolean(taskId),
  });
  const task = progress.data;
  useEffect(() => {
    if (!task || task.status !== 'success' || applied.current === task.taskId)
      return;
    const result = task.result as { environmentId: string; version: string };
    applied.current = task.taskId;
    client.setQueryData<{ list: RuntimeEnvironment[] }>(
      ['runtime-environments', runtime],
      (previous) => ({
        list: [
          ...(previous?.list.filter((env) => env.id !== result.environmentId) ??
            []),
          { id: result.environmentId, version: result.version, projects: [] },
        ],
      }),
    );
    form.setFieldValue('runtimeEnvironment', result.environmentId);
    void client.invalidateQueries({
      queryKey: ['runtime-environments', runtime],
    });
    void client.invalidateQueries({ queryKey: ['software-catalog'] });
  }, [client, form, runtime, task]);
  const running =
    submitting ||
    Boolean(
      taskId && (!task || task.status === 'running') && !progress.isError,
    );
  const items = environments.data?.list ?? [];
  const installed = new Set(items.map((item) => item.version));
  const downloadOptions = releases.data?.list ?? [];
  const activeVersion =
    version ?? items.find((item) => item.id === selectedEnvironment)?.version;
  const [major, minor] = (activeVersion ?? '').split('.').map(Number);
  const supportsUwsgi =
    !activeVersion || (major === 3 && minor >= 8 && minor <= 14);
  const choices = items.filter((item) => item.version === activeVersion);
  const versionOptions = [
    ...downloadOptions.map((item) => ({
      value: item.version,
      label: item.version,
      disabled: Boolean(item.unavailableReason) && !installed.has(item.version),
    })),
    ...[...installed]
      .filter(
        (value) => !downloadOptions.some((item) => item.version === value),
      )
      .map((value) => ({ value, label: value })),
  ].sort((a, b) => b.value.localeCompare(a.value, 'en', { numeric: true }));
  const commands = {
    nodejs: 'npm ci && npm start',
    python: 'pip install -r requirements.txt && python app.py',
    go: './server',
  };

  async function createEnvironment() {
    if (!activeVersion) return;
    setSubmitting(true);
    try {
      const result = await installApplication(runtime, activeVersion, {
        source: 'official',
      });
      setTaskId(result.taskId);
      void client.invalidateQueries({ queryKey: ['software-tasks'] });
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      <Form.Item label="运行环境" required>
        <Space direction="vertical" style={{ width: '100%' }}>
          <Space.Compact style={{ width: '100%' }}>
            <Select
              style={{ flex: 1 }}
              aria-label="运行环境版本"
              placeholder="请选择版本"
              value={activeVersion}
              options={versionOptions}
              showSearch
              optionFilterProp="label"
              loading={releases.isFetching || environments.isFetching}
              disabled={running}
              onChange={(value) => {
                setVersion(value);
                const available = items.filter(
                  (item) => item.version === value,
                );
                form.setFieldValue(
                  'runtimeEnvironment',
                  available.length === 1 ? available[0].id : undefined,
                );
              }}
            />
            <Button
              type="primary"
              loading={running}
              disabled={!activeVersion || environments.isPending}
              onClick={() => void createEnvironment()}
            >
              {choices.length ? '创建新环境' : '创建环境'}
            </Button>
          </Space.Compact>
          <Form.Item
            name="runtimeEnvironment"
            noStyle
            rules={[{ required: true, message: '请选择或创建运行环境' }]}
          >
            {choices.length ? (
              <Select
                style={{ width: '100%' }}
                aria-label="已有运行环境"
                placeholder="请选择已有环境"
                disabled={running}
                showSearch
                optionFilterProp="label"
                options={choices.map((item) => {
                  const projects = item.projects
                    .map((project) => project.name)
                    .join('、');
                  const label = projects
                    ? `${runtimeEnvironmentLabel(item)} · 关联项目：${projects}`
                    : runtimeEnvironmentLabel(item);
                  return { value: item.id, label, title: label };
                })}
              />
            ) : (
              <input type="hidden" />
            )}
          </Form.Item>
          {(environments.error || releases.error) && (
            <Alert
              showIcon
              type="error"
              message={(environments.error || releases.error)?.message}
            />
          )}
          {progress.error && (
            <Alert
              showIcon
              type="error"
              message={progress.error.message || '获取环境创建进度失败'}
              action={
                <Button size="small" onClick={() => void progress.refetch()}>
                  重试
                </Button>
              }
            />
          )}
          {task && task.status !== 'running' && (
            <Alert
              showIcon
              type={task.status === 'success' ? 'success' : 'error'}
              message={
                task.status === 'success'
                  ? '环境已创建并选中'
                  : task.errorMsg || '创建环境失败'
              }
              action={
                task.status !== 'success' && (
                  <Button
                    size="small"
                    onClick={() => useMessageBox.getState().show(task.taskId)}
                  >
                    查看日志
                  </Button>
                )
              }
            />
          )}
        </Space>
      </Form.Item>
      {projectRoot && (
        <>
          {runtime === 'python' && (
            <Form.Item
              name="runtimeLaunch"
              label="启动方式"
              rules={[
                { required: true },
                {
                  validator: (_, value) =>
                    value === 'uwsgi' && !supportsUwsgi
                      ? Promise.reject(
                          new Error('uWSGI 支持 Python 3.8 至 3.14'),
                        )
                      : Promise.resolve(),
                },
              ]}
            >
              <Select
                options={[
                  { value: 'command', label: '命令行启动' },
                  {
                    value: 'uwsgi',
                    label: supportsUwsgi ? 'uWSGI' : 'uWSGI（Python 3.8–3.14）',
                    disabled: !supportsUwsgi,
                  },
                  { value: 'gunicorn', label: 'Gunicorn' },
                ]}
              />
            </Form.Item>
          )}
          {runtime === 'nodejs' && (
            <>
              <Form.Item
                name="runtimeLaunch"
                label="启动方式"
                rules={[{ required: true }]}
              >
                <Select
                  options={[
                    { value: 'script', label: '运行 package.json 脚本' },
                    { value: 'file', label: '运行入口文件' },
                    { value: 'command', label: '命令行启动' },
                  ]}
                />
              </Form.Item>
              <Form.Item
                name="runtimePackageManager"
                label="包管理器"
                extra={
                  manifest.data?.packageManager
                    ? `项目检测到 ${manifest.data.packageManager}`
                    : undefined
                }
              >
                <Select
                  placeholder={manifest.data?.packageManager || 'npm'}
                  options={[
                    { value: 'npm', label: 'npm' },
                    { value: 'pnpm', label: 'pnpm' },
                    { value: 'yarn', label: 'Yarn' },
                  ]}
                />
              </Form.Item>
            </>
          )}
          {runtime === 'nodejs' && launch === 'script' && (
            <Form.Item
              name="runtimeScript"
              label="启动脚本"
              rules={[{ required: true, whitespace: true }]}
            >
              <AutoComplete
                placeholder="例如 start"
                options={manifest.data?.scripts.map((value) => ({ value }))}
              />
            </Form.Item>
          )}
          {(runtime === 'go' ||
            (runtime === 'nodejs' && launch === 'file') ||
            (runtime === 'python' && !autoPort)) && (
            <Form.Item
              label={runtime === 'go' ? '项目执行文件' : '入口文件'}
              required={runtime !== 'go'}
            >
              <Space.Compact style={{ width: '100%' }}>
                <Form.Item
                  name={
                    runtime === 'python' ? 'runtimeEntryFile' : 'runtimeFile'
                  }
                  noStyle
                  getValueProps={
                    runtime === 'go'
                      ? (file?: string) => {
                          const path =
                            file && entryRoot
                              ? `${entryRoot.replace(/\/+$/, '')}/${file}`
                              : (file ?? '');
                          return { value: path, title: path };
                        }
                      : undefined
                  }
                  rules={[
                    { required: runtime !== 'go', message: '请选择入口文件' },
                  ]}
                >
                  <Input readOnly placeholder="从项目目录选择文件" />
                </Form.Item>
                <Button
                  icon={<FolderOpenOutlined />}
                  onClick={() => setSelectingEntry(true)}
                >
                  选择文件
                </Button>
                {runtime === 'go' && goFile && (
                  <Button onClick={() => selectGoFile('')}>清除</Button>
                )}
              </Space.Compact>
            </Form.Item>
          )}
          {(runtime === 'go' || launch === 'command') && (
            <Form.Item
              name="runtimeCommand"
              label={runtime === 'go' ? '项目执行命令' : '启动命令'}
              rules={[
                { required: !(runtime === 'go' && goFile), whitespace: true },
              ]}
              extra={
                autoPort
                  ? '请以前台方式启动服务并监听 0.0.0.0，面板会自动识别 HTTP 端口。'
                  : runtime === 'go'
                    ? '.go 文件默认编译并运行所在的 Go 包。$PROJECT_FILE 指向选中的文件。'
                    : '命令在项目目录内执行。请监听 0.0.0.0 和下方设置的端口。'
              }
            >
              <Input placeholder={commands[runtime]} maxLength={8192} />
            </Form.Item>
          )}
          {runtime === 'python' && !autoPort && (
            <Form.Item
              name="runtimeCallable"
              label="应用对象"
              rules={[
                {
                  required: true,
                  whitespace: true,
                  pattern: /^[A-Za-z_]\w*$/,
                  message: '请填写应用对象，例如 app 或 application',
                },
              ]}
            >
              <Input placeholder="例如 app 或 application" maxLength={128} />
            </Form.Item>
          )}
          {selectingEntry && (
            <ProjectEntryPicker
              root={entryRoot || projectRoot}
              runtime={runtime}
              onClose={() => setSelectingEntry(false)}
              onSelect={(file) => {
                if (runtime === 'python') {
                  form.setFieldsValue({
                    runtimeEntryFile: file,
                    runtimeCallable:
                      file.split('/').at(-1) === 'wsgi.py'
                        ? 'application'
                        : 'app',
                  });
                } else if (runtime === 'go') {
                  selectGoFile(file);
                } else {
                  form.setFieldValue('runtimeFile', file);
                }
                setSelectingEntry(false);
              }}
            />
          )}
          {!autoPort && (
            <Form.Item
              name="runtimePort"
              label="项目端口"
              rules={[{ required: true, type: 'number', min: 1, max: 65535 }]}
            >
              <InputNumber min={1} max={65535} precision={0} />
            </Form.Item>
          )}
          <Form.Item name="runtimeEnv" label="环境变量">
            <Input.TextArea
              placeholder="每行一个 NAME=value"
              autoSize={{ minRows: 2, maxRows: 6 }}
              maxLength={32768}
            />
          </Form.Item>
          <Form.Item
            name="runtimeAutoStart"
            label="开机自启动"
            valuePropName="checked"
            extra="服务器开机时启动已启用的项目。"
          >
            <Switch />
          </Form.Item>
        </>
      )}
    </>
  );
}
