import { useMemo, useState } from 'react';
import {
  Button,
  Dropdown,
  Flex,
  Input,
  Modal,
  Popconfirm,
  Space,
  Switch,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  ChevronRight,
  FolderInput,
  FolderOutput,
  GripVertical,
  Lock,
  Pencil,
  Plus,
  RotateCcw,
  Trash2,
} from 'lucide-react';
import type { MenuGroup } from '../../api/settings';
import { usePluginList } from '../../api/plugin';
import {
  buildMenuLayout,
  EXTENSIONS_GROUP_ID,
  lockedMenuKeys,
  logoutMenuItem,
  menuGroupIconOptions,
  menuGroupKey,
  pages,
  renderMenuGroupIcon,
  type SidebarPageItem,
} from '../../layouts/menuItems';
import { pluginIcon } from '../../plugins/iconMap';
import { SPACING } from '../../styles/tokens';

type ModalMenuItem = SidebarPageItem & {
  isPlugin?: boolean;
  running?: boolean;
};

type DraftNode =
  | { key: string; type: 'item' }
  | { key: string; type: 'group'; group: MenuGroup };

type DragPayload =
  | { kind: 'top'; key: string }
  | { kind: 'child'; key: string; groupId: string };

type MenuVisibilityModalProps = {
  open: boolean;
  hiddenMenus: string[];
  menuOrder: string[];
  menuGroups: MenuGroup[];
  saving: boolean;
  onSave: (payload: {
    hiddenMenus: string[];
    menuOrder: string[];
    menuGroups: MenuGroup[];
  }) => Promise<void>;
  onClose: () => void;
};

export default function MenuVisibilityModal(props: MenuVisibilityModalProps) {
  if (!props.open) return null;
  return <MenuVisibilityModalInner {...props} />;
}

function MenuVisibilityModalInner({
  open,
  hiddenMenus,
  menuOrder,
  menuGroups,
  saving,
  onSave,
  onClose,
}: MenuVisibilityModalProps) {
  const plugins = usePluginList();

  const configurableBuiltin = useMemo<ModalMenuItem[]>(
    () => pages.filter((p) => p.key !== '/'),
    [],
  );
  const pluginItems = useMemo<ModalMenuItem[]>(
    () =>
      (plugins.data ?? [])
        .filter((p) => p.menu)
        .sort(
          (a, b) => a.menu!.order - b.menu!.order || a.id.localeCompare(b.id),
        )
        .map((p) => ({
          key: `/p/${p.id}`,
          label: p.menu!.title,
          icon: pluginIcon(p.menu!.icon),
          isPlugin: true,
          running: p.runtimeStatus === 'running',
        })),
    [plugins.data],
  );
  const itemMap = useMemo(() => {
    const map = new Map<string, ModalMenuItem>();
    for (const item of configurableBuiltin) map.set(item.key, item);
    for (const item of pluginItems) map.set(item.key, item);
    return map;
  }, [configurableBuiltin, pluginItems]);

  const overviewItem = pages.find((p) => p.key === '/')!;

  const buildInitialNodes = (
    order: string[],
    groups: MenuGroup[],
  ): DraftNode[] => {
    const layout = buildMenuLayout<ModalMenuItem>({
      builtinItems: configurableBuiltin,
      pluginItems,
      menuOrder: order,
      menuGroups: groups,
      hideEmptyGroups: false,
    });
    return layout.nodes.map((node) =>
      node.type === 'group'
        ? {
            key: node.key,
            type: 'group',
            group: { ...node.group, items: [...node.group.items] },
          }
        : { key: node.key, type: 'item' },
    );
  };

  const [draftNodes, setDraftNodes] = useState<DraftNode[]>(() =>
    buildInitialNodes(menuOrder, menuGroups),
  );
  const [draftHidden, setDraftHidden] = useState<Set<string>>(
    () => new Set(hiddenMenus.filter((k) => !lockedMenuKeys.has(k))),
  );
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(
    () =>
      new Set(
        draftNodes
          .filter(
            (n): n is Extract<DraftNode, { type: 'group' }> =>
              n.type === 'group',
          )
          .map((n) => n.group.id),
      ),
  );
  const [dragging, setDragging] = useState<DragPayload | null>(null);
  const [dragOverKey, setDragOverKey] = useState<string | null>(null);

  const [groupEditor, setGroupEditor] = useState<{
    mode: 'create' | 'edit';
    groupId?: string;
    title: string;
    icon: string;
  } | null>(null);

  const allGroups = useMemo(
    () =>
      draftNodes
        .filter(
          (n): n is Extract<DraftNode, { type: 'group' }> => n.type === 'group',
        )
        .map((n) => n.group),
    [draftNodes],
  );

  function toggleHidden(key: string, visible: boolean) {
    if (lockedMenuKeys.has(key)) return;
    setDraftHidden((prev) => {
      const next = new Set(prev);
      if (visible) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  function moveTopNode(fromIdx: number, toIdx: number) {
    if (
      fromIdx === toIdx ||
      fromIdx < 0 ||
      toIdx < 0 ||
      toIdx >= draftNodes.length
    ) {
      return;
    }
    setDraftNodes((prev) => {
      const next = [...prev];
      const [moved] = next.splice(fromIdx, 1);
      next.splice(toIdx, 0, moved);
      return next;
    });
  }

  function moveChildInGroup(groupId: string, fromIdx: number, toIdx: number) {
    setDraftNodes((prev) =>
      prev.map((node) => {
        if (node.type !== 'group' || node.group.id !== groupId) return node;
        if (
          fromIdx === toIdx ||
          fromIdx < 0 ||
          toIdx < 0 ||
          toIdx >= node.group.items.length
        ) {
          return node;
        }
        const items = [...node.group.items];
        const [moved] = items.splice(fromIdx, 1);
        items.splice(toIdx, 0, moved);
        return { ...node, group: { ...node.group, items } };
      }),
    );
  }

  function moveItemToGroup(
    itemKey: string,
    targetGroupId: string,
    sourceGroupId?: string,
  ) {
    if (sourceGroupId === targetGroupId) return;
    setDraftNodes((prev) => {
      const withoutTop = sourceGroupId
        ? prev
        : prev.filter((n) => !(n.type === 'item' && n.key === itemKey));
      return withoutTop.map((node) => {
        if (node.type !== 'group') return node;
        if (node.group.id === sourceGroupId) {
          return {
            ...node,
            group: {
              ...node.group,
              items: node.group.items.filter((k) => k !== itemKey),
            },
          };
        }
        if (node.group.id === targetGroupId) {
          return {
            ...node,
            group: {
              ...node.group,
              items: node.group.items.includes(itemKey)
                ? node.group.items
                : [...node.group.items, itemKey],
            },
          };
        }
        return node;
      });
    });
    setExpandedGroups((prev) => new Set(prev).add(targetGroupId));
  }

  function promoteItemToTop(
    itemKey: string,
    sourceGroupId: string,
    insertAtIndex?: number,
  ) {
    setDraftNodes((prev) => {
      let groupIdx = -1;
      const cleaned = prev.map((node, idx) => {
        if (node.type === 'group' && node.group.id === sourceGroupId) {
          groupIdx = idx;
          return {
            ...node,
            group: {
              ...node.group,
              items: node.group.items.filter((k) => k !== itemKey),
            },
          };
        }
        return node;
      });
      const targetIdx =
        insertAtIndex !== undefined
          ? Math.max(0, Math.min(cleaned.length, insertAtIndex))
          : groupIdx >= 0
            ? groupIdx + 1
            : cleaned.length;
      const next = [...cleaned];
      next.splice(targetIdx, 0, { key: itemKey, type: 'item' });
      return next;
    });
  }

  function deleteGroup(groupId: string) {
    setDraftNodes((prev) => {
      const next: DraftNode[] = [];
      for (const node of prev) {
        if (node.type === 'group' && node.group.id === groupId) {
          for (const itemKey of node.group.items) {
            next.push({ key: itemKey, type: 'item' });
          }
        } else {
          next.push(node);
        }
      }
      return next;
    });
  }

  function saveGroupEditor() {
    if (!groupEditor) return;
    const title = groupEditor.title.trim();
    if (!title || title.length > 20) return;

    if (groupEditor.mode === 'create') {
      const id = `g_${Date.now().toString(36)}`;
      const newGroup: MenuGroup = {
        id,
        title,
        icon: groupEditor.icon,
        items: [],
      };
      setDraftNodes((prev) => [
        ...prev,
        { key: menuGroupKey(id), type: 'group', group: newGroup },
      ]);
      setExpandedGroups((prev) => new Set(prev).add(id));
    } else if (groupEditor.groupId) {
      setDraftNodes((prev) =>
        prev.map((node) =>
          node.type === 'group' && node.group.id === groupEditor.groupId
            ? {
                ...node,
                group: {
                  ...node.group,
                  title,
                  icon: groupEditor.icon,
                },
              }
            : node,
        ),
      );
    }
    setGroupEditor(null);
  }

  function handleResetDefault() {
    const initialNodes = buildInitialNodes([], []);
    setDraftNodes(initialNodes);
    setDraftHidden(new Set());
    setExpandedGroups(
      new Set(
        initialNodes
          .filter(
            (n): n is Extract<DraftNode, { type: 'group' }> =>
              n.type === 'group',
          )
          .map((n) => n.group.id),
      ),
    );
  }

  async function handleSubmit() {
    const nextGroups: MenuGroup[] = [];
    const nextOrder: string[] = [];
    for (const node of draftNodes) {
      if (node.type === 'group') {
        if (
          node.group.id === EXTENSIONS_GROUP_ID &&
          node.group.items.length === 0
        ) {
          continue;
        }
        nextGroups.push(node.group);
        nextOrder.push(node.key);
      } else {
        nextOrder.push(node.key);
      }
    }
    await onSave({
      hiddenMenus: [...draftHidden],
      menuOrder: nextOrder,
      menuGroups: nextGroups,
    });
  }

  function renderPinnedRow(item: SidebarPageItem, badgeText: string) {
    return (
      <Flex
        align="center"
        justify="space-between"
        gap={SPACING.sm}
        style={{
          paddingBlock: 10,
          paddingInline: 8,
          borderRadius: 8,
          background: 'var(--ant-color-fill-quaternary)',
          border: '1px solid var(--ant-color-border-secondary)',
        }}
      >
        <Flex align="center" gap={SPACING.sm} style={{ minWidth: 0 }}>
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              color: 'var(--ant-color-text-tertiary)',
            }}
            aria-hidden="true"
          >
            <Lock size={14} strokeWidth={1.85} />
          </span>
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              color: 'var(--ant-color-text-secondary)',
            }}
          >
            {item.icon}
          </span>
          <Typography.Text strong ellipsis>
            {item.label}
          </Typography.Text>
          <Tag style={{ marginInlineEnd: 0 }}>{badgeText}</Tag>
        </Flex>
        <Switch checked disabled aria-label={`${item.label}固定显示`} />
      </Flex>
    );
  }

  function renderMenuItemRow({
    itemKey,
    idx,
    total,
    sourceGroupId,
    onMoveUp,
    onMoveDown,
    onDropOnRow,
  }: {
    itemKey: string;
    idx: number;
    total: number;
    sourceGroupId?: string;
    onMoveUp: () => void;
    onMoveDown: () => void;
    onDropOnRow: () => void;
  }) {
    const item = itemMap.get(itemKey);
    if (!item) return null;
    const locked = lockedMenuKeys.has(item.key);
    const isOver = dragOverKey === item.key && dragging?.key !== item.key;
    const targetGroups = allGroups.filter((g) => g.id !== sourceGroupId);

    return (
      <Flex
        key={item.key}
        draggable={!saving}
        onDragStart={(e) => {
          e.stopPropagation();
          setDragging(
            sourceGroupId
              ? { kind: 'child', key: item.key, groupId: sourceGroupId }
              : { kind: 'top', key: item.key },
          );
          e.dataTransfer.effectAllowed = 'move';
          e.dataTransfer.setData('text/plain', item.key);
        }}
        onDragOver={(e) => {
          if (!dragging) return;
          e.preventDefault();
          e.stopPropagation();
          e.dataTransfer.dropEffect = 'move';
          if (dragOverKey !== item.key) setDragOverKey(item.key);
        }}
        onDrop={(e) => {
          e.preventDefault();
          e.stopPropagation();
          onDropOnRow();
          setDragging(null);
          setDragOverKey(null);
        }}
        onDragEnd={() => {
          setDragging(null);
          setDragOverKey(null);
        }}
        align="center"
        justify="space-between"
        gap={SPACING.xs}
        style={{
          paddingBlock: 8,
          paddingInline: 8,
          borderRadius: 8,
          background: isOver ? 'var(--ant-color-fill-tertiary)' : undefined,
          border: '1px solid var(--ant-color-border-secondary)',
          opacity: dragging?.key === item.key ? 0.5 : 1,
        }}
      >
        <Flex
          align="center"
          gap={SPACING.sm}
          style={{ minWidth: 0, flex: 1, color: 'var(--ant-color-text)' }}
        >
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              cursor: saving ? 'not-allowed' : 'grab',
              color: 'var(--ant-color-text-tertiary)',
            }}
            aria-hidden="true"
          >
            <GripVertical size={15} strokeWidth={1.85} />
          </span>
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              color: 'var(--ant-color-text-secondary)',
            }}
          >
            {item.icon}
          </span>
          <Typography.Text ellipsis>{item.label}</Typography.Text>
          {item.isPlugin && (
            <Tag color="blue" style={{ marginInlineEnd: 0 }}>
              扩展
            </Tag>
          )}
          {item.isPlugin && item.running === false && (
            <Tag style={{ marginInlineEnd: 0 }}>未运行</Tag>
          )}
        </Flex>
        <Space size={2}>
          {sourceGroupId && (
            <Tooltip title="移出分组">
              <Button
                type="text"
                size="small"
                disabled={saving}
                aria-label={`将${item.label}移出分组`}
                icon={<FolderOutput size={14} strokeWidth={1.85} />}
                onClick={() => promoteItemToTop(item.key, sourceGroupId)}
              />
            </Tooltip>
          )}
          {targetGroups.length > 0 && (
            <Dropdown
              menu={{
                items: targetGroups.map((g) => ({
                  key: g.id,
                  icon: renderMenuGroupIcon(g.icon),
                  label: g.title,
                  onClick: () => moveItemToGroup(item.key, g.id, sourceGroupId),
                })),
              }}
              trigger={['click']}
              disabled={saving}
            >
              <Tooltip title={sourceGroupId ? '移动到其他分组' : '移入分组'}>
                <Button
                  type="text"
                  size="small"
                  aria-label={`将${item.label}移入分组`}
                  icon={<FolderInput size={14} strokeWidth={1.85} />}
                />
              </Tooltip>
            </Dropdown>
          )}
          <Button
            type="text"
            size="small"
            disabled={saving || idx === 0}
            aria-label={`上移${item.label}`}
            icon={<ArrowUp size={14} strokeWidth={1.85} />}
            onClick={onMoveUp}
          />
          <Button
            type="text"
            size="small"
            disabled={saving || idx === total - 1}
            aria-label={`下移${item.label}`}
            icon={<ArrowDown size={14} strokeWidth={1.85} />}
            onClick={onMoveDown}
          />
          <span
            style={{
              minWidth: 48,
              display: 'inline-flex',
              justifyContent: 'flex-end',
            }}
          >
            {locked ? (
              <Tooltip title="核心菜单不可隐藏">
                <Switch checked disabled aria-label={`${item.label}不可隐藏`} />
              </Tooltip>
            ) : (
              <Switch
                checked={!draftHidden.has(item.key)}
                disabled={saving}
                aria-label={`切换${item.label}菜单显示`}
                onChange={(checked) => toggleHidden(item.key, checked)}
              />
            )}
          </span>
        </Space>
      </Flex>
    );
  }

  return (
    <>
      <Modal
        open={open}
        title="修改面板菜单"
        width={600}
        onCancel={onClose}
        footer={
          <Flex justify="flex-end" gap={SPACING.sm}>
            <Button onClick={onClose} disabled={saving}>
              取消
            </Button>
            <Button
              type="primary"
              loading={saving}
              onClick={() => void handleSubmit()}
            >
              保存
            </Button>
          </Flex>
        }
        styles={{
          body: {
            maxHeight: 'min(68dvh, 600px)',
            overflowY: 'auto',
            paddingTop: SPACING.xs,
          },
        }}
      >
        <Flex
          justify="space-between"
          align="center"
          wrap="wrap"
          gap={SPACING.sm}
          style={{ marginBottom: SPACING.sm }}
        >
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            拖拽或使用按钮调整顺序与分组；概览固定在顶部，退出固定在底部。
          </Typography.Text>
          <Space size={SPACING.xs}>
            <Button
              size="small"
              icon={<Plus size={14} strokeWidth={1.85} />}
              disabled={saving || allGroups.length >= 20}
              onClick={() =>
                setGroupEditor({
                  mode: 'create',
                  title: '',
                  icon: 'folder',
                })
              }
            >
              新建分组
            </Button>
            <Popconfirm
              title="恢复默认菜单布局？"
              description="将清空自定义分组，恢复默认顺序并显示全部菜单。"
              okText="恢复"
              cancelText="取消"
              onConfirm={handleResetDefault}
            >
              <Button
                size="small"
                icon={<RotateCcw size={13} strokeWidth={1.85} />}
                disabled={saving}
              >
                恢复默认
              </Button>
            </Popconfirm>
          </Space>
        </Flex>

        <Flex vertical gap={6}>
          {renderPinnedRow(overviewItem, '固定顶部')}

          {draftNodes.map((node, idx) => {
            if (node.type === 'item') {
              return renderMenuItemRow({
                itemKey: node.key,
                idx,
                total: draftNodes.length,
                onMoveUp: () => moveTopNode(idx, idx - 1),
                onMoveDown: () => moveTopNode(idx, idx + 1),
                onDropOnRow: () => {
                  if (!dragging) return;
                  if (dragging.kind === 'top') {
                    const fromIdx = draftNodes.findIndex(
                      (n) => n.key === dragging.key,
                    );
                    if (fromIdx >= 0) moveTopNode(fromIdx, idx);
                  } else {
                    promoteItemToTop(dragging.key, dragging.groupId, idx);
                  }
                },
              });
            }

            const { group } = node;
            const expanded = expandedGroups.has(group.id);
            const isOverGroup =
              dragOverKey === node.key && dragging?.key !== node.key;

            return (
              <div
                key={node.key}
                draggable={!saving}
                onDragStart={(e) => {
                  e.stopPropagation();
                  setDragging({ kind: 'top', key: node.key });
                  e.dataTransfer.effectAllowed = 'move';
                  e.dataTransfer.setData('text/plain', node.key);
                }}
                onDragOver={(e) => {
                  if (!dragging) return;
                  e.preventDefault();
                  e.stopPropagation();
                  e.dataTransfer.dropEffect = 'move';
                  if (dragOverKey !== node.key) setDragOverKey(node.key);
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  if (dragging) {
                    if (dragging.kind === 'top') {
                      if (dragging.key.startsWith('group:')) {
                        const fromIdx = draftNodes.findIndex(
                          (n) => n.key === dragging.key,
                        );
                        if (fromIdx >= 0) moveTopNode(fromIdx, idx);
                      } else {
                        moveItemToGroup(dragging.key, group.id);
                      }
                    } else if (dragging.groupId !== group.id) {
                      moveItemToGroup(dragging.key, group.id, dragging.groupId);
                    }
                  }
                  setDragging(null);
                  setDragOverKey(null);
                }}
                onDragEnd={() => {
                  setDragging(null);
                  setDragOverKey(null);
                }}
                style={{
                  borderRadius: 8,
                  border: isOverGroup
                    ? '1px dashed var(--ant-color-primary)'
                    : '1px solid var(--ant-color-border-secondary)',
                  background: isOverGroup
                    ? 'var(--ant-color-primary-bg)'
                    : 'var(--ant-color-fill-quaternary)',
                  opacity: dragging?.key === node.key ? 0.5 : 1,
                }}
              >
                <Flex
                  align="center"
                  justify="space-between"
                  gap={SPACING.xs}
                  style={{ paddingBlock: 8, paddingInline: 8 }}
                >
                  <Flex
                    align="center"
                    gap={SPACING.xs}
                    style={{ minWidth: 0, flex: 1 }}
                  >
                    <span
                      style={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        cursor: saving ? 'not-allowed' : 'grab',
                        color: 'var(--ant-color-text-tertiary)',
                      }}
                      aria-hidden="true"
                    >
                      <GripVertical size={15} strokeWidth={1.85} />
                    </span>
                    <Button
                      type="text"
                      size="small"
                      aria-label={expanded ? '收起分组' : '展开分组'}
                      icon={
                        expanded ? (
                          <ChevronDown size={14} strokeWidth={2} />
                        ) : (
                          <ChevronRight size={14} strokeWidth={2} />
                        )
                      }
                      onClick={() =>
                        setExpandedGroups((prev) => {
                          const next = new Set(prev);
                          if (next.has(group.id)) next.delete(group.id);
                          else next.add(group.id);
                          return next;
                        })
                      }
                    />
                    <span
                      style={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        color: 'var(--ant-color-text-secondary)',
                      }}
                    >
                      {renderMenuGroupIcon(group.icon)}
                    </span>
                    <Typography.Text strong ellipsis>
                      {group.title}
                    </Typography.Text>
                    <Tag style={{ marginInlineEnd: 0 }}>
                      {group.items.length} 项
                    </Tag>
                    {group.id === EXTENSIONS_GROUP_ID && (
                      <Tag color="geekblue" style={{ marginInlineEnd: 0 }}>
                        默认扩展分组
                      </Tag>
                    )}
                  </Flex>
                  <Space size={2}>
                    <Tooltip title="编辑分组">
                      <Button
                        type="text"
                        size="small"
                        disabled={saving}
                        aria-label={`编辑分组${group.title}`}
                        icon={<Pencil size={13} strokeWidth={1.85} />}
                        onClick={() =>
                          setGroupEditor({
                            mode: 'edit',
                            groupId: group.id,
                            title: group.title,
                            icon: group.icon,
                          })
                        }
                      />
                    </Tooltip>
                    <Button
                      type="text"
                      size="small"
                      disabled={saving || idx === 0}
                      aria-label={`上移分组${group.title}`}
                      icon={<ArrowUp size={14} strokeWidth={1.85} />}
                      onClick={() => moveTopNode(idx, idx - 1)}
                    />
                    <Button
                      type="text"
                      size="small"
                      disabled={saving || idx === draftNodes.length - 1}
                      aria-label={`下移分组${group.title}`}
                      icon={<ArrowDown size={14} strokeWidth={1.85} />}
                      onClick={() => moveTopNode(idx, idx + 1)}
                    />
                    {group.items.length > 0 ? (
                      <Popconfirm
                        title={`删除分组「${group.title}」？`}
                        description="组内菜单将自动移回一级列表，不会丢失。"
                        okText="删除"
                        cancelText="取消"
                        onConfirm={() => deleteGroup(group.id)}
                      >
                        <Button
                          type="text"
                          size="small"
                          danger
                          disabled={saving}
                          aria-label={`删除分组${group.title}`}
                          icon={<Trash2 size={14} strokeWidth={1.85} />}
                        />
                      </Popconfirm>
                    ) : (
                      <Button
                        type="text"
                        size="small"
                        danger
                        disabled={saving}
                        aria-label={`删除分组${group.title}`}
                        icon={<Trash2 size={14} strokeWidth={1.85} />}
                        onClick={() => deleteGroup(group.id)}
                      />
                    )}
                  </Space>
                </Flex>

                {expanded && (
                  <Flex
                    vertical
                    gap={6}
                    style={{
                      paddingLeft: 28,
                      paddingRight: 8,
                      paddingBottom: 8,
                    }}
                  >
                    {group.items.length === 0 ? (
                      <Typography.Text
                        type="secondary"
                        style={{
                          fontSize: 12,
                          paddingBlock: 6,
                          paddingInline: 8,
                        }}
                      >
                        暂无子菜单，可拖拽菜单到此处或点击菜单行右侧的「移入分组」。
                      </Typography.Text>
                    ) : (
                      group.items.map((itemKey, cIdx) =>
                        renderMenuItemRow({
                          itemKey,
                          idx: cIdx,
                          total: group.items.length,
                          sourceGroupId: group.id,
                          onMoveUp: () =>
                            moveChildInGroup(group.id, cIdx, cIdx - 1),
                          onMoveDown: () =>
                            moveChildInGroup(group.id, cIdx, cIdx + 1),
                          onDropOnRow: () => {
                            if (!dragging) return;
                            if (
                              dragging.kind === 'child' &&
                              dragging.groupId === group.id
                            ) {
                              const fromIdx = group.items.indexOf(dragging.key);
                              if (fromIdx >= 0) {
                                moveChildInGroup(group.id, fromIdx, cIdx);
                              }
                            } else if (
                              dragging.kind === 'top' &&
                              !dragging.key.startsWith('group:')
                            ) {
                              moveItemToGroup(dragging.key, group.id);
                            } else if (
                              dragging.kind === 'child' &&
                              dragging.groupId !== group.id
                            ) {
                              moveItemToGroup(
                                dragging.key,
                                group.id,
                                dragging.groupId,
                              );
                            }
                          },
                        }),
                      )
                    )}
                  </Flex>
                )}
              </div>
            );
          })}

          {renderPinnedRow(logoutMenuItem, '固定底部')}
        </Flex>
      </Modal>

      <Modal
        open={!!groupEditor}
        title={groupEditor?.mode === 'create' ? '新建菜单分组' : '编辑菜单分组'}
        width={420}
        onCancel={() => setGroupEditor(null)}
        onOk={saveGroupEditor}
        okText="确定"
        cancelText="取消"
        okButtonProps={{
          disabled:
            !groupEditor?.title.trim() || groupEditor.title.trim().length > 20,
        }}
      >
        {groupEditor && (
          <Flex vertical gap={SPACING.md} style={{ paddingTop: SPACING.xs }}>
            <div>
              <Typography.Text
                strong
                style={{ display: 'block', marginBottom: 6 }}
              >
                分组名称
              </Typography.Text>
              <Input
                value={groupEditor.title}
                maxLength={20}
                showCount
                placeholder="例如：运维管理、常用工具"
                onChange={(e) =>
                  setGroupEditor({ ...groupEditor, title: e.target.value })
                }
                onPressEnter={saveGroupEditor}
              />
            </div>
            <div>
              <Typography.Text
                strong
                style={{ display: 'block', marginBottom: 6 }}
              >
                分组图标
              </Typography.Text>
              <Flex wrap="wrap" gap={8}>
                {menuGroupIconOptions.map((opt) => {
                  const selected = groupEditor.icon === opt.value;
                  return (
                    <Tooltip key={opt.value} title={opt.label}>
                      <Button
                        type={selected ? 'primary' : 'default'}
                        icon={opt.icon}
                        aria-label={opt.label}
                        onClick={() =>
                          setGroupEditor({ ...groupEditor, icon: opt.value })
                        }
                      />
                    </Tooltip>
                  );
                })}
              </Flex>
            </div>
          </Flex>
        )}
      </Modal>
    </>
  );
}
