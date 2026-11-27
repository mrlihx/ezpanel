import SectionTabs from '../../components/SectionTabs';
import { lazy, Suspense } from 'react';
import { Alert, Button, Modal, Skeleton, Space } from 'antd';
import TaskDrawer from '../../components/TaskDrawer';
import ApplicationInstallGuide from '../../components/ApplicationInstallGuide';
import { LAYOUT } from '../../styles/tokens';
import useDockerPage from './useDockerPage';
import RouteNotFound from '../../router/RouteNotFound';

const ComposeWorkspace = lazy(() => import('./compose'));
const ContainerManager = lazy(() => import('./ContainerManager'));
const ImageTaskModal = lazy(() => import('./ImageTaskModal'));
const DockerOperationForm = lazy(() => import('./DockerOperationForm'));
const TerminalModal = lazy(() => import('../file/TerminalModal'));
const DockerOverviewPanel = lazy(() => import('./DockerOverviewPanel'));
const DockerContainersPanel = lazy(() => import('./DockerContainersPanel'));
const DockerImagesPanel = lazy(() => import('./DockerImagesPanel'));
const DockerNetworksPanel = lazy(() => import('./DockerNetworksPanel'));
const DockerVolumesPanel = lazy(() => import('./DockerVolumesPanel'));
const DockerRegistriesPanel = lazy(() => import('./DockerRegistriesPanel'));
const DockerSettingsPanel = lazy(() => import('./DockerSettingsPanel'));
const containerTabs = [
  'status',
  'terminal',
  'detail',
  'volumes',
  'network',
  'restart',
  'commit',
  'edit',
  'upgrade',
  'rename',
  'stats',
  'logs',
  'proxy',
];
export default function DockerPage() {
  const {
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
  } = useDockerPage();
  const linkedContainer = tab === 'containers' ? view : undefined;
  const invalid =
    route.invalid ||
    (tab === 'containers' &&
      (detailTab ||
        (linkedContainer && !/^[a-f0-9]{12,64}$/.test(linkedContainer)) ||
        (resource && !containerTabs.includes(resource)))) ||
    (tab === 'images' && (imageRoute.invalid || resource || detailTab)) ||
    (tab === 'settings' && (settingsRoute.invalid || resource || detailTab)) ||
    (view && !['containers', 'images', 'compose', 'settings'].includes(tab));
  if (invalid) return <RouteNotFound back="/docker" />;
  if (
    route.loading ||
    (tab === 'images' && imageRoute.loading) ||
    (tab === 'settings' && settingsRoute.loading)
  )
    return <Skeleton active />;
  if (overview.data?.installed === false)
    return (
      <ApplicationInstallGuide
        name="Docker"
        install="docker"
        onDetect={() => void overview.refetch()}
      />
    );
  const activeDetail = linkedContainer
    ? {
        id: linkedContainer,
        name: linkedContainer.slice(0, 12),
        tab: resource ?? 'status',
      }
    : detail;
  return (
    <>
      {error && (
        <Alert
          type="error"
          message={error.message}
          style={{ marginBottom: 16 }}
        />
      )}
      {overview.data?.notice && (
        <Alert
          type="info"
          message={overview.data.notice}
          style={{ marginBottom: 16 }}
        />
      )}
      {!!tasks.data?.total && (
        <Alert
          type="info"
          style={{ marginBottom: 16 }}
          message={
            <Space wrap>
              Docker 任务进行中
              {tasks.data.list.map((row) => (
                <Button
                  key={row.taskId}
                  type="link"
                  onClick={() => setTask(row.taskId)}
                >
                  {row.target} · 查看日志
                </Button>
              ))}
            </Space>
          }
        />
      )}
      <SectionTabs
        cardContent
        activeKey={tab}
        onChange={(key) => {
          void route.selectTab(key);
          setSelected([]);
          setKeyword('');
        }}
        items={[
          {
            key: 'overview',
            label: '总览',
            children: (
              <Suspense fallback={<Skeleton active />}>
                <DockerOverviewPanel
                  containerBytes={containerBytes}
                  networkList={networkList}
                  online={online}
                  overview={overview}
                  space={space}
                  usage={usage}
                  volumeBytes={volumeBytes}
                />
              </Suspense>
            ),
          },
          {
            key: 'containers',
            label: '容器',
            children: (
              <Suspense fallback={<Skeleton active />}>
                <DockerContainersPanel
                  action={action}
                  actions={actions}
                  busy={busy}
                  containerImageName={containerImageName}
                  containerList={containerList}
                  destroy={destroy}
                  filter={filter}
                  fixedActions={fixedActions}
                  itemName={itemName}
                  match={match}
                  modal={modal}
                  online={online}
                  open={open}
                  prune={prune}
                  run={run}
                  selected={selected}
                  setDetail={setDetail}
                  setSelected={setSelected}
                  setTerminal={setTerminal}
                  token={token}
                />
              </Suspense>
            ),
          },
          {
            key: 'images',
            label: '镜像',
            children: (
              <Suspense fallback={<Skeleton active />}>
                <DockerImagesPanel
                  containerList={containerList}
                  containersByImage={containersByImage}
                  destroy={destroy}
                  filter={filter}
                  imageList={imageList}
                  imageView={imageView}
                  itemName={itemName}
                  match={match}
                  online={online}
                  open={open}
                  prune={prune}
                  search={search}
                  setImageTask={setImageTask}
                  imageRoute={imageRoute}
                  setTerm={setTerm}
                />
              </Suspense>
            ),
          },
          {
            key: 'networks',
            label: '网络',
            children: (
              <Suspense fallback={<Skeleton active />}>
                <DockerNetworksPanel
                  destroy={destroy}
                  filter={filter}
                  fixedActions={fixedActions}
                  match={match}
                  networkList={networkList}
                  online={online}
                  open={open}
                  prune={prune}
                />
              </Suspense>
            ),
          },
          {
            key: 'volumes',
            label: '存储卷',
            children: (
              <Suspense fallback={<Skeleton active />}>
                <DockerVolumesPanel
                  containerList={containerList}
                  destroy={destroy}
                  filter={filter}
                  fixedActions={fixedActions}
                  itemName={itemName}
                  match={match}
                  online={online}
                  open={open}
                  prune={prune}
                  run={run}
                  setJsonDetail={setJsonDetail}
                  volumeList={volumeList}
                />
              </Suspense>
            ),
          },
          {
            key: 'registries',
            label: '仓库',
            children: (
              <Suspense fallback={<Skeleton active />}>
                <DockerRegistriesPanel
                  destroy={destroy}
                  fixedActions={fixedActions}
                  message={message}
                  open={open}
                  registryList={registryList}
                  run={run}
                />
              </Suspense>
            ),
          },
          {
            key: 'compose',
            label: '容器编排',
            children: (
              <Suspense fallback={<Skeleton active />}>
                <ComposeWorkspace
                  active={tab === 'compose'}
                  online={online}
                  onChanged={refresh}
                  onTask={(id) => {
                    setTask(id);
                    void client.invalidateQueries({
                      queryKey: ['docker-tasks'],
                    });
                  }}
                  onContainer={(id, name, tab) => setDetail({ id, name, tab })}
                  onTerminal={setTerminal}
                />
              </Suspense>
            ),
          },
          {
            key: 'settings',
            label: '设置',
            children: (
              <Suspense fallback={<Skeleton active />}>
                <DockerSettingsPanel
                  settingsRoute={settingsRoute}
                  busy={busy}
                  guard={guard}
                  message={message}
                  modal={modal}
                  online={online}
                  overview={overview}
                  run={run}
                />
              </Suspense>
            ),
          },
        ]}
      />
      {imageTask && (
        <Suspense fallback={null}>
          <ImageTaskModal
            mode={imageTask}
            onClose={() => setImageTask(undefined)}
            onSubmitted={(id) => {
              setTask(id);
              void client.invalidateQueries({ queryKey: ['docker-tasks'] });
            }}
          />
        </Suspense>
      )}
      {operation && (
        <Suspense fallback={null}>
          <DockerOperationForm
            operation={operation}
            form={form}
            busy={busy}
            imageList={imageList.data}
            networkList={networkList.data}
            containerList={containerList.data}
            registryOptions={registryOptions}
            onClose={() => setOperation(undefined)}
            onSubmit={() => void submit()}
          />
        </Suspense>
      )}
      {activeDetail && (
        <Suspense fallback={null}>
          <ContainerManager
            key={linkedContainer ?? `${activeDetail.id}:${activeDetail.tab}`}
            id={activeDetail.id}
            name={activeDetail.name}
            initialTab={activeDetail.tab}
            onTabChange={
              linkedContainer
                ? (section) =>
                    void navigate(
                      `/docker/containers/${linkedContainer}/${section}`,
                    )
                : undefined
            }
            onClose={() => {
              setDetail(undefined);
              if (linkedContainer)
                void navigate('/docker/containers', { replace: true });
            }}
            onNavigate={() => setDetail(undefined)}
            onChanged={(name) => {
              if (name) setDetail((current) => current && { ...current, name });
              refresh();
            }}
            onTask={setTask}
            onTerminal={() => setTerminal(activeDetail.id)}
            onOperation={open}
          />
        </Suspense>
      )}
      <Modal
        open={!!jsonDetail}
        title={jsonDetail?.title}
        width={LAYOUT.toolboxWidth}
        footer={null}
        onCancel={() => setJsonDetail(undefined)}
      >
        <pre
          style={{
            maxHeight: '65dvh',
            overflow: 'auto',
            whiteSpace: 'pre-wrap',
          }}
        >
          {JSON.stringify(jsonDetail?.content, null, 2)}
        </pre>
      </Modal>
      {terminal && (
        <Suspense fallback={<Skeleton active />}>
          <TerminalModal
            cwd="/root"
            title={`Docker容器终端 · ${terminal.slice(0, 12)}`}
            initialInput={`printf '\\033[?2004l'; docker exec -it -e TERM=xterm-256color -e 'PS1=\\u@\\h:\\w\\$ ' ${terminal} sh -c 'if command -v bash >/dev/null 2>&1; then exec bash; elif command -v ash >/dev/null 2>&1; then exec ash; else exec sh; fi'; exit`}
            onClose={() => setTerminal(undefined)}
          />
        </Suspense>
      )}
      {task && (
        <TaskDrawer
          taskId={task}
          title="Docker 任务"
          open
          onClose={() => setTask(undefined)}
          onSettled={(finished) => {
            refresh();
            if (finished.status !== 'success') return;
            if (finished.action === 'recreate') {
              const result = finished.result as { id: string; name?: string };
              if (
                linkedContainer === finished.resourceId &&
                result.id !== linkedContainer
              ) {
                void navigate(`/docker/containers/${result.id}/status`, {
                  replace: true,
                });
                return;
              }
              setDetail((current) =>
                current?.id === finished.resourceId && result.id !== current.id
                  ? {
                      id: result.id,
                      name: result.name ?? current.name,
                      tab: 'status',
                    }
                  : current,
              );
            } else if (finished.action === 'commit') {
              const result = finished.result as {
                imageId: string;
                image: string;
                export: boolean;
              };
              if (result.export) {
                const link = document.createElement('a');
                link.href = `/api/docker/images/${encodeURIComponent(result.imageId)}/archive?image=${encodeURIComponent(result.image)}`;
                link.download = '';
                link.click();
              }
            }
          }}
        />
      )}
    </>
  );
}
