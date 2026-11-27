import { useState } from 'react';
import {
  Button,
  Dropdown,
  Empty,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  type MenuProps,
  Grid,
} from 'antd';
import { canPreviewFile, type FileEntry } from '../../api/file';
import { formatSize, formatTime } from '../../utils/format';
import { LAYOUT } from '../../styles/tokens';
import FileIcon from './FileIcon';
import { DirectorySize, FileRemark } from './MetadataCells';

export type FileAction =
  | 'open'
  | 'open-tab'
  | 'download'
  | 'copy'
  | 'move'
  | 'rename'
  | 'permissions'
  | 'properties'
  | 'compress'
  | 'extract'
  | 'delete'
  | 'locate';
export const isArchive = (entry: FileEntry) =>
  !(entry.isDir || entry.linkIsDir) &&
  /\.(zip|tar\.gz|tgz|tar\.bz2|tbz2|tar\.xz|txz|tar\.zst|tzst|tar|7z|rar|gz|bz2|xz|zst)$/i.test(
    entry.name,
  );

export type FilePluginAction = { id: string; title: string; multiple: boolean };

type Props = {
  entries: FileEntry[];
  loading: boolean;
  search: boolean;
  total: number;
  page: number;
  pageSize: number;
  sort: string;
  order: string;
  selection: React.Key[];
  mobileSelect: boolean;
  cutPaths: string[];
  onSelect: (keys: React.Key[]) => void;
  onPage: (page: number, size: number) => void;
  onSort: (field: string, order: string) => void;
  onAction: (action: FileAction, entries: FileEntry[]) => void;
  pluginActions: FilePluginAction[];
  onPlugin: (id: string, entries: FileEntry[]) => void;
  onPaste: () => void;
  onUpload: () => void;
  onTerminal: () => void;
  canPaste: boolean;
  onCreate: (type: 'file' | 'dir') => void;
  onRefresh: () => void;
  onRemarkSaved: () => void;
};

export default function FileList(props: Props) {
  const desktop = Grid.useBreakpoint().lg;
  const selecting = desktop === false && props.mobileSelect;
  const { entries, selection, onSelect, onAction } = props;
  const [contextRow, setContextRow] = useState<FileEntry | null>(null);
  const [morePath, setMorePath] = useState<string | null>(null);
  function menu(rows: FileEntry[]): MenuProps {
    const single = rows.length === 1;
    const entry = rows[0];
    const items: MenuProps['items'] = single
      ? [
          {
            key: 'open',
            label:
              entry.isDir || entry.linkIsDir || isArchive(entry)
                ? '打开'
                : canPreviewFile(entry)
                  ? '预览'
                  : '编辑',
          },
          ...(props.search ? [{ key: 'locate', label: '打开所在目录' }] : []),
          { type: 'divider' },
          { key: 'download', label: '下载' },
          { key: 'copy', label: '复制' },
          { key: 'move', label: '剪切' },
          { key: 'rename', label: '重命名' },
          { key: 'delete', label: '删除', danger: true },
          { type: 'divider' },
          { key: 'compress', label: '创建压缩' },
          ...(isArchive(entry) ? [{ key: 'extract', label: '解压' }] : []),
          { type: 'divider' },
          { key: 'properties', label: '属性' },
        ]
      : [
          { key: 'copy', label: '复制' },
          { key: 'move', label: '剪切' },
          { key: 'delete', label: '删除', danger: true },
          { type: 'divider' },
          { key: 'compress', label: '创建压缩' },
          { key: 'permissions', label: '权限' },
        ];
    const pluginActions = props.pluginActions.filter(
      (action) => single || action.multiple,
    );
    if (pluginActions.length) {
      items.push(
        { type: 'divider' },
        ...pluginActions.map((action) => ({
          key: `plugin:${action.id}`,
          label: action.title,
        })),
      );
    }
    return {
      items,
      onClick: ({ key }) => {
        if (key.startsWith('plugin:')) props.onPlugin(key.slice(7), rows);
        else onAction(key as FileAction, rows);
      },
    };
  }
  const contextEntries = contextRow
    ? selection.includes(contextRow.path)
      ? [
          contextRow,
          ...entries.filter(
            (entry) =>
              entry.path !== contextRow.path && selection.includes(entry.path),
          ),
        ]
      : [contextRow]
    : [];
  const contextMenu: MenuProps = contextRow
    ? contextRow.protected
      ? {
          items: [
            { key: 'protected', label: '面板目录受保护', disabled: true },
          ],
        }
      : menu(contextEntries)
    : {
        items: [
          { key: 'refresh', label: '刷新' },
          { type: 'divider' },
          { key: 'file', label: '新建文件' },
          { key: 'dir', label: '新建文件夹' },
          { key: 'upload', label: '上传' },
          { key: 'terminal', label: '终端' },
          ...(props.canPaste
            ? [{ type: 'divider' as const }, { key: 'paste', label: '粘贴' }]
            : []),
        ],
        onClick: ({ key }) => {
          if (key === 'paste') props.onPaste();
          else if (key === 'upload') props.onUpload();
          else if (key === 'terminal') props.onTerminal();
          else if (key === 'refresh') props.onRefresh();
          else props.onCreate(key as 'file' | 'dir');
        },
      };
  return (
    <Dropdown
      menu={contextMenu}
      trigger={['contextMenu']}
      onOpenChange={(open) => {
        if (!open) setContextRow(null);
      }}
    >
      <div
        className="file-list"
        onContextMenu={(event) => {
          const rowPath = (event.target as HTMLElement)
            .closest('tr[data-row-key]')
            ?.getAttribute('data-row-key');
          const row = entries.find((entry) => entry.path === rowPath) ?? null;
          setContextRow(row);
          if (row && !row.protected && !selection.includes(row.path))
            onSelect([row.path]);
        }}
      >
        <Table<FileEntry>
          size="small"
          rowKey="path"
          loading={props.loading}
          dataSource={entries}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={
                  props.search
                    ? '没有找到匹配的文件'
                    : '此目录为空，可上传或新建文件'
                }
              />
            ),
          }}
          scroll={{
            x: true,
            y: LAYOUT.fileTableHeight,
          }}
          rowClassName={(row) =>
            [
              props.cutPaths.includes(row.path) ? 'file-row-cut' : '',
              morePath === row.path ? 'file-row-menu-open' : '',
            ].join(' ')
          }
          rowSelection={{
            selectedRowKeys: selection,
            onChange: onSelect,
            getCheckboxProps: (row) => ({ disabled: row.protected }),
          }}
          onRow={(row) => ({
            onClick: (event) => {
              if (
                row.protected ||
                (event.target as HTMLElement).closest(
                  'button:not(.file-name),a,input,label',
                )
              )
                return;
              onSelect(
                selecting || event.ctrlKey || event.metaKey
                  ? selection.includes(row.path)
                    ? selection.filter((key) => key !== row.path)
                    : [...selection, row.path]
                  : [row.path],
              );
            },
            onDoubleClick: (event) => {
              if (
                !selecting &&
                !row.protected &&
                !(event.target as HTMLElement).closest('button,a,input,label')
              )
                onAction('open', [row]);
            },
          })}
          pagination={
            props.search
              ? false
              : {
                  current: props.page,
                  pageSize: props.pageSize,
                  total: props.total,
                  pageSizeOptions: [100, 200, 500],
                  showSizeChanger: true,
                  showTotal: (total, range) =>
                    `${range[0]}–${range[1]} / 共 ${total} 项`,
                  onChange: props.onPage,
                }
          }
          onChange={(_pagination, _filters, sorter, extra) => {
            if (
              extra.action === 'sort' &&
              !Array.isArray(sorter) &&
              sorter.field
            )
              props.onSort(
                sorter.order ? String(sorter.field) : 'name',
                sorter.order === 'descend' ? 'desc' : 'asc',
              );
          }}
          columns={[
            {
              title: '文件名',
              dataIndex: 'name',
              width: LAYOUT.fileNameWidth,
              sorter: true,
              sortOrder:
                props.sort === 'name'
                  ? props.order === 'desc'
                    ? 'descend'
                    : 'ascend'
                  : null,
              render: (name: string, row) => (
                <div className="file-identity">
                  <FileIcon entry={row} />
                  <div className="file-identity-text">
                    <Space size="small">
                      <Tooltip
                        title={
                          row.linkTarget
                            ? `${row.path} -> ${row.linkTarget}`
                            : row.path
                        }
                      >
                        <button
                          type="button"
                          className="file-name"
                          disabled={row.protected}
                          onClick={(event) => {
                            if (
                              !selecting &&
                              (row.isDir ||
                                row.linkIsDir ||
                                (!desktop && canPreviewFile(row))) &&
                              !event.ctrlKey &&
                              !event.metaKey &&
                              !event.shiftKey
                            ) {
                              event.stopPropagation();
                              onAction('open', [row]);
                            }
                          }}
                          onDoubleClick={() => {
                            if (!selecting && !row.isDir && !row.linkIsDir)
                              onAction('open', [row]);
                          }}
                        >
                          {name}
                          {row.isLink && row.linkTarget
                            ? ` -> ${row.linkTarget}`
                            : ''}
                        </button>
                      </Tooltip>
                      {row.protected && <Tag>受保护</Tag>}
                    </Space>
                    {props.search && (
                      <Typography.Text
                        className="file-result-path"
                        type="secondary"
                        ellipsis
                        title={row.path}
                      >
                        {row.path}
                      </Typography.Text>
                    )}
                    {row.match && (
                      <div className="file-content-match">
                        <Typography.Text type="secondary">
                          第 {row.match.line} 行
                        </Typography.Text>
                        <Typography.Text
                          ellipsis
                          title={row.match.text}
                          className="file-content-match-text"
                        >
                          {row.match.text}
                        </Typography.Text>
                      </div>
                    )}
                  </div>
                </div>
              ),
            },
            {
              title: '权限 / 所有者',
              width: LAYOUT.filePermissionsWidth,
              responsive: ['md'],
              render: (_, row) => (
                <Tooltip title={`用户组：${row.group}`}>
                  <Button
                    type="text"
                    size="small"
                    className="file-permissions"
                    disabled={row.protected}
                    onClick={() => onAction('permissions', [row])}
                  >
                    <span className="file-mode">{row.mode}</span>
                    <span className="file-owner"> / {row.owner}</span>
                  </Button>
                </Tooltip>
              ),
            },
            {
              title: '大小',
              dataIndex: 'size',
              width: LAYOUT.fileSizeWidth,
              align: 'right',
              sorter: true,
              sortOrder:
                props.sort === 'size'
                  ? props.order === 'desc'
                    ? 'descend'
                    : 'ascend'
                  : null,
              className: 'file-size',
              render: (size: number | null, row) =>
                row.isDir ? (
                  <DirectorySize entry={row} />
                ) : size === null ? (
                  '—'
                ) : (
                  formatSize(size)
                ),
            },
            {
              title: '修改时间',
              dataIndex: 'mtime',
              width: LAYOUT.fileTimeWidth,
              responsive: ['md'],
              sorter: true,
              sortOrder:
                props.sort === 'mtime'
                  ? props.order === 'desc'
                    ? 'descend'
                    : 'ascend'
                  : null,
              render: (_, row) => (
                <span className="file-time">{formatTime(row.modTime)}</span>
              ),
            },
            {
              title: '备注',
              key: 'remark',
              width: LAYOUT.fileRemarkWidth,
              responsive: ['lg'],
              render: (_, row) => (
                <FileRemark
                  key={`${row.path}-${row.remark}`}
                  entry={row}
                  onSaved={props.onRemarkSaved}
                />
              ),
            },
            {
              title: '操作',
              key: 'actions',
              className: 'file-row-actions',
              fixed: desktop ? 'right' : undefined,
              width: LAYOUT.fileActionsWidth,
              align: 'right',
              render: (_, row) => (
                <Dropdown
                  menu={menu([row])}
                  trigger={['click']}
                  disabled={row.protected}
                  onOpenChange={(open) => setMorePath(open ? row.path : null)}
                >
                  <Button
                    className="file-more"
                    type="link"
                    size="small"
                    disabled={row.protected}
                  >
                    更多
                  </Button>
                </Dropdown>
              ),
            },
          ]}
        />
      </div>
    </Dropdown>
  );
}
