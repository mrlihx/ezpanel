import {
  Suspense,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
} from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { listTasks, subscribeTaskChanges } from '../api/task';
import { useMessageBox } from '../stores/messageBox';
import { usePluginList } from '../api/plugin';
import { pluginIcon } from '../plugins/iconMap';
import {
  App,
  Badge,
  Button,
  Drawer,
  Flex,
  Grid,
  Layout,
  Menu,
  Skeleton,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import {
  Bell,
  Clock,
  Cloud,
  Container,
  Database,
  FolderOpen,
  Globe,
  LayoutDashboard,
  LayoutGrid,
  LogOut,
  Menu as MenuIcon,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  ScrollText,
  Settings,
  Shield,
  ShieldAlert,
  Sun,
  TerminalSquare,
  Wrench,
} from 'lucide-react';
import { matchPath, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { logout } from '../api/auth';
import { panelStatus } from '../api/sysinfo';
import { useAuth } from '../stores/auth';
import { useTheme } from '../stores/theme';
import { LAYOUT, SPACING } from '../styles/tokens';
import MessageBox from '../components/MessageBox';
import PanelUpdateModal from '../components/PanelUpdateModal';
import { useIdleSession } from '../hooks/useIdleSession';
import './MainLayout.css';

export const pages = [
  {
    key: '/',
    label: '概览',
    icon: <LayoutDashboard size={17} strokeWidth={1.85} />,
  },
  {
    key: '/websites',
    label: '网站',
    icon: <Globe size={17} strokeWidth={1.85} />,
  },
  {
    key: '/databases',
    label: '数据库',
    icon: <Database size={17} strokeWidth={1.85} />,
  },
  {
    key: '/docker',
    label: 'Docker',
    icon: <Container size={17} strokeWidth={1.85} />,
  },
  {
    key: '/security',
    label: '安全',
    icon: <Shield size={17} strokeWidth={1.85} />,
  },
  {
    key: '/waf',
    label: 'WAF',
    icon: <ShieldAlert size={17} strokeWidth={1.85} />,
  },
  {
    key: '/files',
    label: '文件',
    icon: <FolderOpen size={17} strokeWidth={1.85} />,
  },
  {
    key: '/crons',
    label: '计划任务',
    icon: <Clock size={17} strokeWidth={1.85} />,
  },
  {
    key: '/terminal',
    label: '终端',
    icon: <TerminalSquare size={17} strokeWidth={1.85} />,
  },
  {
    key: '/oplogs',
    label: '操作日志',
    icon: <ScrollText size={17} strokeWidth={1.85} />,
  },
  {
    key: '/apps',
    label: '应用商店',
    icon: <LayoutGrid size={17} strokeWidth={1.85} />,
  },
  {
    key: '/tools',
    label: '工具',
    icon: <Wrench size={17} strokeWidth={1.85} />,
  },
  {
    key: '/settings',
    label: '设置',
    icon: <Settings size={17} strokeWidth={1.85} />,
  },
];

// iOS 底部液态玻璃导航栏的 5 个核心页签
const mobileDockItems = [
  {
    key: '/',
    label: '概览',
    icon: <LayoutDashboard size={20} strokeWidth={2} />,
  },
  {
    key: '/websites',
    label: '网站',
    icon: <Globe size={20} strokeWidth={2} />,
  },
  {
    key: '/docker',
    label: 'Docker',
    icon: <Container size={20} strokeWidth={2} />,
  },
  {
    key: '/terminal',
    label: '终端',
    icon: <TerminalSquare size={20} strokeWidth={2} />,
  },
  {
    key: '/apps',
    label: '应用商店',
    icon: <LayoutGrid size={20} strokeWidth={2} />,
  },
];

export default function MainLayout() {
  useIdleSession();
  const { token } = theme.useToken();
  const messageBox = useMessageBox();
  const softwareTasks = useQuery({
    queryKey: ['software-tasks', 'running'],
    queryFn: ({ signal }) =>
      listTasks({ module: 'software', status: 'running' }, signal),
  });
  const client = useQueryClient();
  useEffect(
    () =>
      subscribeTaskChanges('software', () => {
        void client.invalidateQueries({
          queryKey: ['software-tasks', 'running'],
        });
        void client.invalidateQueries({ queryKey: ['clamav-history'] });
      }),
    [client],
  );
  const { modal, message } = App.useApp();
  const [updateModalOpen, setUpdateModalOpen] = useState(false);
  const panelStatusQuery = useQuery({
    queryKey: ['panel-status'],
    queryFn: ({ signal }) => panelStatus(signal),
    staleTime: 5 * 60 * 1000,
    refetchInterval: 30 * 60 * 1000,
  });
  const screens = Grid.useBreakpoint();
  // Grid.useBreakpoint() 首帧同步渲染时返回空对象，直接用 matchMedia 作为初始值，
  // 避免桌面端刷新时首帧 marginInlineStart 为 0、次帧再过渡到 224px 导致骨架屏从左侧菜单栏处闪出。
  const lg =
    screens.lg ??
    (typeof window !== 'undefined'
      ? window.matchMedia(`(min-width: ${LAYOUT.mobileBreakpoint}px)`).matches
      : true);
  const [collapsed, setCollapsed] = useState(false);
  const [drawer, setDrawer] = useState(false);
  const navCollapsed = !!lg && collapsed;
  const contentPadding = lg ? SPACING.xl : SPACING.lg;
  const mobileTopHeaderHeight = 52;
  const mobileBottomDockSpace = 88;
  const mobileMenuSpace = lg
    ? 0
    : mobileTopHeaderHeight + mobileBottomDockSpace;
  const location = useLocation();
  const pluginQuery = usePluginList(true);
  const pluginMenus = (pluginQuery.data ?? [])
    .filter((p) => p.runtimeStatus === 'running' && p.menu)
    .sort((a, b) => a.menu!.order - b.menu!.order || a.id.localeCompare(b.id))
    .map((p) => ({
      key: `/p/${p.id}`,
      label: p.menu!.title,
      icon: pluginIcon(p.menu!.icon),
    }));
  const navigate = useNavigate();
  const user = useAuth((s) => s.user);
  const setUser = useAuth((s) => s.setUser);

  // 移动端弹窗或抽屉内多行文本框 / 日志框 / 代码编辑器独立上下滑动保护，阻止滚动链穿透到底层页面
  useEffect(() => {
    let lastY = 0;
    const findScrollableInOverlay = (
      target: EventTarget | null,
    ): HTMLElement | null => {
      if (!(target instanceof HTMLElement)) return null;
      const overlay = target.closest<HTMLElement>(
        '.ant-modal-content, .ant-drawer-content',
      );
      if (!overlay) return null;
      let el: HTMLElement | null = target;
      while (el && el !== overlay) {
        if (
          el.classList.contains('panel-scroll-box') ||
          el.classList.contains('logs-output') ||
          el.classList.contains('docker-container-log') ||
          el.getAttribute('role') === 'region' ||
          el.classList.contains('monaco-scrollable-element')
        ) {
          return el;
        }
        const style = window.getComputedStyle(el);
        if (
          (style.overflowY === 'auto' || style.overflowY === 'scroll') &&
          el.scrollHeight > el.clientHeight + 1
        ) {
          return el;
        }
        el = el.parentElement;
      }
      return null;
    };

    const onTouchStart = (e: TouchEvent) => {
      if (e.touches.length === 1) {
        lastY = e.touches[0].clientY;
      }
      if (e.target instanceof HTMLElement) {
        const tab = e.target.closest<HTMLElement>(
          '.ant-tabs-top > .ant-tabs-nav .ant-tabs-tab',
        );
        const navWrap = tab?.closest<HTMLElement>('.ant-tabs-nav-wrap');
        if (navWrap) {
          navWrap.dataset.savedScrollLeft = String(navWrap.scrollLeft);
          navWrap.dataset.touchStartX = String(e.touches[0].clientX);
          navWrap.dataset.touchStartY = String(e.touches[0].clientY);
        }
      }
    };

    const onPointerDown = (e: PointerEvent) => {
      if (e.target instanceof HTMLElement) {
        const navWrap = e.target.closest<HTMLElement>(
          '.ant-tabs-top > .ant-tabs-nav .ant-tabs-nav-wrap',
        );
        if (navWrap) {
          navWrap.dataset.savedScrollLeft = String(navWrap.scrollLeft);
        }
      }
    };

    const onTouchEnd = (e: TouchEvent) => {
      if (!(e.target instanceof HTMLElement)) return;
      // 如果轻点的是可编辑 Tab 的关闭按钮（×），不介入切换或滚动
      if (e.target.closest('.ant-tabs-tab-remove')) return;
      const tab = e.target.closest<HTMLElement>(
        '.ant-tabs-top > .ant-tabs-nav .ant-tabs-tab',
      );
      const navWrap = tab?.closest<HTMLElement>('.ant-tabs-nav-wrap');
      if (!tab || !navWrap || e.changedTouches.length !== 1) return;
      const saved = Number(navWrap.dataset.savedScrollLeft || 0);
      if (navWrap.scrollLeft === 0 && saved > 2) {
        navWrap.scrollLeft = saved;
      }
      const dx = Math.abs(
        e.changedTouches[0].clientX - Number(navWrap.dataset.touchStartX || 0),
      );
      const dy = Math.abs(
        e.changedTouches[0].clientY - Number(navWrap.dataset.touchStartY || 0),
      );
      // 如果是轻点（非滑动拖拽），立即确保 Tab 点击被触发并平滑居中
      if (dx < 10 && dy < 10) {
        const tabBtn =
          tab.querySelector<HTMLElement>('.ant-tabs-tab-btn') ?? tab;
        // 在 iOS Safari / Android WebView 上，如果 focus 被拦截或容器刚经历滚动，合成 click 可能被吞掉；
        // 用 tappedAt 标记本次轻点，若原生 click 正常触发会在 onClickCapture 中清除 tappedAt；
        // 仅当原生 click 未触发且 Tab 仍未激活时才补发一次 click
        tab.dataset.tappedAt = String(Date.now());
        window.setTimeout(() => {
          if (
            tab.dataset.tappedAt &&
            !tab.classList.contains('ant-tabs-tab-active') &&
            !tab.classList.contains('ant-tabs-tab-disabled')
          ) {
            delete tab.dataset.tappedAt;
            tabBtn.click();
          } else {
            delete tab.dataset.tappedAt;
          }
        }, 32);

        if (navWrap.scrollWidth > navWrap.clientWidth + 2) {
          window.requestAnimationFrame(() => {
            const targetLeft =
              tab.offsetLeft - (navWrap.clientWidth - tab.offsetWidth) / 2;
            const maxScroll = Math.max(
              0,
              navWrap.scrollWidth - navWrap.clientWidth,
            );
            const clamped = Math.max(0, Math.min(maxScroll, targetLeft));
            navWrap.dataset.savedScrollLeft = String(clamped);
            navWrap.scrollTo({ left: clamped, behavior: 'smooth' });
          });
        }
      }
    };

    const onFocusIn = (e: FocusEvent) => {
      if (!(e.target instanceof HTMLElement)) return;
      if (e.target.closest('.ant-tabs-tab-remove')) return;
      const tab = e.target.closest<HTMLElement>(
        '.ant-tabs-top > .ant-tabs-nav .ant-tabs-tab',
      );
      const navWrap = tab?.closest<HTMLElement>('.ant-tabs-nav-wrap');
      if (!tab || !navWrap) return;
      const saved = Number(navWrap.dataset.savedScrollLeft || 0);
      if (navWrap.scrollLeft === 0 && saved > 0) {
        navWrap.scrollLeft = saved;
      }
    };

    let lastOverlayCloseAt = 0;

    const onClickCapture = (e: MouseEvent) => {
      if (!(e.target instanceof HTMLElement)) return;
      // 移动端防鬼点（Ghost Click）与关闭模态框/抽屉后的焦点回弹保护：
      // 当点击 Modal / Drawer 的关闭按钮或遮罩时，记录关闭时间戳并主动 blur 当前焦点，
      // 并在随后的 350ms 窗口内阻止穿透到底层按钮的合成 click，避免关闭后又自动重新弹出一个窗口
      const isTouch = window.matchMedia('(pointer: coarse)').matches;
      const isCloseTrigger =
        isTouch &&
        e.target.closest(
          '.ant-modal-close, .ant-drawer-close, .ant-modal-confirm-btns .ant-btn',
        );
      if (isCloseTrigger) {
        lastOverlayCloseAt = Date.now();
        if (document.activeElement instanceof HTMLElement) {
          document.activeElement.blur();
        }
        return;
      }
      if (
        isTouch &&
        Date.now() - lastOverlayCloseAt < 320 &&
        !e.target.closest('.ant-modal, .ant-drawer')
      ) {
        e.stopPropagation();
        e.preventDefault();
        return;
      }

      const tab = e.target.closest<HTMLElement>(
        '.ant-tabs-top > .ant-tabs-nav .ant-tabs-tab',
      );
      if (tab) {
        delete tab.dataset.tappedAt;
      }
      if (e.target.closest('.ant-tabs-tab-remove')) return;
      const navWrap = tab?.closest<HTMLElement>('.ant-tabs-nav-wrap');
      if (!tab || !navWrap) return;
      if (navWrap.scrollWidth <= navWrap.clientWidth + 2) return;
      const saved = Number(navWrap.dataset.savedScrollLeft || 0);
      if (navWrap.scrollLeft === 0 && saved > 0) {
        navWrap.scrollLeft = saved;
      }
      window.requestAnimationFrame(() => {
        if (navWrap.scrollLeft === 0 && saved > 0) {
          navWrap.scrollLeft = saved;
        }
        const targetLeft =
          tab.offsetLeft - (navWrap.clientWidth - tab.offsetWidth) / 2;
        const maxScroll = Math.max(
          0,
          navWrap.scrollWidth - navWrap.clientWidth,
        );
        const clamped = Math.max(0, Math.min(maxScroll, targetLeft));
        navWrap.dataset.savedScrollLeft = String(clamped);
        navWrap.scrollTo({ left: clamped, behavior: 'smooth' });
      });
    };

    const onTouchMove = (e: TouchEvent) => {
      if (e.touches.length !== 1) return;
      const currentY = e.touches[0].clientY;
      const deltaY = lastY - currentY;
      lastY = currentY;
      const scrollBox = findScrollableInOverlay(e.target);
      if (!scrollBox) return;
      const canScroll = scrollBox.scrollHeight > scrollBox.clientHeight + 1;
      if (!canScroll) {
        return;
      }
      const atTop = scrollBox.scrollTop <= 0 && deltaY < 0;
      const atBottom =
        scrollBox.scrollTop + scrollBox.clientHeight >=
          scrollBox.scrollHeight - 1 && deltaY > 0;
      if ((atTop || atBottom) && e.cancelable) {
        e.preventDefault();
      }
    };

    document.addEventListener('touchstart', onTouchStart, {
      passive: true,
      capture: true,
    });
    document.addEventListener('touchend', onTouchEnd, {
      passive: true,
      capture: true,
    });
    document.addEventListener('pointerdown', onPointerDown, {
      passive: true,
      capture: true,
    });
    document.addEventListener('focusin', onFocusIn, {
      passive: true,
      capture: true,
    });
    document.addEventListener('click', onClickCapture, {
      passive: false,
      capture: true,
    });
    document.addEventListener('touchmove', onTouchMove, {
      passive: false,
      capture: true,
    });
    return () => {
      document.removeEventListener('touchstart', onTouchStart, {
        capture: true,
      });
      document.removeEventListener('touchend', onTouchEnd, {
        capture: true,
      });
      document.removeEventListener('pointerdown', onPointerDown, {
        capture: true,
      });
      document.removeEventListener('focusin', onFocusIn, {
        capture: true,
      });
      document.removeEventListener('click', onClickCapture, {
        capture: true,
      });
      document.removeEventListener('touchmove', onTouchMove, {
        capture: true,
      });
    };
  }, []);
  const themeState = useTheme();
  const dark =
    themeState.mode === 'dark' ||
    (themeState.mode === 'system' && themeState.systemDark);
  const exit = async () => {
    await logout();
    setUser(null);
    window.location.replace('/login');
  };

  const selectedKey =
    [...pages, ...pluginMenus].find(({ key }) =>
      matchPath({ path: key, end: key === '/' }, location.pathname),
    )?.key ?? location.pathname;

  const currentPageTitle =
    pages.find((p) => p.key === selectedKey)?.label ||
    pluginMenus.find((p) => p.key === selectedKey)?.label ||
    user?.alias ||
    'ezpanel';

  useEffect(() => {
    const brand = user?.alias || 'ezpanel';
    document.title =
      currentPageTitle === brand ? brand : `${currentPageTitle} · ${brand}`;
  }, [currentPageTitle, user?.alias]);

  const activeDockIndex = mobileDockItems.findIndex(
    (item) => item.key === selectedKey,
  );
  const dockBarRef = useRef<HTMLDivElement>(null);
  const [dockDrag, setDockDrag] = useState<{
    floatIndex: number;
    hoverIndex: number;
    velocity: number;
  } | null>(null);
  // 路由切换期间保留目标位置，避免松手后指示器闪回。
  const [pendingDock, setPendingDock] = useState<{
    from: string;
    index: number;
  } | null>(null);
  if (pendingDock && pendingDock.from !== selectedKey) {
    setPendingDock(null);
  }
  const pendingDockIndex = pendingDock?.index ?? null;
  const dockDragStateRef = useRef<{
    startX: number;
    lastX: number;
    moved: boolean;
    hoverIndex: number;
  } | null>(null);

  useEffect(() => {
    const bar = dockBarRef.current;
    if (!bar) return;
    const calcFromClientX = (clientX: number, prevX: number) => {
      const rect = bar.getBoundingClientRect();
      const pad = 5;
      const innerWidth = Math.max(1, rect.width - pad * 2);
      const count = mobileDockItems.length;
      const cellWidth = innerWidth / count;
      const relX = clientX - rect.left - pad;
      const rawFloat = (relX - cellWidth / 2) / cellWidth;
      const floatIndex = Math.max(0, Math.min(count - 1, rawFloat));
      const hoverIndex = Math.max(
        0,
        Math.min(count - 1, Math.round(floatIndex)),
      );
      const velocity = Math.max(-1, Math.min(1, (clientX - prevX) / 18));
      return { floatIndex, hoverIndex, velocity };
    };

    const onTouchStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) return;
      // 阻止 iOS Safari / Android 浏览器边缘左滑右滑触发网页后退/前进手势
      if (e.cancelable) e.preventDefault();
      const clientX = e.touches[0].clientX;
      const { floatIndex, hoverIndex } = calcFromClientX(clientX, clientX);
      dockDragStateRef.current = {
        startX: clientX,
        lastX: clientX,
        moved: false,
        hoverIndex,
      };
      setPendingDock(null);
      setDockDrag({ floatIndex, hoverIndex, velocity: 0 });
    };

    const onTouchMove = (e: TouchEvent) => {
      const st = dockDragStateRef.current;
      if (!st || e.touches.length !== 1) return;
      if (e.cancelable) e.preventDefault();
      const clientX = e.touches[0].clientX;
      if (Math.abs(clientX - st.startX) > 4) {
        st.moved = true;
      }
      const { floatIndex, hoverIndex, velocity } = calcFromClientX(
        clientX,
        st.lastX,
      );
      st.lastX = clientX;
      st.hoverIndex = hoverIndex;
      setDockDrag({ floatIndex, hoverIndex, velocity });
    };

    const finishTouch = () => {
      const st = dockDragStateRef.current;
      if (!st) return;
      dockDragStateRef.current = null;
      const targetIndex = st.hoverIndex;
      const targetItem = mobileDockItems[targetIndex];
      setDockDrag(null);
      if (targetItem && targetItem.key !== selectedKey) {
        setPendingDock({ from: selectedKey, index: targetIndex });
        navigate(targetItem.key);
      }
    };

    bar.addEventListener('touchstart', onTouchStart, { passive: false });
    bar.addEventListener('touchmove', onTouchMove, { passive: false });
    bar.addEventListener('touchend', finishTouch, { passive: true });
    bar.addEventListener('touchcancel', finishTouch, { passive: true });
    return () => {
      bar.removeEventListener('touchstart', onTouchStart);
      bar.removeEventListener('touchmove', onTouchMove);
      bar.removeEventListener('touchend', finishTouch);
      bar.removeEventListener('touchcancel', finishTouch);
    };
  }, [lg, navigate, selectedKey]);

  const toggleLabel = lg ? (collapsed ? '展开菜单' : '收起菜单') : '关闭菜单';
  const themeLabel = dark ? '切换浅色模式' : '切换深色模式';
  const themeButton = (
    <Tooltip title={themeLabel} placement="right">
      <Button
        type="text"
        className="panel-sidebar-action"
        aria-label={themeLabel}
        icon={
          dark ? (
            <Sun size={17} strokeWidth={1.85} />
          ) : (
            <Moon size={17} strokeWidth={1.85} />
          )
        }
        onClick={() => themeState.setMode(dark ? 'light' : 'dark')}
      />
    </Tooltip>
  );
  const navigation = (
    <div
      className="panel-sidebar-inner"
      style={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        color: token.colorText,
      }}
    >
      <Flex
        align="center"
        justify={navCollapsed ? 'center' : undefined}
        gap={SPACING.sm}
        style={{
          height: LAYOUT.headerHeight,
          flexShrink: 0,
          paddingInline: navCollapsed ? SPACING.xs : SPACING.lg,
        }}
      >
        {!navCollapsed && (
          <div className="panel-brand-logo">
            <Cloud size={18} strokeWidth={2.2} />
          </div>
        )}
        {!navCollapsed && (
          <Typography.Text
            style={{
              color: token.colorText,
              flex: 1,
              minWidth: 0,
              fontSize: 15,
              fontWeight: 600,
              letterSpacing: '-0.02em',
            }}
            ellipsis
          >
            {user?.alias || 'ezpanel'}
          </Typography.Text>
        )}
        <Tooltip title="消息盒子">
          <Button
            type="text"
            className="panel-sidebar-action"
            aria-label="消息盒子"
            style={{ flexShrink: 0 }}
            onClick={() => {
              setDrawer(false);
              messageBox.show();
            }}
          >
            <Badge
              count={softwareTasks.data?.total ?? 0}
              size="small"
              dot={!!softwareTasks.error}
            >
              <Bell
                size={17}
                strokeWidth={1.85}
                style={{
                  color: token.colorTextSecondary,
                  verticalAlign: 'middle',
                }}
              />
            </Badge>
          </Button>
        </Tooltip>
      </Flex>
      <Menu
        className="panel-apple-menu"
        mode="inline"
        style={{
          flex: 1,
          minHeight: 0,
          overflowY: 'auto',
          borderInlineEnd: 'none',
        }}
        selectedKeys={[selectedKey]}
        items={[
          ...pages,
          {
            key: 'logout',
            label: '退出',
            icon: <LogOut size={17} strokeWidth={1.85} />,
            className: 'panel-menu-logout',
          },
          ...(pluginMenus.length
            ? [
                {
                  key: 'plugin-group',
                  type: 'group' as const,
                  label: '扩展应用',
                  children: pluginMenus,
                },
              ]
            : []),
        ]}
        onClick={({ key }) => {
          setDrawer(false);
          if (key === 'logout') {
            modal.confirm({
              title: '确认退出登录？',
              okText: '退出',
              cancelText: '取消',
              okButtonProps: { danger: true },
              autoFocusButton: 'cancel',
              onOk: exit,
            });
            return;
          }
          navigate(key);
        }}
      />
      <Flex
        className="panel-sidebar-footer"
        vertical={navCollapsed}
        align="center"
        justify="space-between"
        gap={SPACING.xs}
        style={{
          padding: navCollapsed ? SPACING.sm : SPACING.md,
          borderTop: `1px solid ${token.colorBorderSecondary}`,
        }}
      >
        {navCollapsed && themeButton}
        <Tooltip title={toggleLabel} placement="right">
          <Button
            type="text"
            className="panel-sidebar-action"
            aria-label={toggleLabel}
            icon={
              navCollapsed ? (
                <PanelLeftOpen size={17} strokeWidth={1.85} />
              ) : (
                <PanelLeftClose size={17} strokeWidth={1.85} />
              )
            }
            onClick={() => {
              if (lg) setCollapsed((value) => !value);
              else setDrawer(false);
            }}
          />
        </Tooltip>
        {!navCollapsed && themeButton}
      </Flex>
    </div>
  );
  return (
    <Layout
      className={`panel-layout ${dark ? 'panel-theme-dark' : 'panel-theme-light'}`}
      style={{ minHeight: '100dvh' }}
    >
      {lg ? (
        <Layout.Sider
          className="panel-apple-sider"
          width={LAYOUT.siderWidth}
          collapsedWidth={LAYOUT.siderCollapsedWidth}
          collapsed={collapsed}
          style={{
            position: 'fixed',
            insetBlock: 0,
            insetInlineStart: 0,
            height: '100dvh',
            borderInlineEnd: `1px solid ${token.colorBorderSecondary}`,
          }}
        >
          {navigation}
        </Layout.Sider>
      ) : (
        <Drawer
          closable={false}
          placement="left"
          width={LAYOUT.siderWidth}
          open={drawer}
          onClose={() => setDrawer(false)}
          styles={{
            body: {
              padding: 0,
              background: dark ? '#1C1F24' : '#FFFFFF',
            },
          }}
        >
          {navigation}
        </Drawer>
      )}
      <Layout
        style={{
          marginInlineStart: lg
            ? collapsed
              ? LAYOUT.siderCollapsedWidth
              : LAYOUT.siderWidth
            : 0,
          minWidth: 0,
          transition:
            'margin-inline-start 0.24s cubic-bezier(0.32, 0.72, 0.28, 1)',
        }}
      >
        {!lg && (
          <header className="panel-mobile-header">
            <Button
              type="text"
              className="panel-sidebar-action"
              aria-label="展开菜单"
              aria-expanded={drawer}
              icon={<MenuIcon size={20} strokeWidth={2} />}
              onClick={() => setDrawer(true)}
            />
            <span className="panel-mobile-header__title">
              {currentPageTitle}
            </span>
            <Flex align="center" gap={2}>
              <Button
                type="text"
                className="panel-sidebar-action"
                aria-label="消息盒子"
                onClick={() => messageBox.show()}
              >
                <Badge
                  count={softwareTasks.data?.total ?? 0}
                  size="small"
                  dot={!!softwareTasks.error}
                >
                  <Bell
                    size={18}
                    strokeWidth={1.9}
                    style={{
                      color: token.colorTextSecondary,
                      verticalAlign: 'middle',
                    }}
                  />
                </Badge>
              </Button>
              <Button
                type="text"
                className="panel-sidebar-action"
                aria-label={themeLabel}
                icon={
                  dark ? (
                    <Sun size={18} strokeWidth={1.9} />
                  ) : (
                    <Moon size={18} strokeWidth={1.9} />
                  )
                }
                onClick={() => themeState.setMode(dark ? 'light' : 'dark')}
              />
            </Flex>
          </header>
        )}
        <Layout.Content
          style={
            {
              padding: contentPadding,
              paddingBottom: lg ? SPACING.md : SPACING.md,
              minWidth: 0,
              '--panel-content-height': `calc(100dvh - ${contentPadding + SPACING.md + (lg ? 42 : mobileMenuSpace + 48)}px)`,
            } as CSSProperties
          }
        >
          <Suspense fallback={<Skeleton active />}>
            <Outlet />
          </Suspense>
        </Layout.Content>
        <footer
          className="panel-footer"
          style={{
            paddingBottom: lg ? 10 : mobileBottomDockSpace + 8,
          }}
        >
          <div className="panel-footer__inner">
            {panelStatusQuery.data?.publicIp && (
              <Tooltip title="点击复制外网 IP">
                <button
                  type="button"
                  className="panel-footer__ip"
                  onClick={() => {
                    const ip = panelStatusQuery.data?.publicIp;
                    if (!ip) return;
                    void navigator.clipboard
                      ?.writeText(ip)
                      .then(() => message.success(`已复制外网 IP：${ip}`))
                      .catch(() => message.info(`外网 IP：${ip}`));
                  }}
                >
                  <span className="panel-footer__ip-label">IP</span>
                  <span className="panel-footer__ip-val">
                    {panelStatusQuery.data.publicIp}
                  </span>
                </button>
              </Tooltip>
            )}
            <span className="panel-footer__brand">
              © EzPanel
              {panelStatusQuery.data?.panelVersion && (
                <span className="panel-footer__ver">
                  v{panelStatusQuery.data.panelVersion}
                </span>
              )}
            </span>
            <span className="panel-footer__dot" aria-hidden="true">
              ·
            </span>
            <a
              className="panel-footer__link"
              href={
                panelStatusQuery.data?.repoUrl ||
                'https://github.com/mrlihx/ezpanel'
              }
              target="_blank"
              rel="noreferrer"
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="currentColor"
                aria-hidden="true"
              >
                <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0 0 24 12c0-6.63-5.37-12-12-12z" />
              </svg>
              <span>GitHub ↗</span>
            </a>
            <span className="panel-footer__dot" aria-hidden="true">
              ·
            </span>
            <Badge
              dot={Boolean(panelStatusQuery.data?.hasUpdate)}
              offset={[-2, 2]}
            >
              <button
                type="button"
                className="panel-footer__update"
                onClick={() => setUpdateModalOpen(true)}
              >
                {panelStatusQuery.data?.hasUpdate
                  ? `发现新版 v${panelStatusQuery.data.latestVersion}`
                  : '检查更新'}
              </button>
            </Badge>
          </div>
        </footer>
      </Layout>
      {!lg && (
        <nav className="panel-ios-dock" aria-label="底部快捷导航">
          <div
            ref={dockBarRef}
            className={`panel-ios-dock__bar ${dockDrag ? 'is-dragging' : ''}`}
          >
            {(activeDockIndex >= 0 ||
              pendingDockIndex !== null ||
              dockDrag) && (
              <div
                className={`panel-ios-dock__lens ${dockDrag ? 'is-dragging' : ''}`}
                aria-hidden="true"
                style={
                  {
                    '--dock-index': dockDrag
                      ? dockDrag.floatIndex
                      : (pendingDockIndex ?? Math.max(0, activeDockIndex)),
                    '--dock-stretch': dockDrag
                      ? 1 + Math.min(0.18, Math.abs(dockDrag.velocity) * 0.18)
                      : 1,
                    '--dock-squeeze': dockDrag
                      ? 1 - Math.min(0.08, Math.abs(dockDrag.velocity) * 0.08)
                      : 1,
                  } as CSSProperties
                }
              />
            )}
            {mobileDockItems.map((item, idx) => {
              const currentVisualIndex = dockDrag
                ? dockDrag.hoverIndex
                : (pendingDockIndex ?? activeDockIndex);
              const active = currentVisualIndex === idx;
              return (
                <button
                  key={item.key}
                  type="button"
                  className={`panel-ios-dock__item ${active ? 'is-active' : ''} ${
                    dockDrag && dockDrag.hoverIndex === idx ? 'is-hovered' : ''
                  }`}
                  aria-current={selectedKey === item.key ? 'page' : undefined}
                  onClick={() => {
                    if (item.key === selectedKey) return;
                    setPendingDock({ from: selectedKey, index: idx });
                    navigate(item.key);
                  }}
                >
                  <span className="panel-ios-dock__icon">{item.icon}</span>
                  <span className="panel-ios-dock__label">{item.label}</span>
                </button>
              );
            })}
          </div>
        </nav>
      )}
      <MessageBox running={softwareTasks} />
      <PanelUpdateModal
        open={updateModalOpen}
        onClose={() => setUpdateModalOpen(false)}
      />
    </Layout>
  );
}
