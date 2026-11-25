import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Dropdown,
  Flex,
  Skeleton,
  Space,
  Tabs,
  Typography,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Ellipsis } from 'lucide-react';
import {
  composeProject,
  saveComposeFiles,
  validateComposeProject,
  type ComposeProjectDetail,
} from '../../../api/composeProjects';
import { waitForTask } from '../../../api/task';
import ComposeEnvEditor from './ComposeEnvEditor';
import ComposeLogsTab from './ComposeLogsTab';
import ComposeServicesTab, { composeServiceName } from './ComposeServicesTab';
import ComposeYaml, { composeYamlError } from './ComposeYaml';

const ComposeRevisionModal = lazy(() => import('./ComposeRevisionModal'));

type FileChange = { kind: 'yaml' | 'env'; value: string; apply: boolean };
type PendingSave = { id: string } & (
  FileChange | { kind: 'rollback'; draft?: string; apply: boolean }
);
type SaveNotice = {
  type: 'success' | 'warning' | 'error';
  message: string;
  detail?: string;
};

export default function ComposeProjectDetail({
  project,
  active,
  online,
  onTask,
  onChanged,
  onContainer,
  onTerminal,
  onDirtyChange,
  tab,
  onTabChange,
}: {
  project: ComposeProjectDetail;
  active: boolean;
  online: boolean;
  onTask: (id: string) => void;
  onChanged: () => void;
  onContainer: (id: string, name: string, tab: string) => void;
  onTerminal: (id: string) => void;
  onDirtyChange: (dirty: boolean) => void;
  tab: string;
  onTabChange: (tab: string) => void;
}) {
  const { message } = App.useApp();
  const client = useQueryClient();
  const [draftYaml, setDraftYaml] = useState<string>();
  const [env, setEnv] = useState<{ original: string; value: string }>();
  const [showEnv, setShowEnv] = useState(false);
  if (tab !== 'env' && showEnv) setShowEnv(false);
  const [history, setHistory] = useState(false);
  const [busy, setBusy] = useState<'validate' | 'reveal'>();
  const [pending, setPending] = useState<PendingSave>();
  const [notice, setNotice] = useState<SaveNotice>();
  const handled = useRef<string>();
  const yaml = draftYaml ?? project.yaml;
  const containers = project.containers;
  const services = [
    ...new Set(containers.map(composeServiceName).filter(Boolean)),
  ].sort();
  const formatFiles = project.composeFiles.slice(1);
  const filesEditable = !project.fileError;
  const yamlDirty = yaml !== project.yaml;
  const envDirty = !!env && env.value !== env.original;

  useEffect(() => {
    onDirtyChange(yamlDirty || envDirty);
  }, [yamlDirty, envDirty, onDirtyChange]);

  const save = useMutation({
    mutationFn: (change: FileChange) =>
      saveComposeFiles(project.name, {
        [change.kind]: change.value,
        apply: change.apply,
      }),
    onSuccess({ taskId }, change) {
      setPending({ id: taskId, ...change });
      onTask(taskId);
    },
  });
  const progress = useQuery({
    queryKey: ['compose-project-save-task', pending?.id],
    queryFn: ({ signal }) => waitForTask(pending!.id, signal),
    enabled: !!pending,
  });
  const task = progress.data;

  useEffect(() => {
    if (
      !pending ||
      !task ||
      task.status === 'running' ||
      handled.current === task.taskId
    )
      return;
    handled.current = task.taskId;
    const result = task.result as
      { project?: string; saved?: boolean; applied?: boolean } | undefined;
    const saved = result?.project === project.name && result.saved === true;
    const applied = saved && result?.applied === true;
    const key = ['compose-project-detail', project.name];
    if (saved && pending.kind === 'yaml') {
      client.setQueryData<ComposeProjectDetail>(
        key,
        (current) => current && { ...current, yaml: pending.value },
      );
    }
    // 任务抽屉关闭后，仍以任务返回的落盘结果更新已保存版本。
    void Promise.all([
      client.invalidateQueries({ queryKey: key }),
      client.invalidateQueries({ queryKey: ['compose-projects'] }),
      client.invalidateQueries({
        queryKey: ['compose-project-revisions', project.name],
      }),
    ]).then(() => {
      if (saved && pending.kind === 'yaml') {
        setDraftYaml((draft) => (draft === pending.value ? undefined : draft));
      } else if (saved && pending.kind === 'env') {
        setEnv((current) => current && { ...current, original: pending.value });
      } else if (saved && pending.kind === 'rollback') {
        setDraftYaml((draft) => (draft === pending.draft ? undefined : draft));
        setEnv(undefined);
        setShowEnv(false);
      }

      const label =
        pending.kind === 'rollback' ? '已恢复历史版本' : '配置已保存';
      setNotice(
        !saved
          ? {
              type: 'error',
              message:
                pending.kind === 'rollback' ? '未能恢复历史版本' : '配置未保存',
              detail: task.errorMsg,
            }
          : pending.apply && !applied
            ? {
                type: 'warning',
                message: `${label}，应用失败`,
                detail: task.errorMsg,
              }
            : { type: 'success', message: applied ? `${label}并应用` : label },
      );
      setPending(undefined);
    });
  }, [client, pending, project.name, task]);

  const writing = save.isPending ? save.variables : pending;
  const validateYaml = () => {
    if (yaml.trim() && !composeYamlError(yaml)) return true;
    void message.error('请先修正 Compose YAML');
    return false;
  };

  async function validate() {
    if (!validateYaml()) return;
    setBusy('validate');
    try {
      const result = await validateComposeProject({
        name: project.name,
        yaml,
        env: envDirty ? env?.value : undefined,
      });
      if (result.previewOnly) {
        void message.info('预检通过。保存时将按实际项目目录重新校验。');
      } else {
        void message.success('Compose 配置校验通过');
      }
    } catch {
      // 请求错误由统一 API 提示展示。
    } finally {
      setBusy(undefined);
    }
  }

  function submit(kind: FileChange['kind'], apply: boolean) {
    if (kind === 'yaml' && !validateYaml()) return;
    setNotice(undefined);
    save.mutate({ kind, value: kind === 'yaml' ? yaml : env!.value, apply });
  }

  async function reveal() {
    if (showEnv) {
      setShowEnv(false);
      return;
    }
    setBusy('reveal');
    try {
      if (!env || (env.original === env.value && writing?.kind !== 'env')) {
        const visible = await composeProject(project.name, undefined, true);
        if (visible.fileError) {
          void message.error(visible.fileError);
          return;
        }
        setEnv({ original: visible.env, value: visible.env });
      }
      setShowEnv(true);
    } catch {
      // 请求错误由统一 API 提示展示。
    } finally {
      setBusy(undefined);
    }
  }

  return (
    <>
      <div className="compose-project-services">
        <ComposeServicesTab
          active={active && online}
          containers={containers}
          onChanged={onChanged}
          onContainer={onContainer}
          onTerminal={onTerminal}
        />
      </div>
      {notice && (
        <Alert
          className="compose-save-notice"
          type={notice.type}
          message={notice.message}
          description={notice.detail || undefined}
          showIcon
          closable
          onClose={() => setNotice(undefined)}
        />
      )}
      {pending && progress.error && (
        <Alert
          className="compose-save-notice"
          showIcon
          type="warning"
          message="暂时无法读取任务状态"
          action={
            <Button size="small" onClick={() => void progress.refetch()}>
              重试
            </Button>
          }
        />
      )}
      <Tabs
        rootClassName="compose-detail-tabs"
        size="small"
        activeKey={tab}
        onChange={(key) => {
          if (key !== 'env') setShowEnv(false);
          onTabChange(key);
        }}
        tabBarExtraContent={
          tab === 'editor' ? (
            <Space size={6} className="compose-editor-actions">
              <Button
                type="primary"
                loading={writing?.kind === 'yaml' && !writing.apply}
                disabled={!filesEditable || !yamlDirty || !!busy || !!writing}
                onClick={() => submit('yaml', false)}
              >
                保存
              </Button>
              <Dropdown
                trigger={['click']}
                disabled={!!busy || !!writing}
                menu={{
                  items: [
                    {
                      key: 'apply',
                      label: '保存并应用',
                      disabled: !online || !filesEditable || !yamlDirty,
                    },
                    {
                      key: 'validate',
                      label: '校验配置',
                      disabled: !filesEditable,
                    },
                    { key: 'history', label: '版本历史' },
                  ],
                  onClick: ({ key }) => {
                    if (key === 'apply') submit('yaml', true);
                    if (key === 'validate') void validate();
                    if (key === 'history') setHistory(true);
                  },
                }}
              >
                <Button
                  aria-label="更多编排文件操作"
                  icon={<Ellipsis size={16} />}
                  loading={
                    busy === 'validate' ||
                    (writing?.kind === 'yaml' && writing.apply)
                  }
                />
              </Dropdown>
            </Space>
          ) : undefined
        }
        items={[
          {
            key: 'editor',
            label: '编排文件',
            children: (
              <Flex vertical gap={12}>
                {project.fileError && (
                  <Alert type="warning" showIcon message={project.fileError} />
                )}
                {!!formatFiles.length && (
                  <Alert
                    type="info"
                    message={`同时加载覆盖文件：${formatFiles.join('、')}`}
                  />
                )}
                {filesEditable && (
                  <ComposeYaml
                    id={`compose-${project.name}`}
                    dark
                    value={yaml}
                    onChange={(value) =>
                      setDraftYaml(value === project.yaml ? undefined : value)
                    }
                  />
                )}
                {project.composeFile && (
                  <Typography.Text className="compose-file-path" code copyable>
                    {project.composeFile}
                  </Typography.Text>
                )}
              </Flex>
            ),
          },
          {
            key: 'logs',
            label: '项目日志',
            children: online ? (
              <ComposeLogsTab
                name={project.name}
                services={services}
                containerRevision={containers
                  .map((row) => `${row.Id}:${row.State}`)
                  .sort()
                  .join(',')}
                active={active && tab === 'logs'}
              />
            ) : (
              <Alert type="info" message="启动 Docker 后可查看项目日志" />
            ),
          },
          {
            key: 'env',
            label: '环境变量',
            children: (
              <Flex vertical gap={12}>
                {project.fileError && (
                  <Alert type="warning" showIcon message={project.fileError} />
                )}
                {project.envFile && (
                  <Typography.Text className="compose-file-path" code copyable>
                    {project.envFile}
                  </Typography.Text>
                )}
                {filesEditable && (
                  <>
                    <Button
                      style={{ alignSelf: 'flex-start' }}
                      loading={busy === 'reveal'}
                      disabled={writing?.kind === 'rollback'}
                      onClick={() => void reveal()}
                    >
                      {showEnv ? '遮罩环境变量' : '显示明文并编辑'}
                    </Button>
                    <ComposeEnvEditor
                      key={showEnv ? 'visible' : 'masked'}
                      value={showEnv ? (env?.value ?? '') : project.env}
                      onChange={(value) =>
                        setEnv((current) => current && { ...current, value })
                      }
                      readOnly={!showEnv}
                      masked={!showEnv}
                    />
                    {showEnv && (
                      <Space wrap>
                        <Button
                          type="primary"
                          loading={writing?.kind === 'env' && !writing.apply}
                          disabled={!envDirty || !!writing || !!busy}
                          onClick={() => submit('env', false)}
                        >
                          保存
                        </Button>
                        <Button
                          loading={writing?.kind === 'env' && writing.apply}
                          disabled={!online || !envDirty || !!writing || !!busy}
                          onClick={() => submit('env', true)}
                        >
                          保存并应用
                        </Button>
                      </Space>
                    )}
                  </>
                )}
              </Flex>
            ),
          },
        ]}
      />
      {history && (
        <Suspense fallback={<Skeleton active />}>
          <ComposeRevisionModal
            project={project}
            online={online}
            onClose={() => setHistory(false)}
            onTask={(id, apply) => {
              setNotice(undefined);
              setPending({ id, kind: 'rollback', draft: draftYaml, apply });
              onTask(id);
            }}
          />
        </Suspense>
      )}
    </>
  );
}
