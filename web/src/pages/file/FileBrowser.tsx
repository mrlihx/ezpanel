import { useRef, useState, type CSSProperties } from 'react';
import {
  Alert,
  App,
  Badge,
  Breadcrumb,
  Button,
  Checkbox,
  Dropdown,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Typography,
  Progress,
  Tooltip,
  theme,
} from 'antd';
import {
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  ChevronDown,
  FileText,
  Folder,
  FolderOpen,
  HelpCircle,
  ListTodo,
  ListChecks,
  Pencil,
  Plus,
  RotateCw,
  TerminalSquare,
  Trash2,
  Upload,
} from 'lucide-react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import TransferConflictModal, {
  type PasteConflict,
} from './TransferConflictModal';
import {
  canPreviewFile,
  files,
  fileTask,
  previewTransfer,
  resumeUploadThreshold,
  uploadInChunks,
  downloadFile as download,
  type Batch,
  type FileEntry,
  type TrashConfig,
} from '../../api/file';
import { FONT_SIZE, LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';
import TaskDrawer from '../../components/TaskDrawer';
import TrashModal from './TrashModal';
import PermissionsModal from './PermissionsModal';
import DirectoryPicker from './DirectoryPicker';
import ExtractModal from './ExtractModal';
import ImportModal from './ImportModal';
import { droppedEntries, type UploadEntry } from './uploadEntries';
import BatchPermissionsModal from './BatchPermissionsModal';
import FileTable, { isArchive, type FileAction } from './FileList';
import { useNavigate } from 'react-router-dom';
import { usePluginList } from '../../api/plugin';
import FilePreview from './FilePreview';
import FileBatchToolbar from './FileBatchToolbar';
import './file.css';
import { isMac, modifierKey } from '../../utils/platform';
export type Clipboard = { type: 'copy' | 'move'; paths: string[] } | null;
type Operation = 'file' | 'dir' | 'rename' | 'copy' | 'move' | 'compress';
const labels: Record<Operation, string> = {
  file: '新建文件',
  dir: '新建文件夹',
  rename: '重命名',
  copy: '复制',
  move: '移动',
  compress: '压缩',
};
export default function FileBrowser({
  initialPath,
  active,
  blocked,
  clipboard,
  setClipboard,
  onPathChange,
  onNewTab,
  onEditFile,
  onTerminal,
}: {
  initialPath: string;
  active: boolean;
  blocked: boolean;
  clipboard: Clipboard;
  setClipboard: (value: Clipboard) => void;
  onPathChange: (path: string) => void;
  onNewTab: (path: string) => void;
  onEditFile: (path: string, line?: number) => void;
  onTerminal: (path: string) => void;
}) {
  const { modal, message } = App.useApp();
  const navigate = useNavigate();
  const plugins = usePluginList();
  const pluginActions = (plugins.data ?? []).flatMap((plugin) =>
    plugin.runtimeStatus === 'running' && plugin.fileAction
      ? [{ id: plugin.id, ...plugin.fileAction }]
      : [],
  );
  function openFilePlugin(id: string, rows: FileEntry[]) {
    if (rows.length)
      void navigate(`/p/${encodeURIComponent(id)}`, {
        state: { filePaths: rows.map((row) => row.path) },
      });
  }
  const { token } = theme.useToken();
  const [history, setHistory] = useState([initialPath]);
  const [cursor, setCursor] = useState(0);
  const path = history[cursor];
  const [editingPath, setEditingPath] = useState(false);
  const [pathInput, setPathInput] = useState(path);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(100);
  const [sort, setSort] = useState('name');
  const [order, setOrder] = useState('asc');
  const [selection, setSelection] = useState<React.Key[]>([]);
  const [mobileSelect, setMobileSelect] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [search, setSearch] = useState('');
  const [searchMode, setSearchMode] = useState<'name' | 'content' | 'global'>(
    'name',
  );
  const [trashOpen, setTrashOpen] = useState(false);
  const [task, setTask] = useState<string | null>(null);
  const [taskOpen, setTaskOpen] = useState(false);
  const [retryTask, setRetryTask] = useState<(() => void) | undefined>();
  const [operation, setOperation] = useState<{
    type: Operation;
    paths: string[];
    fromClipboard: boolean;
  } | null>(null);
  const [form] = Form.useForm<{
    value: string;
    overwrite: boolean;
    format: string;
    filename: string;
    password: string;
  }>();
  const compressFormat = Form.useWatch('format', form);
  const [working, setWorking] = useState(false);
  const [propertyTab, setPropertyTab] = useState<'general' | 'permissions'>(
    'general',
  );
  const [choosingDirectory, setChoosingDirectory] = useState(false);
  const [batchPermissions, setBatchPermissions] = useState<FileEntry[]>();
  const [extracting, setExtracting] = useState<string>();
  const [pasteConflict, setPasteConflict] = useState<PasteConflict>();
  const [permission, setPermission] = useState<FileEntry | null>(null);
  const [preview, setPreview] = useState<FileEntry>();
  const [dropped, setDropped] = useState<UploadEntry[]>([]);
  const readingUpload = useRef(false);
  const [importing, setImporting] = useState<'upload' | 'download' | 'git'>();
  const [uploading, setUploading] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [uploadProgress, setUploadProgress] = useState(0);
  const [uploadTarget, setUploadTarget] = useState('');
  const searchPath = searchMode === 'global' ? '/' : path;
  const list = useQuery({
    enabled: active,
    queryKey: search
      ? ['files', searchPath, searchMode, 'search', search]
      : ['files', path, page, pageSize, sort, order],
    queryFn: ({ signal }) =>
      search
        ? files<{ list: FileEntry[]; truncated: boolean }>(
            searchMode === 'name' ? '/search' : '/content-search',
            { params: { path: searchPath, keyword: search }, signal },
          ).then((r) => ({ ...r, total: r.list.length }))
        : files<{ list: FileEntry[]; total: number; truncated?: boolean }>(
            '/list',
            {
              params: { path, page, page_size: pageSize, sort, order },
              signal,
            },
          ),
  });
  const [previousPath, setPreviousPath] = useState(initialPath);
  if (previousPath !== initialPath) {
    setPreviousPath(initialPath);
    if (initialPath !== path) {
      const visited = history.lastIndexOf(initialPath);
      if (visited >= 0) setCursor(visited);
      else {
        setHistory([...history.slice(0, cursor + 1), initialPath]);
        setCursor(cursor + 1);
      }
      setPathInput(initialPath);
      setPage(1);
      setSelection([]);
      setMobileSelect(false);
      setSearch('');
      setKeyword('');
      setEditingPath(false);
    }
  }
  const trash = useQuery({
    enabled: active,
    queryKey: ['trash-config'],
    queryFn: () => files<TrashConfig>('/trash/config'),
  });
  const queryClient = useQueryClient();
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['trash'] });
    void list.refetch();
    void trash.refetch();
    setSelection([]);
  };
  function go(next: string) {
    if (next !== path) {
      setHistory([...history.slice(0, cursor + 1), next]);
      setCursor(cursor + 1);
    }
    onPathChange(next);
    setPage(1);
    setSelection([]);
    setMobileSelect(false);
    setSearch('');
    setKeyword('');
    setEditingPath(false);
  }
  function historyMove(index: number) {
    setCursor(index);
    onPathChange(history[index]);
    setPage(1);
    setSelection([]);
    setMobileSelect(false);
    setSearch('');
    setKeyword('');
    setEditingPath(false);
  }
  function taskStarted(id: string, retry?: () => void) {
    setRetryTask(() => retry);
    setTask(id);
    setTaskOpen(true);
  }
  function showOperation(
    type: Operation | 'extract',
    paths = selection.map(String),
    destination = path,
    fromClipboard = false,
  ) {
    if (type === 'extract') {
      setExtracting(paths[0]);
      return;
    }
    if (type === 'compress' && paths.length > 100) {
      void message.info('单次最多操作100项，请减少选择');
      return;
    }
    let filename = '';
    if (type === 'compress') {
      const alphabet =
        '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz';
      let suffix: string;
      do {
        suffix = Array.from(
          crypto.getRandomValues(new Uint8Array(6)),
          (n) => alphabet[n % alphabet.length],
        ).join('');
      } while (
        !/[0-9]/.test(suffix) ||
        !/[A-Z]/.test(suffix) ||
        !/[a-z]/.test(suffix)
      );
      filename = `${path.split('/').filter(Boolean).pop() || 'root'}_${suffix}.tar.gz`;
    }
    form.setFieldsValue({
      value:
        type === 'rename'
          ? paths[0].split('/').pop()
          : type === 'copy' || type === 'move'
            ? destination
            : type === 'compress'
              ? path
              : '',
      overwrite: false,
      format: 'tar.gz',
      filename,
      password: '',
    });
    setOperation({ type, paths, fromClipboard });
  }
  async function perform() {
    if (!operation) return;
    const values = await form.validateFields();
    setWorking(true);
    try {
      const { type, paths } = operation;
      if (type === 'file' || type === 'dir')
        await files('/create', {
          method: 'POST',
          data: { path: `${path === '/' ? '' : path}/${values.value}`, type },
        });
      else if (type === 'rename')
        await files('/rename', {
          method: 'POST',
          data: { path: paths[0], newName: values.value },
        });
      else {
        if ((type === 'copy' || type === 'move') && operation.fromClipboard) {
          const plan = { paths, dest: values.value };
          const preview = await previewTransfer(plan);
          if (preview.conflicts.length) {
            setPasteConflict({ type, ...plan, conflicts: preview.conflicts });
            setOperation(null);
            return;
          }
        }
        const data =
          type === 'compress'
            ? {
                paths,
                dest: `${values.value.replace(/\/$/, '')}/${values.filename}`,
                format: values.format,
                password:
                  values.format === 'tar.gz' ? '' : values.password || '',
              }
            : { paths, dest: values.value, overwrite: values.overwrite };
        const r = await fileTask(`/${type}`, data);
        taskStarted(r.taskId, () => {
          showOperation(type, paths, values.value);
          form.setFieldsValue(values);
        });
        if (type === 'move' && operation.fromClipboard) setClipboard(null);
      }
      setOperation(null);
      refresh();
    } finally {
      setWorking(false);
    }
  }
  function remove(paths: string[]) {
    if (paths.length > 100) {
      void message.info('单次最多操作100项，请减少选择');
      return;
    }
    if (!trash.data) {
      void message.info('尚未获取回收站状态，请刷新后再删除');
      return;
    }
    let permanent = !trash.data.enabled;
    modal.confirm({
      title: trash.data?.enabled
        ? `确定将选中的 ${paths.length} 个项目移入回收站吗？`
        : `彻底删除 ${paths.length} 项，不可恢复`,
      content: trash.data?.enabled ? (
        <Checkbox
          onChange={(e) => {
            permanent = e.target.checked;
          }}
        >
          彻底删除（不经过回收站）
        </Checkbox>
      ) : (
        '删除后无法恢复。'
      ),
      okButtonProps: { danger: true },
      onOk: async () => {
        const r = await fileTask('/delete', { paths, permanent });
        taskStarted(r.taskId, () => remove(paths));
      },
    });
  }
  function open(entry: FileEntry) {
    if (entry.protected) return;
    if (entry.isDir || entry.linkIsDir) go(entry.path);
    else if (isArchive(entry)) showOperation('extract', [entry.path]);
    else if (canPreviewFile(entry)) setPreview(entry);
    else onEditFile(entry.path, entry.match?.line);
  }
  async function upload(entries: UploadEntry[], overwrite: boolean) {
    const requestLimit = 2 * 1024 ** 3 - 8192;
    const groups: {
      entries: UploadEntry[];
      bytes: number;
      chunkFile?: File;
    }[] = [];
    for (const entry of entries) {
      const chunkFile =
        entry.file && entry.file.size >= resumeUploadThreshold
          ? entry.file
          : undefined;
      const bytes =
        chunkFile?.size ??
        (entry.file?.size ?? 0) + 1024 + entry.path.length * 4;
      const previous = groups.at(-1);
      if (
        chunkFile ||
        !previous ||
        previous.chunkFile ||
        previous.entries.length === 100 ||
        previous.bytes + bytes > requestLimit
      ) {
        groups.push({ entries: [entry], bytes, chunkFile });
      } else {
        previous.entries.push(entry);
        previous.bytes += bytes;
      }
    }
    setUploading(true);
    setUploadTarget(path);
    setUploadProgress(0);
    const result: Batch = { successCount: 0, failedCount: 0, results: [] };
    const total = groups.reduce((sum, group) => sum + group.bytes, 0);
    let processed = 0;
    try {
      for (const group of groups) {
        const progress = (bytes: number) =>
          setUploadProgress(
            Math.min(99, Math.floor((100 * (processed + bytes)) / total)),
          );
        try {
          if (group.chunkFile) {
            const target = group.entries[0].path;
            await uploadInChunks(
              path,
              target,
              group.chunkFile,
              overwrite,
              progress,
            );
            result.successCount++;
            result.results.push({ target, code: 0, msg: 'ok' });
            continue;
          }
          const data = new FormData();
          data.append('dir', path);
          data.append('overwrite', String(overwrite));
          for (const entry of group.entries) {
            if (entry.file) data.append('files[]', entry.file, entry.path);
            else data.append('dirs[]', entry.path);
          }
          const batch = await files<Batch>('/upload', {
            method: 'POST',
            data,
            silentError: true,
            onUploadProgress: ({ loaded, total }) => {
              if (total) progress((group.bytes * loaded) / total);
            },
          });
          result.successCount += batch.successCount;
          result.failedCount += batch.failedCount;
          result.results.push(...batch.results);
        } catch (error) {
          result.failedCount += group.entries.length;
          const msg = error instanceof Error ? error.message : '上传请求失败';
          result.results.push(
            ...group.entries.map((entry) => ({
              target: entry.path,
              code: 5000,
              msg,
            })),
          );
        } finally {
          processed += group.bytes;
        }
      }
      return result;
    } finally {
      setUploading(false);
      refresh();
    }
  }
  function submitPath() {
    const next = pathInput.trim();
    if (!next.startsWith('/')) {
      void message.info('请输入以 / 开头的绝对路径');
      return;
    }
    go(next.replace(/\/+/g, '/').replace(/\/$/, '') || '/');
  }
  function stageClipboard(type: 'copy' | 'move', paths: string[]) {
    if (paths.length > 100) {
      void message.info('单次最多操作100项，请减少选择');
      return;
    }
    setClipboard({ type, paths });
    void message.success(
      `已${type === 'copy' ? '复制' : '剪切'} ${paths.length} 项，请进入目标目录后粘贴`,
    );
  }
  function paste() {
    if (clipboard) showOperation(clipboard.type, clipboard.paths, path, true);
  }
  function handleAction(action: FileAction, rows: FileEntry[]) {
    const targets = rows.map((row) => row.path);
    const entry = rows[0];
    if (!entry) return;
    if (action === 'open') open(entry);
    else if (action === 'open-tab') onNewTab(entry.path);
    else if (action === 'download') void download(entry.path);
    else if (action === 'copy' || action === 'move')
      stageClipboard(action, targets);
    else if (action === 'permissions' && rows.length > 1)
      setBatchPermissions(rows);
    else if (action === 'permissions' || action === 'properties') {
      setPropertyTab(action === 'permissions' ? 'permissions' : 'general');
      setPermission(entry);
    } else if (action === 'delete') remove(targets);
    else if (action === 'locate')
      go(entry.path.slice(0, entry.path.lastIndexOf('/')) || '/');
    else showOperation(action, targets);
  }
  // 搜索接口返回完整的限额结果，搜索排序仅处理这份结果，不把单页当完整目录。
  const entries = search
    ? [...(list.data?.list ?? [])].sort((a, b) => {
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        const difference =
          sort === 'size'
            ? (a.size ?? 0) - (b.size ?? 0)
            : sort === 'mtime'
              ? Date.parse(a.modTime) - Date.parse(b.modTime)
              : 0;
        const value = difference || a.name.localeCompare(b.name);
        return order === 'desc' ? -value : value;
      })
    : (list.data?.list ?? []);
  const segments = path.split('/').filter(Boolean);
  const selectable = entries.filter((entry) => !entry.protected);
  const selectedEntries = selectable.filter((entry) =>
    selection.includes(entry.path),
  );
  const allSelected =
    selectable.length > 0 && selectedEntries.length === selectable.length;
  function clearSelection() {
    setSelection([]);
    setMobileSelect(false);
  }
  const workspaceStyle = {
    '--file-table-height': LAYOUT.fileTableHeight,
    '--file-bg': token.colorBgContainer,
    '--file-raised': token.colorFillAlter,
    '--file-border': token.colorBorderSecondary,
    '--file-muted': token.colorTextSecondary,
    '--file-ink': token.colorText,
    '--file-accent': token.colorPrimary,
    '--file-selected': token.colorPrimaryBg,
    '--file-folder': token.colorPrimary,
    '--file-radius': `${token.borderRadiusLG}px`,
    '--file-radius-sm': `${token.borderRadius}px`,
    '--file-space-xs': `${SPACING.xs}px`,
    '--file-space-sm': `${SPACING.sm}px`,
    '--file-space-md': `${SPACING.md}px`,
    '--file-space-lg': `${SPACING.lg}px`,
    '--file-space-xl': `${SPACING.xl}px`,
    '--file-search-width': `${LAYOUT.fileSearchWidth}px`,
    '--file-path-min': `${LAYOUT.filePathMinWidth}px`,
    '--file-small-font': `${FONT_SIZE.auxiliary}px`,
    '--file-icon-size': `${FONT_SIZE.title}px`,
    '--file-mono': MONO_FONT,
    '--file-control-height': `${token.controlHeight}px`,
    '--file-toolbar-primary-width': `${LAYOUT.fileToolbarPrimaryWidth}px`,
  } as CSSProperties;
  return (
    <>
      <section
        className="file-workspace"
        aria-label="文件管理"
        tabIndex={0}
        style={workspaceStyle}
        onMouseDown={(event) => {
          if (
            !(event.target as HTMLElement).closest(
              'button,a,input,label,[role="menu"],[role="dialog"]',
            )
          )
            event.currentTarget.focus({ preventScroll: true });
        }}
        onKeyDown={(event) => {
          if (
            operation ||
            blocked ||
            permission ||
            batchPermissions ||
            extracting ||
            pasteConflict ||
            importing ||
            preview ||
            trashOpen ||
            taskOpen ||
            (event.target as HTMLElement).closest(
              'input,textarea,select,[contenteditable="true"],[role="menu"],[role="dialog"]',
            )
          )
            return;
          const modifier = event.ctrlKey || event.metaKey;
          const key = event.key.toLowerCase();
          if (modifier && key === 'a') {
            event.preventDefault();
            setSelection(
              entries.filter((row) => !row.protected).map((row) => row.path),
            );
          } else if (
            modifier &&
            (key === 'c' || key === 'x') &&
            selection.length
          ) {
            event.preventDefault();
            stageClipboard(
              key === 'c' ? 'copy' : 'move',
              selection.map(String),
            );
          } else if (modifier && key === 'v' && clipboard) {
            event.preventDefault();
            paste();
          } else if (event.key === 'Delete' && selection.length) {
            event.preventDefault();
            remove(selection.map(String));
          } else if (event.key === 'F2' && selection.length === 1) {
            event.preventDefault();
            showOperation('rename');
          } else if (event.key === 'Escape') {
            clearSelection();
            setClipboard(null);
          }
        }}
        onDragOver={(event) => {
          if (!event.dataTransfer.types.includes('Files')) return;
          event.preventDefault();
          event.dataTransfer.dropEffect = uploading ? 'none' : 'copy';
          setDragging(!uploading);
        }}
        onDragLeave={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null))
            setDragging(false);
        }}
        onDrop={(event) => {
          event.preventDefault();
          setDragging(false);
          if (uploading || importing || readingUpload.current) return;
          readingUpload.current = true;
          void droppedEntries(event.dataTransfer)
            .then(
              (entries) => {
                if (entries.length) {
                  setDropped(entries);
                  setImporting('upload');
                }
              },
              () => void message.error('有项目无法读取，请重新选择后再上传。'),
            )
            .finally(() => {
              readingUpload.current = false;
            });
        }}
      >
        <div className="file-location-bar">
          <Space.Compact>
            <Tooltip title="后退">
              <Button
                aria-label="后退"
                icon={<ArrowLeft size={16} strokeWidth={1.9} />}
                disabled={cursor === 0}
                onClick={() => historyMove(cursor - 1)}
              />
            </Tooltip>
            <Tooltip title="前进">
              <Button
                aria-label="前进"
                icon={<ArrowRight size={16} strokeWidth={1.9} />}
                disabled={cursor === history.length - 1}
                onClick={() => historyMove(cursor + 1)}
              />
            </Tooltip>
            <Tooltip title="返回上级目录">
              <Button
                aria-label="上级目录"
                icon={<ArrowUp size={16} strokeWidth={1.9} />}
                disabled={path === '/'}
                onClick={() => go(path.slice(0, path.lastIndexOf('/')) || '/')}
              />
            </Tooltip>
          </Space.Compact>
          <div className="file-address">
            {editingPath ? (
              <Input
                autoFocus
                aria-label="目录绝对路径"
                value={pathInput}
                onChange={(event) => setPathInput(event.target.value)}
                onFocus={(event) => event.target.select()}
                onPressEnter={submitPath}
                onKeyDown={(event) => {
                  if (event.key === 'Escape') setEditingPath(false);
                }}
                onBlur={() => setEditingPath(false)}
                suffix={
                  <Typography.Text type="secondary">Enter 跳转</Typography.Text>
                }
              />
            ) : (
              <div
                className="file-breadcrumb"
                onClick={() => {
                  setPathInput(path);
                  setEditingPath(true);
                }}
                title="点击空白处输入路径"
              >
                <Breadcrumb
                  items={[
                    {
                      title: (
                        <button
                          type="button"
                          onClick={(event) => {
                            event.stopPropagation();
                            go('/');
                          }}
                        >
                          根目录
                        </button>
                      ),
                    },
                    ...segments.map((name, index) => ({
                      title: (
                        <button
                          type="button"
                          onClick={(event) => {
                            event.stopPropagation();
                            go('/' + segments.slice(0, index + 1).join('/'));
                          }}
                        >
                          {name}
                        </button>
                      ),
                    })),
                  ]}
                />
                <Button
                  type="text"
                  size="small"
                  aria-label="输入目录路径"
                  icon={<Pencil size={14} strokeWidth={1.9} />}
                  onClick={(event) => {
                    event.stopPropagation();
                    setPathInput(path);
                    setEditingPath(true);
                  }}
                />
              </div>
            )}
          </div>
          <Tooltip title="刷新目录">
            <Button
              aria-label="刷新目录"
              icon={<RotateCw size={16} strokeWidth={1.9} />}
              loading={list.isFetching}
              onClick={refresh}
            />
          </Tooltip>
          <Space.Compact className="file-search">
            <Select
              aria-label="搜索范围"
              value={searchMode}
              options={[
                { value: 'name', label: '文件名' },
                { value: 'content', label: '目录内容' },
                { value: 'global', label: '全局内容' },
              ]}
              onChange={(value) => {
                setSearchMode(value);
                if (search) setSearch(keyword.trim());
                setPage(1);
                setSelection([]);
              }}
            />
            <Input.Search
              aria-label={
                searchMode === 'name' ? '递归搜索文件名' : '搜索文件内容'
              }
              placeholder={
                searchMode === 'name'
                  ? '搜索文件 / 目录'
                  : '搜索内容（区分大小写）'
              }
              value={keyword}
              onChange={(event) => {
                setKeyword(event.target.value);
                if (!event.target.value) {
                  setSearch('');
                  setSelection([]);
                }
              }}
              onSearch={(value) => {
                setSearch(value.trim());
                setPage(1);
                setSelection([]);
              }}
              allowClear
            />
          </Space.Compact>
        </div>
        <div className="file-toolbar">
          <Space className="file-toolbar-main" size="small">
            <Dropdown
              trigger={['click']}
              disabled={uploading}
              menu={{
                items: [
                  { key: 'upload', label: '上传文件/文件夹' },
                  { type: 'divider' },
                  { key: 'download', label: 'URL链接下载' },
                  { key: 'git', label: 'git仓库克隆' },
                ],
                onClick: ({ key }) =>
                  setImporting(key as 'upload' | 'download' | 'git'),
              }}
            >
              <Button
                type="primary"
                icon={<Upload size={15} strokeWidth={2} />}
                loading={uploading}
              >
                上传/下载 <ChevronDown size={14} strokeWidth={2} />
              </Button>
            </Dropdown>
            <Dropdown
              trigger={['click']}
              menu={{
                items: [
                  {
                    key: 'file',
                    icon: <FileText size={15} strokeWidth={1.85} />,
                    label: '新建文件',
                  },
                  {
                    key: 'dir',
                    icon: <Folder size={15} strokeWidth={1.85} />,
                    label: '新建文件夹',
                  },
                ],
                onClick: ({ key }) => showOperation(key as Operation, []),
              }}
            >
              <Button icon={<Plus size={15} strokeWidth={2} />}>
                新建 <ChevronDown size={14} strokeWidth={2} />
              </Button>
            </Dropdown>
            <Dropdown
              trigger={['click']}
              menu={{
                items: [
                  { key: '/', label: '根目录　/' },
                  { key: '/www', label: '网站目录　/www' },
                  { key: '/etc', label: '配置目录　/etc' },
                  { key: '/root', label: '主目录　/root' },
                  { key: '/tmp', label: '临时目录　/tmp' },
                ],
                onClick: ({ key }) => go(key),
              }}
            >
              <Button icon={<FolderOpen size={15} strokeWidth={1.9} />}>
                常用目录 <ChevronDown size={14} strokeWidth={2} />
              </Button>
            </Dropdown>
            <Button
              icon={<TerminalSquare size={15} strokeWidth={1.9} />}
              onClick={() => onTerminal(path)}
            >
              终端
            </Button>
          </Space>
          <div className="file-toolbar-right">
            <Button
              className="file-select-toggle"
              icon={<ListChecks size={16} />}
              aria-pressed={mobileSelect}
              disabled={!selectable.length && !mobileSelect}
              onClick={() =>
                mobileSelect ? clearSelection() : setMobileSelect(true)
              }
            >
              {mobileSelect ? '退出多选' : '多选'}
            </Button>
            <div className="file-toolbar-context">
              <Space size="small">
                <Tooltip
                  title={`选中文件后使用 ${modifierKey} + C 复制、X 剪切、V 粘贴；${isMac ? 'Fn + Delete' : 'Delete'} 删除、F2 重命名，Esc 清除选择和剪贴板`}
                >
                  <Button
                    type="text"
                    aria-label="文件快捷键说明"
                    icon={<HelpCircle size={16} strokeWidth={1.85} />}
                  />
                </Tooltip>
                {task && (
                  <Button
                    type="text"
                    icon={<ListTodo size={15} strokeWidth={1.9} />}
                    onClick={() => setTaskOpen(true)}
                  >
                    操作任务
                  </Button>
                )}
              </Space>
              {(selection.length > 0 || mobileSelect || clipboard) && (
                <FileBatchToolbar
                  count={selectedEntries.length}
                  archiveAction={
                    selectedEntries.length === 1 &&
                    isArchive(selectedEntries[0])
                      ? 'extract'
                      : 'compress'
                  }
                  mobileSelect={mobileSelect}
                  allSelected={allSelected}
                  canSelect={selectable.length > 0}
                  clipboard={clipboard}
                  onSelectAll={() => {
                    setMobileSelect(true);
                    setSelection(
                      allSelected ? [] : selectable.map((entry) => entry.path),
                    );
                  }}
                  onClear={clearSelection}
                  onAction={(action) => handleAction(action, selectedEntries)}
                  pluginActions={pluginActions.filter(
                    (action) => selectedEntries.length === 1 || action.multiple,
                  )}
                  onPlugin={(id) => openFilePlugin(id, selectedEntries)}
                  onPaste={paste}
                  onClearClipboard={() => setClipboard(null)}
                />
              )}
            </div>
            <Badge
              count={trash.data?.count ?? 0}
              size="small"
              overflowCount={999}
            >
              <Button
                icon={<Trash2 size={15} strokeWidth={1.9} />}
                onClick={() => setTrashOpen(true)}
              >
                回收站
              </Button>
            </Badge>
          </div>
        </div>
        {uploading && (
          <div className="file-upload-progress">
            <Typography.Text>正在上传到 {uploadTarget}</Typography.Text>
            <Progress percent={uploadProgress} size="small" />
          </div>
        )}
        {list.error && (
          <Alert
            type="error"
            showIcon
            message={list.error.message}
            action={<Button onClick={() => void list.refetch()}>重试</Button>}
          />
        )}
        {list.data?.truncated && (
          <Alert
            type="warning"
            message={
              searchMode === 'name'
                ? '搜索达到500条或10秒限制，结果已截断'
                : '搜索达到扫描范围、500条结果或10秒上限，请缩小目录范围'
            }
            showIcon
          />
        )}
        {search && (
          <div className="file-search-summary">
            <Space wrap>
              <Typography.Text>
                “{search}” 的搜索结果 · {list.data?.total ?? 0} 项
              </Typography.Text>
              <Typography.Text type="secondary">
                {searchMode === 'name'
                  ? '包含子目录'
                  : `${searchMode === 'global' ? '从根目录搜索' : '包含子目录'} · UTF-8 文本，单文件不超过 2 MiB`}
              </Typography.Text>
              <Button
                type="link"
                size="small"
                onClick={() => {
                  setSearch('');
                  setKeyword('');
                  setSelection([]);
                }}
              >
                返回当前目录
              </Button>
            </Space>
          </div>
        )}
        <FileTable
          entries={entries}
          loading={list.isFetching}
          search={Boolean(search)}
          total={list.data?.total ?? 0}
          page={page}
          pageSize={pageSize}
          sort={sort}
          order={order}
          selection={selection}
          mobileSelect={mobileSelect}
          cutPaths={clipboard?.type === 'move' ? clipboard.paths : []}
          onSelect={setSelection}
          onPage={(next, size) => {
            setPage(next);
            setPageSize(size);
            setSelection([]);
          }}
          onSort={(field, nextOrder) => {
            setSort(field);
            setOrder(nextOrder);
            setPage(1);
            setSelection([]);
          }}
          onAction={handleAction}
          pluginActions={pluginActions}
          onPlugin={openFilePlugin}
          onUpload={() => setImporting('upload')}
          onTerminal={() => onTerminal(path)}
          onPaste={paste}
          canPaste={Boolean(clipboard)}
          onCreate={(type) => showOperation(type, [])}
          onRefresh={refresh}
          onRemarkSaved={() => void list.refetch()}
        />
        <div className="file-status-bar">
          <Typography.Text type="secondary">
            <span className="file-path-label">
              {search ? '搜索结果' : '当前目录'}
            </span>
            ：{entries.filter((entry) => entry.isDir).length} 个目录，
            {entries.filter((entry) => !entry.isDir).length} 个文件
          </Typography.Text>
          <Typography.Text type="secondary">
            支持拖拽上传 · 右键查看更多操作
          </Typography.Text>
        </div>
        {dragging && (
          <div className="file-drop-overlay">
            <Upload size={28} strokeWidth={1.85} />
            <Typography.Text>松开文件，上传到当前目录</Typography.Text>
            <Typography.Text type="secondary">{path}</Typography.Text>
          </div>
        )}
      </section>
      {preview && (
        <FilePreview
          key={preview.path}
          entry={preview}
          onClose={() => setPreview(undefined)}
        />
      )}
      <Modal
        title={
          operation
            ? operation.fromClipboard
              ? `粘贴 · ${operation.type === 'copy' ? '复制' : '移动'} ${operation.paths.length} 项`
              : labels[operation.type]
            : ''
        }
        open={Boolean(operation)}
        onCancel={() => setOperation(null)}
        onOk={perform}
        confirmLoading={working}
        destroyOnClose
      >
        {operation &&
          operation.type !== 'compress' &&
          operation.paths.length > 0 && (
            <Typography.Paragraph type="secondary">
              {operation.paths.length === 1
                ? operation.paths[0]
                : `已选 ${operation.paths.length} 项`}
            </Typography.Paragraph>
          )}
        <Form
          form={form}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
        >
          {operation?.type === 'compress' && (
            <Form.Item name="format" label="压缩类型">
              <Select
                onChange={(format: string) => {
                  const filename = form.getFieldValue('filename');
                  if (filename)
                    form.setFieldValue(
                      'filename',
                      filename.replace(/\.(tar\.gz|zip|7z)$/i, '') +
                        '.' +
                        format,
                    );
                  if (format === 'tar.gz') form.setFieldValue('password', '');
                }}
                options={[
                  { label: 'tar.gz', value: 'tar.gz' },
                  { label: 'zip', value: 'zip' },
                  { label: '7z', value: '7z' },
                ]}
              />
            </Form.Item>
          )}
          <Form.Item
            name="value"
            label={
              operation && ['file', 'dir', 'rename'].includes(operation.type)
                ? '名称'
                : '目标绝对路径'
            }
            rules={[
              { required: true, message: '请填写此项' },
              {
                validator: (_, value: string) =>
                  operation &&
                  ['file', 'dir', 'rename'].includes(operation.type) &&
                  (!value ||
                    ['.', '..'].includes(value) ||
                    value.includes('/') ||
                    value.includes('\\') ||
                    value.includes(String.fromCharCode(0)))
                    ? Promise.reject(new Error('请输入单个名称'))
                    : value &&
                        operation &&
                        !['file', 'dir', 'rename'].includes(operation.type) &&
                        !value.startsWith('/')
                      ? Promise.reject(new Error('请输入绝对路径'))
                      : Promise.resolve(),
              },
            ]}
          >
            <Input
              autoFocus
              addonAfter={
                operation?.type === 'compress' ? (
                  <Button
                    type="text"
                    size="small"
                    icon={<FolderOpen size={15} strokeWidth={1.85} />}
                    aria-label="选择压缩保存目录"
                    onClick={() => setChoosingDirectory(true)}
                  />
                ) : undefined
              }
            />
          </Form.Item>
          {operation?.type === 'compress' && (
            <>
              <Form.Item
                name="filename"
                label="保存文件名"
                rules={[
                  { required: true, message: '请输入保存文件名' },
                  {
                    validator: (_, value: string) =>
                      value &&
                      !['.', '..'].includes(value) &&
                      !value.includes('/') &&
                      !value.includes('\\') &&
                      !value.includes(String.fromCharCode(0)) &&
                      value
                        .toLowerCase()
                        .endsWith('.' + form.getFieldValue('format'))
                        ? Promise.resolve()
                        : Promise.reject(
                            new Error('请输入与压缩类型匹配的单个文件名'),
                          ),
                  },
                ]}
              >
                <Input />
              </Form.Item>
              <Form.Item
                name="password"
                label="压缩密码"
                extra={
                  compressFormat === 'tar.gz'
                    ? 'tar.gz 不支持压缩密码，如需加密请选择 zip 或 7z。'
                    : '可选，留空则不加密。'
                }
              >
                <Input.Password
                  autoComplete="new-password"
                  disabled={compressFormat === 'tar.gz'}
                  placeholder={
                    compressFormat === 'tar.gz'
                      ? 'tar.gz 不支持压缩密码'
                      : '无密码则留空'
                  }
                />
              </Form.Item>
            </>
          )}
          {operation &&
            !operation.fromClipboard &&
            ['copy', 'move'].includes(operation.type) && (
              <Form.Item name="overwrite" valuePropName="checked">
                <Checkbox>覆盖同名普通文件（目录不合并）</Checkbox>
              </Form.Item>
            )}
        </Form>
      </Modal>
      {pasteConflict && (
        <TransferConflictModal
          plan={pasteConflict}
          onClose={() => setPasteConflict(undefined)}
          onStarted={(id) => {
            taskStarted(id, () =>
              showOperation(
                pasteConflict.type,
                pasteConflict.paths,
                pasteConflict.dest,
                true,
              ),
            );
            if (pasteConflict.type === 'move') setClipboard(null);
            refresh();
          }}
        />
      )}
      {choosingDirectory && (
        <DirectoryPicker
          initialPath={String(form.getFieldValue('value') || path)}
          onClose={() => setChoosingDirectory(false)}
          onSelect={(directory) => {
            form.setFieldValue('value', directory);
            setChoosingDirectory(false);
          }}
        />
      )}
      {extracting && (
        <ExtractModal
          path={extracting}
          destination={path}
          onClose={() => setExtracting(undefined)}
          onStarted={(id) => taskStarted(id)}
        />
      )}
      {batchPermissions && (
        <BatchPermissionsModal
          entries={batchPermissions}
          onClose={() => setBatchPermissions(undefined)}
          refresh={refresh}
        />
      )}
      {permission && (
        <PermissionsModal
          entry={permission}
          initialTab={propertyTab}
          onClose={() => setPermission(null)}
          refresh={refresh}
        />
      )}
      {importing && (
        <ImportModal
          type={importing}
          path={path}
          onClose={() => {
            setImporting(undefined);
            setDropped([]);
          }}
          initialEntries={dropped}
          onUpload={upload}
          uploadProgress={uploadProgress}
          onStarted={taskStarted}
        />
      )}
      <TrashModal
        open={trashOpen}
        onClose={() => setTrashOpen(false)}
        config={trash.data}
        refresh={refresh}
        onTask={taskStarted}
      />
      <TaskDrawer
        taskId={task}
        title="文件操作"
        open={taskOpen}
        onClose={() => setTaskOpen(false)}
        onSettled={refresh}
        onRetry={retryTask}
      />
    </>
  );
}
