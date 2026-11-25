import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Dropdown,
  Empty,
  Flex,
  Grid,
  Input,
  Popover,
  Space,
  Spin,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import {
  Maximize2,
  Minimize2,
  MoreHorizontal,
  Plus,
  Search,
  Server,
} from 'lucide-react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { terminalSettings, saveTerminalSettings } from '../../api/settings';
import {
  deleteTerminalHost,
  saveTerminalHost,
  terminalHosts,
  type TerminalHost,
  type TerminalHostInput,
} from '../../api/terminal';
import { toolboxData } from '../../api/toolbox';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import SectionTabs from '../../components/SectionTabs';
import { SPACING } from '../../styles/tokens';
import Commands from './Commands';
import { HostForm, HostList } from './Hosts';
import './terminal.css';

const Session = lazy(() => import('./session'));
const hostKey = ['terminal', 'hosts'];
const localLabel = '本地服务器';

type TerminalTab = {
  id: string;
  name: string;
  cwd?: string;
  hostId?: string;
};

export default function TerminalPage() {
  const screens = Grid.useBreakpoint();
  const isMobile = !screens.sm;
  const location = useLocation();
  const cwd = new URLSearchParams(location.search).get('cwd') || '/root';
  const next = useRef(1);
  const [tabs, setTabs] = useState<TerminalTab[]>([
    { id: '1', cwd, name: localLabel },
  ]);
  const [active, setActive] = useState('1');
  const navigate = useNavigate();
  const { section: requested } = useParams();
  const route = useRoutedTab(
    'terminal',
    '/terminal',
    requested,
    ['sessions', 'hosts', 'commands'],
    {
      keys: [],
      tab: new URLSearchParams(location.search).has('cwd') ? 'sessions' : null,
    },
  );
  const section = route.tab;
  function setSection(next: string) {
    if (section !== next) void navigate(`/terminal/${next}${location.search}`);
  }
  const [hostSearch, setHostSearch] = useState('');
  const [launcherOpen, setLauncherOpen] = useState(false);
  const [editingHost, setEditingHost] = useState<TerminalHost | 'new'>();
  const [clearCount, setClearCount] = useState({ id: '', count: 0 });
  const root = useRef<HTMLDivElement>(null);
  const [full, setFull] = useState(false);
  const [pseudoFull, setPseudoFull] = useState(false);
  const isFull = full || pseudoFull;
  const getPopupContainer = isFull ? () => root.current! : undefined;

  const { message } = App.useApp();
  const client = useQueryClient();
  const hosts = useQuery({ queryKey: hostKey, queryFn: terminalHosts });
  const commands = useQuery({
    queryKey: ['toolbox', 'commands'],
    queryFn: () => toolboxData('commands'),
  });
  const hostSave = useMutation({
    mutationFn: (data: TerminalHostInput) =>
      saveTerminalHost(
        data,
        editingHost === 'new' ? undefined : editingHost?.id,
      ),
    onSuccess: async () => {
      setEditingHost(undefined);
      message.success('主机已保存');
      await client.invalidateQueries({ queryKey: hostKey });
    },
  });
  const hostDelete = useMutation({
    mutationFn: deleteTerminalHost,
    onSuccess: () => client.invalidateQueries({ queryKey: hostKey }),
  });
  const settings = useQuery({
    queryKey: ['terminal-settings'],
    queryFn: terminalSettings,
  });
  const fontSize = settings.data?.fontSize ?? 14;
  const fontSizeChange = useMutation({
    mutationFn: saveTerminalSettings,
    onSuccess: (data) => client.setQueryData(['terminal-settings'], data),
  });

  useEffect(() => {
    const update = () => setFull(document.fullscreenElement === root.current);
    document.addEventListener('fullscreenchange', update);
    return () => document.removeEventListener('fullscreenchange', update);
  }, []);

  const toggleFullscreen = async () => {
    if (full) {
      await document.exitFullscreen();
      return;
    }
    if (pseudoFull) {
      setPseudoFull(false);
      return;
    }
    if (root.current?.requestFullscreen) {
      try {
        await root.current.requestFullscreen();
        return;
      } catch {
        // iOS Safari 等浏览器不支持普通 div 全屏时，回退为固定视口。
      }
    }
    setPseudoFull(true);
  };

  const newSession = (host?: TerminalHost) => {
    if (tabs.length >= 5) {
      message.warning('最多同时连接 5 个终端');
      return;
    }
    const id = String(++next.current);
    setTabs((current) => [
      ...current,
      host
        ? { id, name: host.name, hostId: host.id }
        : { id, cwd, name: localLabel },
    ]);
    setActive(id);
    setSection('sessions');
    setLauncherOpen(false);
    setHostSearch('');
  };

  const search = hostSearch.trim().toLowerCase();
  const choices = hosts.data?.filter((host) =>
    [host.name, host.address, host.username].some((value) =>
      value.toLowerCase().includes(search),
    ),
  );
  const limitReached = tabs.length >= 5;
  const launcherContent = (
    <div className="terminal-launcher-popover">
      {limitReached && (
        <Alert
          type="warning"
          showIcon
          message="已达 5 个终端上限，请先关闭部分标签页。"
        />
      )}
      <Flex gap={SPACING.xs}>
        <Button
          type="primary"
          size="small"
          block
          icon={<Server size={14} />}
          disabled={limitReached}
          onClick={() => newSession()}
        >
          连接本地服务器
        </Button>
        <Button
          size="small"
          icon={<Plus size={14} />}
          onClick={() => {
            setLauncherOpen(false);
            setEditingHost('new');
          }}
        >
          新建主机
        </Button>
      </Flex>
      {(hosts.data?.length ?? 0) > 0 && (
        <Input
          size="small"
          allowClear
          aria-label="搜索已保存主机"
          placeholder="搜索 SSH 主机..."
          prefix={<Search size={14} />}
          value={hostSearch}
          onChange={(event) => setHostSearch(event.target.value)}
        />
      )}
      <div className="terminal-launcher-list">
        {hosts.isPending ? (
          <Flex justify="center" align="center" style={{ padding: 20 }}>
            <Spin size="small" />
          </Flex>
        ) : hosts.isError ? (
          <Alert
            type="error"
            message="读取主机失败"
            action={
              <Button size="small" onClick={() => void hosts.refetch()}>
                重试
              </Button>
            }
          />
        ) : choices?.length ? (
          choices.map((host) => (
            <button
              key={host.id}
              type="button"
              className="terminal-launcher-item"
              disabled={limitReached}
              onClick={() => newSession(host)}
            >
              <div className="terminal-launcher-item-top">
                <span className="terminal-launcher-item-name" title={host.name}>
                  {host.name}
                </span>
                <Tag style={{ margin: 0 }}>
                  {host.authType === 'password' ? '密码' : '私钥'}
                </Tag>
              </div>
              <span
                className="terminal-launcher-item-meta"
                title={`${host.username}@${host.address}:${host.port}`}
              >
                {host.username}@
                {host.address.includes(':')
                  ? `[${host.address}]`
                  : host.address}
                :{host.port}
              </span>
            </button>
          ))
        ) : (
          <Typography.Text
            type="secondary"
            style={{ display: 'block', textAlign: 'center', padding: '12px 0' }}
          >
            {search ? '没有匹配的 SSH 主机' : '暂无已保存的 SSH 主机'}
          </Typography.Text>
        )}
      </div>
    </div>
  );

  if (route.invalid) return <RouteNotFound back="/terminal" />;
  return (
    <div
      ref={root}
      className={[
        'terminal-page',
        isFull && 'panel-terminal-fullscreen',
        pseudoFull && 'terminal-pseudo-fullscreen',
      ]
        .filter(Boolean)
        .join(' ')}
    >
      {!isFull && (
        <SectionTabs
          activeKey={section}
          onChange={setSection}
          items={[
            { key: 'sessions', label: '终端' },
            { key: 'hosts', label: '主机' },
            { key: 'commands', label: '常用命令' },
          ]}
        />
      )}
      <div className="terminal-workspace" hidden={section !== 'sessions'}>
        <Tabs
          className="terminal-session-tabs"
          type="editable-card"
          activeKey={tabs.length ? active : ''}
          onChange={setActive}
          hideAdd
          onEdit={(key, action) => {
            if (action !== 'remove') return;
            const remaining = tabs.filter((tab) => tab.id !== key);
            setTabs(remaining);
            if (key === active) setActive(remaining[0]?.id ?? '');
          }}
          items={tabs.map((tab) => ({ key: tab.id, label: tab.name }))}
          tabBarExtraContent={
            <Flex align="center" justify="space-between" gap={SPACING.xs}>
              <Popover
                open={launcherOpen}
                onOpenChange={(open) => {
                  setLauncherOpen(open);
                  if (!open) setHostSearch('');
                }}
                trigger="click"
                placement={isMobile ? 'bottom' : 'bottomLeft'}
                autoAdjustOverflow
                overlayClassName="terminal-launcher-overlay"
                getPopupContainer={getPopupContainer}
                content={launcherContent}
              >
                <Button
                  size="small"
                  type={launcherOpen ? 'primary' : 'default'}
                  aria-label="添加终端"
                  title="新建终端连接"
                  icon={<Plus size={15} />}
                />
              </Popover>
              <Space size={SPACING.xs}>
                {isMobile ? (
                  <Dropdown
                    trigger={['click']}
                    getPopupContainer={getPopupContainer}
                    menu={{
                      items: [
                        {
                          key: 'font-dec',
                          label: `缩小字体（当前 ${fontSize}px）`,
                          disabled:
                            !settings.data ||
                            fontSizeChange.isPending ||
                            fontSize <= 12,
                          onClick: () => fontSizeChange.mutate(fontSize - 1),
                        },
                        {
                          key: 'font-inc',
                          label: `放大字体（当前 ${fontSize}px）`,
                          disabled:
                            !settings.data ||
                            fontSizeChange.isPending ||
                            fontSize >= 20,
                          onClick: () => fontSizeChange.mutate(fontSize + 1),
                        },
                        { type: 'divider' },
                        {
                          key: 'clear',
                          label: '清屏',
                          disabled: tabs.length === 0,
                          onClick: () =>
                            setClearCount({
                              id: active,
                              count: clearCount.count + 1,
                            }),
                        },
                      ],
                    }}
                  >
                    <Button
                      size="small"
                      aria-label="更多终端操作"
                      title="更多操作"
                      icon={<MoreHorizontal size={15} />}
                    />
                  </Dropdown>
                ) : (
                  <>
                    <Button
                      size="small"
                      aria-label="缩小终端字体"
                      disabled={
                        !settings.data ||
                        fontSizeChange.isPending ||
                        fontSize <= 12
                      }
                      onClick={() => fontSizeChange.mutate(fontSize - 1)}
                    >
                      A−
                    </Button>
                    <Button
                      size="small"
                      aria-label="放大终端字体"
                      disabled={
                        !settings.data ||
                        fontSizeChange.isPending ||
                        fontSize >= 20
                      }
                      onClick={() => fontSizeChange.mutate(fontSize + 1)}
                    >
                      A＋
                    </Button>
                    <Button
                      size="small"
                      disabled={tabs.length === 0}
                      onClick={() =>
                        setClearCount({
                          id: active,
                          count: clearCount.count + 1,
                        })
                      }
                    >
                      清屏
                    </Button>
                  </>
                )}
                <Button
                  size="small"
                  type={isFull ? 'primary' : 'default'}
                  aria-label={isFull ? '退出全屏' : '全屏'}
                  title={isFull ? '退出全屏' : '全屏'}
                  icon={
                    isFull ? <Minimize2 size={14} /> : <Maximize2 size={14} />
                  }
                  onClick={() => void toggleFullscreen()}
                />
              </Space>
            </Flex>
          }
        />
        {tabs.length === 0 && (
          <Flex
            flex={1}
            align="center"
            justify="center"
            className="terminal-empty-state"
          >
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="当前没有打开的终端会话"
            >
              <Space>
                <Button
                  type="primary"
                  icon={<Server size={15} />}
                  onClick={() => newSession()}
                >
                  连接本地服务器
                </Button>
                <Button
                  icon={<Plus size={15} />}
                  onClick={() => setLauncherOpen(true)}
                >
                  选择 SSH 主机
                </Button>
              </Space>
            </Empty>
          </Flex>
        )}
        <Suspense fallback={<Spin />}>
          {tabs.map((tab) => (
            <Session
              key={tab.id}
              cwd={tab.cwd}
              hostId={tab.hostId}
              active={section === 'sessions' && active === tab.id}
              fontSize={fontSize}
              clearCount={clearCount.id === tab.id ? clearCount.count : 0}
              commands={commands.data?.list ?? []}
              getPopupContainer={getPopupContainer}
            />
          ))}
        </Suspense>
      </div>
      {section === 'hosts' &&
        (hosts.isError ? (
          <Alert
            type="error"
            message={hosts.error.message}
            action={<Button onClick={() => void hosts.refetch()}>重试</Button>}
          />
        ) : (
          <HostList
            hosts={hosts.data ?? []}
            loading={hosts.isPending}
            pending={hostDelete.isPending}
            onConnect={newSession}
            onAdd={() => setEditingHost('new')}
            onEdit={setEditingHost}
            onDelete={hostDelete.mutateAsync}
          />
        ))}
      {section === 'commands' &&
        (commands.isError ? (
          <Alert
            type="error"
            message={commands.error.message}
            action={
              <Button onClick={() => void commands.refetch()}>重试</Button>
            }
          />
        ) : (
          <Commands
            commands={commands.data?.list ?? []}
            loading={commands.isPending}
          />
        ))}
      {editingHost && (
        <HostForm
          host={editingHost === 'new' ? undefined : editingHost}
          saving={hostSave.isPending}
          onSave={hostSave.mutate}
          onClose={() => {
            if (!hostSave.isPending) setEditingHost(undefined);
          }}
          getContainer={getPopupContainer}
        />
      )}
    </div>
  );
}
