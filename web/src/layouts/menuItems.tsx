import type { ReactNode } from 'react';
import {
  Blocks,
  Clock,
  Container,
  Database,
  FileText,
  Folder,
  FolderOpen,
  Globe,
  Layers,
  LayoutDashboard,
  LayoutGrid,
  LogOut,
  ScrollText,
  Server,
  Settings,
  Shield,
  ShieldAlert,
  TerminalSquare,
  Wrench,
} from 'lucide-react';
import type { MenuGroup } from '../api/settings';

export type SidebarPageItem = {
  key: string;
  label: string;
  icon: ReactNode;
};

export const lockedMenuKeys = new Set(['/', '/apps', '/settings', 'logout']);
export const pinnedMenuKeys = new Set(['/', 'logout']);
export const EXTENSIONS_GROUP_ID = 'extensions';
export const EXTENSIONS_GROUP_KEY = 'group:extensions';
export const menuGroupKey = (id: string) => `group:${id}`;

export const menuGroupIconOptions: Array<{
  value: string;
  label: string;
  icon: ReactNode;
}> = [
  {
    value: 'folder',
    label: '文件夹',
    icon: <Folder size={17} strokeWidth={1.85} />,
  },
  {
    value: 'blocks',
    label: '扩展模块',
    icon: <Blocks size={17} strokeWidth={1.85} />,
  },
  {
    value: 'layers',
    label: '多层分组',
    icon: <Layers size={17} strokeWidth={1.85} />,
  },
  {
    value: 'server',
    label: '服务器',
    icon: <Server size={17} strokeWidth={1.85} />,
  },
  {
    value: 'globe',
    label: '网络站点',
    icon: <Globe size={17} strokeWidth={1.85} />,
  },
  {
    value: 'shield',
    label: '安全防护',
    icon: <Shield size={17} strokeWidth={1.85} />,
  },
  {
    value: 'wrench',
    label: '运维工具',
    icon: <Wrench size={17} strokeWidth={1.85} />,
  },
  {
    value: 'database',
    label: '数据库',
    icon: <Database size={17} strokeWidth={1.85} />,
  },
  {
    value: 'terminal',
    label: '终端命令',
    icon: <TerminalSquare size={17} strokeWidth={1.85} />,
  },
  {
    value: 'settings',
    label: '系统设置',
    icon: <Settings size={17} strokeWidth={1.85} />,
  },
  {
    value: 'layout-grid',
    label: '应用集合',
    icon: <LayoutGrid size={17} strokeWidth={1.85} />,
  },
  {
    value: 'file-text',
    label: '文档日志',
    icon: <FileText size={17} strokeWidth={1.85} />,
  },
];

const menuGroupIconMap = new Map(
  menuGroupIconOptions.map((opt) => [opt.value, opt.icon]),
);

export function renderMenuGroupIcon(icon?: string): ReactNode {
  return (
    menuGroupIconMap.get(icon ?? 'folder') ?? menuGroupIconMap.get('folder')
  );
}

export const pages: SidebarPageItem[] = [
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

export const logoutMenuItem: SidebarPageItem = {
  key: 'logout',
  label: '退出',
  icon: <LogOut size={17} strokeWidth={1.85} />,
};

export function sortByMenuOrder<T extends { key: string }>(
  items: T[],
  order: string[],
): T[] {
  if (!order.length) return items;
  const index = new Map(order.map((key, idx) => [key, idx]));
  return [...items].sort((a, b) => {
    const ai = index.get(a.key);
    const bi = index.get(b.key);
    if (ai !== undefined && bi !== undefined) return ai - bi;
    if (ai !== undefined) return -1;
    if (bi !== undefined) return 1;
    return 0;
  });
}

export type MenuLayoutNode<T extends { key: string }> =
  | { key: string; type: 'item'; item: T }
  | { key: string; type: 'group'; group: MenuGroup; items: T[] };

export function buildMenuLayout<T extends { key: string }>({
  builtinItems,
  pluginItems,
  menuOrder,
  menuGroups,
  hideEmptyGroups = true,
}: {
  builtinItems: T[];
  pluginItems: T[];
  menuOrder: string[];
  menuGroups: MenuGroup[];
  hideEmptyGroups?: boolean;
}): {
  nodes: MenuLayoutNode<T>[];
  groupByItemKey: Map<string, string>;
} {
  const leafMap = new Map<string, T>();
  for (const item of builtinItems) leafMap.set(item.key, item);
  for (const item of pluginItems) leafMap.set(item.key, item);

  const assignedKeys = new Set<string>();
  const normalizedGroups: MenuGroup[] = [];
  const seenGroupIds = new Set<string>();

  for (const g of menuGroups) {
    if (!g.id || seenGroupIds.has(g.id)) continue;
    seenGroupIds.add(g.id);
    const items: string[] = [];
    for (const k of g.items ?? []) {
      if (leafMap.has(k) && !assignedKeys.has(k)) {
        assignedKeys.add(k);
        items.push(k);
      }
    }
    normalizedGroups.push({
      id: g.id,
      title: g.title,
      icon: g.icon || (g.id === EXTENSIONS_GROUP_ID ? 'blocks' : 'folder'),
      items,
    });
  }

  const orderSet = new Set(menuOrder);
  const unassignedPlugins = pluginItems.filter(
    (p) => !assignedKeys.has(p.key) && !orderSet.has(p.key),
  );
  if (unassignedPlugins.length > 0) {
    const pluginKeys = unassignedPlugins.map((p) => p.key);
    for (const k of pluginKeys) assignedKeys.add(k);
    const existingExt = normalizedGroups.find(
      (g) => g.id === EXTENSIONS_GROUP_ID,
    );
    if (existingExt) {
      existingExt.items.push(...pluginKeys);
    } else {
      normalizedGroups.push({
        id: EXTENSIONS_GROUP_ID,
        title: '扩展应用',
        icon: 'blocks',
        items: pluginKeys,
      });
    }
  }

  const visibleGroups = normalizedGroups.filter((g) => {
    if (g.id === EXTENSIONS_GROUP_ID) return g.items.length > 0;
    return hideEmptyGroups ? g.items.length > 0 : true;
  });

  const topLeafNodes: MenuLayoutNode<T>[] = [...builtinItems, ...pluginItems]
    .filter((item) => !assignedKeys.has(item.key))
    .map((item) => ({ key: item.key, type: 'item', item }));

  const groupNodes: MenuLayoutNode<T>[] = visibleGroups.map((group) => ({
    key: menuGroupKey(group.id),
    type: 'group',
    group,
    items: group.items.map((k) => leafMap.get(k)!).filter(Boolean),
  }));

  const extNode = groupNodes.find((n) => n.key === EXTENSIONS_GROUP_KEY);
  const shouldInsertExtAfterApps =
    !!extNode && !orderSet.has(EXTENSIONS_GROUP_KEY);

  const sortableNodes = shouldInsertExtAfterApps
    ? [
        ...topLeafNodes,
        ...groupNodes.filter((n) => n.key !== EXTENSIONS_GROUP_KEY),
      ]
    : [...topLeafNodes, ...groupNodes];

  const sortedNodes = sortByMenuOrder(sortableNodes, menuOrder);

  if (shouldInsertExtAfterApps && extNode) {
    const appsIndex = sortedNodes.findIndex((n) => n.key === '/apps');
    if (appsIndex >= 0) {
      sortedNodes.splice(appsIndex + 1, 0, extNode);
    } else {
      sortedNodes.push(extNode);
    }
  }

  const groupByItemKey = new Map<string, string>();
  for (const node of sortedNodes) {
    if (node.type === 'group') {
      for (const item of node.items) {
        groupByItemKey.set(item.key, node.key);
      }
    }
  }

  return { nodes: sortedNodes, groupByItemKey };
}
