import {
  lazy,
  Suspense,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
} from 'react';
import { Alert, Button, Dropdown, Skeleton, Space, Tabs, theme } from 'antd';
import { Folder, Plus } from 'lucide-react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { files } from '../../api/file';
import { useAuth } from '../../stores/auth';
import { LAYOUT, SPACING } from '../../styles/tokens';
import FileBrowser, { type Clipboard } from './FileBrowser';
import TerminalModal from './TerminalModal';
import './file.css';

const FileEditor = lazy(() => import('./FileEditor'));
type DirectoryTab = { id: string; path: string };
type Workspace = { tabs: DirectoryTab[]; active: string };

export default function FilePage() {
  const username = useAuth((state) => state.user!.username);
  const query = useQuery({
    queryKey: ['file-workspace', username],
    gcTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: ({ signal }) => files<Workspace>('/workspace', { signal }),
    staleTime: 0,
  });
  if (query.isPending || query.isFetching) return <Skeleton active />;
  if (query.error)
    return (
      <Alert
        type="error"
        message="无法读取面板中保存的文件工作区"
        description={query.error.message}
        action={<Button onClick={() => void query.refetch()}>重试</Button>}
      />
    );
  return <FileWorkspace initial={query.data} />;
}

function FileWorkspace({ initial }: { initial: Workspace }) {
  const { token } = theme.useToken();
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const requested = params.get('path');
  const directory = requested?.startsWith('/') ? requested : undefined;
  const [appliedDirectory, setAppliedDirectory] = useState(directory);
  const [workspace, setWorkspace] = useState<Workspace>(() => {
    const sourceTabs = initial.tabs.length
      ? initial.tabs
      : [{ id: '1', path: '/' }];
    const activeIndex = Math.max(
      0,
      sourceTabs.findIndex((tab) => tab.id === initial.active),
    );
    const saved = {
      tabs: sourceTabs.map((tab, index) => ({
        id: String(index + 1),
        path: tab.path,
      })),
      active: String(activeIndex + 1),
    };
    if (directory) {
      const existing = saved.tabs.find((tab) => tab.path === directory);
      if (existing) saved.active = existing.id;
      else {
        const id = String(saved.tabs.length + 1);
        saved.tabs.push({ id, path: directory });
        saved.active = id;
      }
    }
    return saved;
  });
  const [saveError, setSaveError] = useState('');
  const saved = useRef(JSON.stringify(initial));
  const next = useRef(workspace.tabs.length + 1);
  const [clipboard, setClipboard] = useState<Clipboard>(null);
  const [editorRequest, setEditorRequest] = useState<{
    path: string;
    line?: number;
  } | null>(null);
  const [terminalPath, setTerminalPath] = useState<string | null>(null);
  const [editorMinimized, setEditorMinimized] = useState(false);
  const saveSeq = useRef(0);
  useEffect(() => {
    const serialized = JSON.stringify(workspace);
    if (saved.current === serialized) return;
    const seq = ++saveSeq.current;
    const timer = setTimeout(() => {
      saved.current = serialized;
      // keepalive 允许刷新页面时，已经发出的目录记忆请求继续完成。
      void files('/workspace', {
        method: 'PUT',
        data: workspace,
        silentError: true,
        adapter: 'fetch',
        fetchOptions: { keepalive: true },
      })
        .then(() => {
          if (saveSeq.current === seq) setSaveError('');
        })
        .catch(() => {
          if (saveSeq.current === seq)
            setSaveError('目录记忆尚未保存到面板系统，请重试');
        });
    }, 250);
    return () => clearTimeout(timer);
  }, [workspace]);
  const activePath =
    workspace.tabs.find((tab) => tab.id === workspace.active)?.path ?? '/';
  if (appliedDirectory !== directory) {
    setAppliedDirectory(directory);
    if (directory && directory !== activePath) {
      const existing = workspace.tabs.find((tab) => tab.path === directory);
      setWorkspace(
        existing
          ? { ...workspace, active: existing.id }
          : {
              ...workspace,
              tabs: workspace.tabs.map((tab) =>
                tab.id === workspace.active ? { ...tab, path: directory } : tab,
              ),
            },
      );
    }
  }
  useEffect(() => {
    if (!directory) return;
    setParams(
      (previous) => {
        const nextParams = new URLSearchParams(previous);
        nextParams.delete('path');
        return nextParams;
      },
      { replace: true },
    );
  }, [directory, setParams]);

  function update(next: Workspace) {
    setWorkspace(next);
  }
  function addTab(path: string) {
    const id = String(next.current++);
    update({ tabs: [...workspace.tabs, { id, path }], active: id });
  }
  function closeTabs(ids: string[]) {
    const tabs = workspace.tabs.filter((tab) => !ids.includes(tab.id));
    if (!tabs.length) return;
    const index = workspace.tabs.findIndex(
      (tab) => tab.id === workspace.active,
    );
    update({
      tabs,
      active: tabs.some((tab) => tab.id === workspace.active)
        ? workspace.active
        : tabs[Math.min(index, tabs.length - 1)].id,
    });
  }
  return (
    <>
      {saveError && (
        <Alert
          type="warning"
          message={saveError}
          action={
            <Button
              size="small"
              onClick={() => {
                saved.current = '';
                setWorkspace({ ...workspace });
              }}
            >
              重试保存
            </Button>
          }
        />
      )}
      <div
        className="file-directory-tabs"
        style={
          {
            '--file-tab-width': `${LAYOUT.fileTabWidth}px`,
            '--file-folder': token.colorPrimary,
          } as CSSProperties
        }
      >
        <Tabs
          type="editable-card"
          activeKey={workspace.active}
          onChange={(active) => update({ ...workspace, active })}
          onEdit={(key, action) => {
            if (action === 'add') addTab(activePath);
            else closeTabs([String(key)]);
          }}
          addIcon={<Plus size={15} strokeWidth={2} />}
          renderTabBar={(props, DefaultTabBar) => (
            <DefaultTabBar {...props}>
              {(node) => {
                const index = workspace.tabs.findIndex(
                  (item) => item.id === node.key,
                );
                const tab = workspace.tabs[index];
                return (
                  <Dropdown
                    trigger={['contextMenu']}
                    menu={{
                      items: [
                        {
                          key: 'close',
                          label: '关闭当前',
                          disabled: workspace.tabs.length === 1,
                        },
                        {
                          key: 'others',
                          label: '关闭其它',
                          disabled: workspace.tabs.length === 1,
                        },
                        {
                          key: 'right',
                          label: '关闭右侧',
                          disabled: index === workspace.tabs.length - 1,
                        },
                        { type: 'divider' },
                        { key: 'duplicate', label: '复制选项卡' },
                      ],
                      onClick: ({ key }) => {
                        if (key === 'duplicate') addTab(tab.path);
                        else if (key === 'close') closeTabs([tab.id]);
                        else if (key === 'others')
                          closeTabs(
                            workspace.tabs
                              .filter((item) => item.id !== tab.id)
                              .map((item) => item.id),
                          );
                        else
                          closeTabs(
                            workspace.tabs
                              .slice(index + 1)
                              .map((item) => item.id),
                          );
                      },
                    }}
                    key={tab.id}
                  >
                    {node}
                  </Dropdown>
                );
              }}
            </DefaultTabBar>
          )}
          items={workspace.tabs.map((tab) => ({
            key: tab.id,
            closable: workspace.tabs.length > 1,
            label: (
              <span title={tab.path}>
                <Space size={SPACING.sm}>
                  <Folder
                    size={16}
                    strokeWidth={1.6}
                    fill="currentColor"
                    className="file-folder-icon"
                  />
                  <span className="file-directory-tab-name">
                    {tab.path.split('/').filter(Boolean).pop() || '根目录'}
                  </span>
                </Space>
              </span>
            ),
          }))}
        />
      </div>
      {workspace.tabs.map((tab) => (
        <div key={tab.id} hidden={workspace.active !== tab.id}>
          <FileBrowser
            initialPath={tab.path}
            active={workspace.active === tab.id}
            blocked={Boolean(
              (editorRequest && !editorMinimized) || terminalPath,
            )}
            clipboard={clipboard}
            setClipboard={setClipboard}
            onPathChange={(path) =>
              update({
                ...workspace,
                tabs: workspace.tabs.map((item) =>
                  item.id === tab.id ? { ...item, path } : item,
                ),
              })
            }
            onNewTab={addTab}
            onEditFile={(path, line) => {
              setEditorRequest({ path, line });
              setEditorMinimized(false);
            }}
            onTerminal={setTerminalPath}
          />
        </div>
      ))}
      {editorRequest && (
        <Suspense fallback={null}>
          <FileEditor
            request={editorRequest}
            minimized={editorMinimized}
            onMinimizedChange={setEditorMinimized}
            onClose={() => setEditorRequest(null)}
            onSaved={() => {
              void queryClient.invalidateQueries({ queryKey: ['files'] });
            }}
          />
        </Suspense>
      )}
      {terminalPath !== null && (
        <TerminalModal
          cwd={terminalPath}
          onClose={() => setTerminalPath(null)}
        />
      )}
    </>
  );
}
