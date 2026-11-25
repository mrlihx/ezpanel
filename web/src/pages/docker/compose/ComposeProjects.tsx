import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
} from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Dropdown,
  Empty,
  Flex,
  Input,
  Modal,
  Radio,
  Select,
  Skeleton,
  Space,
  Tag,
  Typography,
  theme,
  type MenuProps,
} from 'antd';
import {
  Ellipsis,
  FolderOpen,
  ListChecks,
  Plus,
  RefreshCw,
  Upload,
} from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { Link, matchPath, useBlocker } from 'react-router-dom';
import {
  composeProject,
  composeProjectAction,
  deleteComposeProject,
  listComposeProjects,
  type ComposeDeleteOptions,
  type ComposeProject,
  type ComposeProjectAction,
} from '../../../api/composeProjects';
import { formatTime } from '../../../utils/format';
import { ComposeSaveTemplateModal } from '../ComposeTemplates';
import ComposeCreateModal from './ComposeCreateModal';
import ComposeProjectDetail from './ComposeProjectDetail';
import './compose.css';

const projectStatuses = {
  running: { color: 'success', label: '运行中' },
  partial: { color: 'warning', label: '部分运行' },
  stopped: { color: 'default', label: '已停止' },
  error: { color: 'error', label: '异常退出' },
};

function projectSource(row: ComposeProject) {
  if (row.source === 'managed') return '面板创建';
  return row.registered ? '外部纳管' : '外部发现';
}

const actions = {
  up: '启动',
  start: '启动已存在容器',
  stop: '停止',
  restart: '重启',
  down: '下线清理',
  pull: '拉取镜像',
  rebuild: '强制重建',
  'pull-up': '拉取镜像并重建',
  delete: '删除',
};

const defaultDelete: ComposeDeleteOptions = {
  removeContainers: true,
  removeVolumes: false,
  removeFiles: false,
};

type ContainerCallbacks = {
  onContainer: (id: string, name: string, tab: string) => void;
  onTerminal: (id: string) => void;
};

export default function ComposeProjects({
  active,
  requestedProject,
  projectTab,
  onProjectTab,
  online,
  onTask,
  onChanged,
  onContainer,
  onTerminal,
}: ContainerCallbacks & {
  active: boolean;
  requestedProject?: string;
  projectTab?: string;
  onProjectTab: (name: string, tab: string) => void;
  online: boolean;
  onTask: (taskId: string) => void;
  onChanged: () => void;
}) {
  const { modal, message } = App.useApp();
  const { token } = theme.useToken();
  const [keyword, setKeyword] = useState('');
  const [status, setStatus] = useState<string>();
  const [selected, setSelected] = useState<string[]>([]);
  const [picking, setPicking] = useState(false);
  const [create, setCreate] = useState<{
    adopt?: ComposeProject;
    importing?: boolean;
  }>();
  const hasDraft = useRef(false);
  const detailPanel = useRef<HTMLElement>(null);
  const [template, setTemplate] = useState<ComposeProject>();
  const [destructive, setDestructive] = useState<{
    action: 'down' | 'delete';
    rows: ComposeProject[];
  }>();
  const [submitted, setSubmitted] = useState<{ name: string; id: string }[]>(
    [],
  );
  const [busy, setBusy] = useState(false);
  const query = useQuery({
    queryKey: ['compose-projects'],
    queryFn: ({ signal }) => listComposeProjects(signal),
    enabled: active,
    refetchInterval: (query) =>
      active && online && !query.state.error ? 12000 : false,
    retry: false,
  });
  const rows = query.data ?? [];
  const term = keyword.trim().toLowerCase();
  const matching = rows.filter(
    (row) =>
      (!status || row.status === status) &&
      (!term ||
        [row.name, row.remark, row.workDir, row.composeFile].some((text) =>
          text?.toLowerCase().includes(term),
        )),
  );
  const picked = rows.filter((row) => selected.includes(row.name));
  const batchMissingConfig = picked.some((item) => item.missingComposeFile);
  const checkedCount = matching.filter((item) =>
    selected.includes(item.name),
  ).length;
  const row =
    rows.find((item) => item.name === requestedProject) ??
    (requestedProject ? undefined : rows[0]);
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    if (
      !active ||
      !hasDraft.current ||
      currentLocation.pathname === nextLocation.pathname
    )
      return false;
    const next = matchPath(
      '/docker/compose/projects/:project/:tab?',
      nextLocation.pathname,
    );
    return (
      next?.params.project !== (row ? encodeURIComponent(row.name) : undefined)
    );
  });
  useEffect(() => {
    if (blocker.state !== 'blocked') return;
    const confirm = modal.confirm({
      title: '编排配置尚未保存，确定离开？',
      okText: '放弃修改并离开',
      cancelText: '继续编辑',
      onOk: () => {
        hasDraft.current = false;
        blocker.proceed();
      },
      onCancel: () => blocker.reset(),
    });
    return () => confirm.destroy();
  }, [blocker, modal]);

  const detail = useQuery({
    queryKey: ['compose-project-detail', row?.name],
    queryFn: ({ signal }) => composeProject(row!.name, signal),
    enabled: active && !!row,
    refetchInterval: (query) =>
      active && online && !query.state.error ? 10000 : false,
    retry: false,
  });

  const updateDirty = useCallback((dirty: boolean) => {
    hasDraft.current = dirty;
  }, []);

  function selectProject(name: string) {
    if (requestedProject !== name) onProjectTab(name, 'editor');
    if (window.matchMedia('(max-width: 900px)').matches) {
      window.requestAnimationFrame(() =>
        detailPanel.current?.scrollIntoView({ block: 'start' }),
      );
    }
  }

  async function perform(
    action: ComposeProjectAction | 'delete',
    targets: ComposeProject[],
    options = defaultDelete,
    removeImages = false,
  ) {
    setBusy(true);
    const result = await Promise.allSettled(
      targets.map((row) =>
        action === 'delete'
          ? deleteComposeProject(row.name, options)
          : composeProjectAction(row.name, {
              action,
              removeVolumes: options.removeVolumes,
              removeImages,
            }),
      ),
    );
    setBusy(false);
    const submitted = result.flatMap((item, index) =>
      item.status === 'fulfilled'
        ? [{ name: targets[index].name, id: item.value.taskId }]
        : [],
    );
    if (submitted.length) {
      setSelected((current) =>
        current.filter((name) => !submitted.some((item) => item.name === name)),
      );
      if (targets.length > 1) setSubmitted(submitted);
      onTask(submitted[0].id);
    }
    if (targets.length > 1) {
      const failed = result.length - submitted.length;
      const summary = `已提交 ${submitted.length} 个任务，提交失败 ${failed} 个`;
      if (failed) void message.warning(summary);
      else void message.success(summary);
    }
    return submitted.length > 0;
  }

  function choose(
    action: ComposeProjectAction | 'delete',
    targets: ComposeProject[],
  ) {
    if (action === 'delete' || action === 'down') {
      setDestructive({ action, rows: targets });
      return;
    }
    const label = actions[action];
    if (action === 'up' || action === 'start' || action === 'pull') {
      void perform(action, targets);
      return;
    }
    modal.confirm({
      title: `${label}${targets.length === 1 ? `「${targets[0].name}」` : `这 ${targets.length} 个项目`}？`,
      content:
        action === 'stop'
          ? '容器和网络将保留。'
          : '操作过程中项目服务可能短暂中断。',
      onOk: () => perform(action, targets).then(() => undefined),
    });
  }

  const batch: MenuProps['items'] = [
    { key: 'up', label: '批量启动' },
    { key: 'stop', label: '批量停止' },
    { key: 'restart', label: '批量重启' },
    { type: 'divider' },
    {
      key: 'delete',
      label: batchMissingConfig ? '含配置缺失的项目，请逐项删除' : '批量删除',
      disabled: batchMissingConfig,
      danger: true,
    },
  ];

  return (
    <>
      <Flex justify="space-between" gap={12} wrap className="compose-toolbar">
        <Space wrap>
          <Button
            type="primary"
            icon={<Plus size={16} />}
            disabled={!online}
            onClick={() => setCreate({})}
          >
            创建
          </Button>
          <Button
            icon={<Upload size={15} />}
            disabled={!online}
            onClick={() => setCreate({ importing: true })}
          >
            导入
          </Button>
          <Button
            icon={<ListChecks size={16} />}
            type={picking ? 'primary' : 'default'}
            aria-pressed={picking}
            onClick={() => {
              setPicking(!picking);
              if (picking) setSelected([]);
            }}
          >
            {picking ? '退出多选' : '多选'}
          </Button>
          {picking && (
            <Dropdown
              trigger={['click']}
              disabled={!picked.length || busy || !online}
              menu={{
                items: batch,
                onClick: ({ key }) =>
                  choose(key as ComposeProjectAction | 'delete', picked),
              }}
            >
              <Button>
                批量操作{picked.length ? ` (${picked.length})` : ''}
              </Button>
            </Dropdown>
          )}
        </Space>
        <Space wrap>
          <Input.Search
            aria-label="搜索编排项目"
            placeholder="搜索项目名、备注或路径"
            allowClear
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
          />
          <Select
            aria-label="筛选状态"
            placeholder="全部状态"
            style={{ width: 126 }}
            allowClear
            value={status}
            options={Object.entries(projectStatuses).map(([value, item]) => ({
              value,
              label: item.label,
            }))}
            onChange={setStatus}
          />
          <Button
            icon={<RefreshCw size={15} />}
            aria-label="刷新编排项目"
            loading={query.isFetching || detail.isFetching}
            onClick={() => {
              void query.refetch();
              if (row) void detail.refetch();
            }}
          />
        </Space>
      </Flex>
      {query.error && (
        <Alert
          className="compose-toolbar"
          showIcon
          type="error"
          message={query.error.message}
        />
      )}
      {!!submitted.length && (
        <Alert
          className="compose-toolbar"
          type="info"
          showIcon
          closable
          onClose={() => setSubmitted([])}
          message={
            <Space wrap>
              <span>批量任务</span>
              {submitted.map(({ id, name }) => (
                <Button
                  key={id}
                  type="link"
                  size="small"
                  onClick={() => onTask(id)}
                >
                  {name}
                </Button>
              ))}
            </Space>
          }
        />
      )}
      <div
        className="compose-workspace"
        style={
          {
            '--compose-surface': token.colorBgContainer,
            '--compose-radius': `${token.borderRadiusLG}px`,
            '--compose-shadow': token.boxShadowTertiary,
            '--compose-border': token.colorBorderSecondary,
            '--compose-selected': token.colorFillQuaternary,
            '--compose-hover': token.colorFillTertiary,
            '--compose-ink': token.colorText,
            '--compose-muted': token.colorTextSecondary,
            '--compose-accent': token.colorPrimary,
            '--compose-success': token.colorSuccessText,
            '--compose-warning': token.colorWarningText,
            '--compose-error': token.colorErrorText,
          } as CSSProperties
        }
      >
        <aside
          className="compose-panel compose-project-list"
          aria-label="编排项目"
        >
          <div className="compose-list-heading">
            <Space size={8}>
              {picking && (
                <Checkbox
                  aria-label="选择筛选结果"
                  disabled={!matching.length}
                  checked={
                    !!matching.length && checkedCount === matching.length
                  }
                  indeterminate={
                    checkedCount > 0 && checkedCount < matching.length
                  }
                  onChange={({ target }) => {
                    const visible = new Set(matching.map((item) => item.name));
                    setSelected((keys) =>
                      target.checked
                        ? Array.from(new Set([...keys, ...visible]))
                        : keys.filter((name) => !visible.has(name)),
                    );
                  }}
                />
              )}
              <Typography.Text strong>编排项目</Typography.Text>
            </Space>
            <Typography.Text type="secondary">
              {matching.length}
            </Typography.Text>
          </div>
          {query.isLoading ? (
            <Skeleton active paragraph={{ rows: 4 }} />
          ) : !matching.length ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                term || status ? '没有符合条件的项目' : '暂无编排项目'
              }
            />
          ) : (
            <ul className="compose-project-items">
              {matching.map((item) => {
                const current = item.name === row?.name;
                const statusInfo = projectStatuses[item.status];
                return (
                  <li
                    key={item.name}
                    className={current ? 'compose-project-current' : undefined}
                  >
                    <div className="compose-project-line">
                      {picking && (
                        <Checkbox
                          checked={selected.includes(item.name)}
                          aria-label={`选择 ${item.name}`}
                          onChange={({ target }) =>
                            setSelected((keys) =>
                              target.checked
                                ? [...keys, item.name]
                                : keys.filter((key) => key !== item.name),
                            )
                          }
                        />
                      )}
                      <button
                        type="button"
                        className="compose-project-button"
                        aria-current={current ? 'true' : undefined}
                        onClick={() => selectProject(item.name)}
                      >
                        <span className="compose-project-name">
                          {item.name}
                        </span>
                        <span className="compose-project-meta">
                          <span>{projectSource(item)}</span>
                          <span
                            className="compose-project-state"
                            data-state={online ? item.status : undefined}
                          >
                            {online
                              ? `${statusInfo.label} ${item.running}/${item.total}`
                              : 'Docker 离线'}
                          </span>
                        </span>
                      </button>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
          <div className="compose-list-footer">
            <Link to="/docker/containers">查看容器</Link>
          </div>
        </aside>
        <section
          className="compose-panel compose-workspace-detail"
          ref={detailPanel}
          aria-label="编排项目详情"
        >
          {row ? (
            <>
              <header className="compose-project-header">
                <div className="compose-project-identity">
                  <div className="compose-project-title">
                    <h3>{row.name}</h3>
                    <Tag>{projectSource(row)}</Tag>
                  </div>
                  <div className="compose-project-meta">
                    {!!row.createdAt && !row.createdAt.startsWith('0001-') && (
                      <span>创建于 {formatTime(row.createdAt)}</span>
                    )}
                    {!!row.updatedAt &&
                      !row.updatedAt.startsWith('0001-') &&
                      row.updatedAt !== row.createdAt && (
                        <span>更新于 {formatTime(row.updatedAt)}</span>
                      )}
                    {row.remark && <span>{row.remark}</span>}
                  </div>
                </div>
                <Space wrap align="center" className="compose-project-actions">
                  <Tag
                    color={
                      online ? projectStatuses[row.status].color : 'default'
                    }
                  >
                    {online
                      ? `${projectStatuses[row.status].label} ${row.running}/${row.total}`
                      : 'Docker 未运行'}
                  </Tag>
                  <Button
                    disabled={!online || busy}
                    onClick={() =>
                      choose(
                        row.running > 0 && row.running === row.total
                          ? 'stop'
                          : 'up',
                        [row],
                      )
                    }
                  >
                    {row.running > 0 && row.running === row.total
                      ? '停止'
                      : '启动'}
                  </Button>
                  {row.workDir && (
                    <Link
                      className="compose-directory-link"
                      to={`/files?path=${encodeURIComponent(row.workDir)}`}
                      title={row.workDir}
                    >
                      <FolderOpen size={15} /> 目录
                    </Link>
                  )}
                  <Dropdown
                    trigger={['click']}
                    menu={{
                      items: [
                        {
                          key: 'restart',
                          label: '重启',
                          disabled: !online || busy,
                        },
                        {
                          key: 'pull-up',
                          label: '拉取镜像并重建',
                          disabled: !online || busy,
                        },
                        {
                          key: 'down',
                          label: '下线清理',
                          disabled: !online || busy,
                        },
                        ...(!row.registered
                          ? [
                              {
                                key: 'adopt',
                                label: '纳管',
                                disabled: !online || !row.workDir,
                              },
                            ]
                          : []),
                        {
                          key: 'template',
                          label: '另存为模板',
                          disabled: !detail.data || !!detail.data.fileError,
                        },
                        { type: 'divider' },
                        {
                          key: 'delete',
                          label: '删除项目',
                          danger: true,
                          disabled: busy || (!online && !row.registered),
                        },
                      ],
                      onClick: ({ key }) => {
                        if (key === 'adopt') setCreate({ adopt: row });
                        else if (key === 'template') setTemplate(row);
                        else
                          choose(key as ComposeProjectAction | 'delete', [
                            detail.data ?? row,
                          ]);
                      },
                    }}
                  >
                    <Button
                      icon={<Ellipsis size={17} />}
                      aria-label="更多编排操作"
                    />
                  </Dropdown>
                </Space>
              </header>
              {detail.data ? (
                <ComposeProjectDetail
                  key={row.name}
                  project={detail.data}
                  tab={projectTab ?? 'editor'}
                  onTabChange={(next) => onProjectTab(row.name, next)}
                  active={active}
                  online={online}
                  onTask={onTask}
                  onChanged={onChanged}
                  onContainer={onContainer}
                  onTerminal={onTerminal}
                  onDirtyChange={updateDirty}
                />
              ) : detail.error ? (
                <Alert
                  showIcon
                  type="error"
                  message={detail.error.message}
                  action={
                    <Button onClick={() => void detail.refetch()}>重试</Button>
                  }
                />
              ) : (
                <Skeleton active />
              )}
            </>
          ) : query.isLoading ? (
            <Skeleton active />
          ) : (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                requestedProject
                  ? `未找到编排项目「${requestedProject}」，可从左侧选择其他项目。`
                  : '请选择或创建编排项目'
              }
            />
          )}
        </section>
      </div>
      {create && (
        <ComposeCreateModal
          adopt={create.adopt}
          importing={create.importing}
          onTask={onTask}
          onClose={() => setCreate(undefined)}
        />
      )}
      {template && (
        <ComposeSaveTemplateModal
          project={template}
          onClose={() => setTemplate(undefined)}
        />
      )}
      {destructive && (
        <DestructiveModal
          action={destructive.action}
          online={online}
          rows={destructive.rows}
          busy={busy}
          onClose={() => setDestructive(undefined)}
          onSubmit={async (options, images) => {
            if (
              await perform(
                destructive.action,
                destructive.rows,
                options,
                images,
              )
            ) {
              setDestructive(undefined);
            }
          }}
        />
      )}
    </>
  );
}

function DestructiveModal({
  action,
  online,
  rows,
  busy,
  onClose,
  onSubmit,
}: {
  action: 'down' | 'delete';
  online: boolean;
  rows: ComposeProject[];
  busy: boolean;
  onClose: () => void;
  onSubmit: (options: ComposeDeleteOptions, images: boolean) => Promise<void>;
}) {
  const deleting = action === 'delete';
  const registered = rows.every((row) => row.registered);
  const missing = deleting && rows.some((row) => row.missingComposeFile);
  const [options, setOptions] = useState<ComposeDeleteOptions>({
    ...defaultDelete,
    removeContainers: !deleting || online || !registered,
  });
  const [images, setImages] = useState(false);
  const cleanup = options.removeContainers;
  const subject =
    rows.length === 1 ? `「${rows[0].name}」` : `这 ${rows.length} 个项目`;
  const allowFileRemoval =
    deleting && !missing && rows.length === 1 && rows[0].canRemoveFiles;
  const needsConfirmationName =
    options.removeFiles || (missing && !!options.allowMissingConfig);

  return (
    <Modal
      open
      centered
      title={`${actions[action]}${subject}？`}
      okText={deleting && !cleanup ? '取消纳管' : actions[action]}
      onCancel={onClose}
      onOk={() => void onSubmit(options, images)}
      confirmLoading={busy}
      okButtonProps={{
        danger: true,
        disabled:
          (cleanup && (!online || (missing && !options.allowMissingConfig))) ||
          (needsConfirmationName && options.confirmName !== rows[0].name),
      }}
    >
      <Flex vertical gap={12}>
        {deleting && registered ? (
          <Radio.Group
            value={cleanup ? 'cleanup' : 'unregister'}
            onChange={(event) =>
              setOptions({
                ...defaultDelete,
                removeContainers: event.target.value === 'cleanup',
              })
            }
          >
            <Space direction="vertical">
              <Radio value="cleanup" disabled={!online}>
                删除项目并清理关联容器{missing ? '' : '和网络'}
              </Radio>
              <Radio value="unregister">仅取消纳管</Radio>
            </Space>
          </Radio.Group>
        ) : (
          <Typography.Text>
            {missing
              ? '停止并移除项目关联容器。'
              : '停止并移除容器和默认网络。'}
          </Typography.Text>
        )}
        {cleanup && missing && (
          <>
            <Alert
              type="warning"
              showIcon
              message="Compose 文件缺失"
              description="仅清理与该项目标签匹配的容器，保留数据卷、网络和工作目录。"
            />
            <Checkbox
              checked={!!options.allowMissingConfig}
              onChange={(event) =>
                setOptions({
                  ...options,
                  allowMissingConfig: event.target.checked,
                })
              }
            >
              确认按编排标签清理
            </Checkbox>
          </>
        )}
        {cleanup && !missing && (
          <Checkbox
            checked={options.removeVolumes}
            onChange={(event) =>
              setOptions({ ...options, removeVolumes: event.target.checked })
            }
          >
            同时删除项目数据卷
          </Checkbox>
        )}
        {cleanup && allowFileRemoval && (
          <Checkbox
            checked={options.removeFiles}
            onChange={(event) =>
              setOptions({ ...options, removeFiles: event.target.checked })
            }
          >
            同时删除工作目录及文件
          </Checkbox>
        )}
        {cleanup &&
          deleting &&
          !missing &&
          rows.length === 1 &&
          !allowFileRemoval && (
            <Typography.Text type="secondary">
              只有面板创建的项目专用目录可以随项目删除。
            </Typography.Text>
          )}
        {needsConfirmationName && (
          <Input
            aria-label="确认清理的项目名称"
            placeholder={`输入项目名称 ${rows[0].name} 确认`}
            value={options.confirmName}
            onChange={(event) =>
              setOptions({ ...options, confirmName: event.target.value })
            }
          />
        )}
        {!deleting && (
          <Checkbox
            checked={images}
            onChange={(event) => setImages(event.target.checked)}
          >
            同时删除本地项目镜像
          </Checkbox>
        )}
        {deleting && !cleanup && (
          <Alert
            type="info"
            message="容器和文件将保留。运行中的容器仍可能被识别为外部项目。"
          />
        )}
        {(options.removeVolumes || options.removeFiles) && (
          <Alert
            type="warning"
            showIcon
            message="勾选删除的数据卷或目录将无法恢复"
          />
        )}
      </Flex>
    </Modal>
  );
}
