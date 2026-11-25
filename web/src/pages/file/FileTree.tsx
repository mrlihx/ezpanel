import { useState } from 'react';
import { Alert, Button, Input, Space, Spin, Tooltip, Typography } from 'antd';
import {
  DownOutlined,
  FileOutlined,
  FolderFilled,
  ReloadOutlined,
  RightOutlined,
  ArrowUpOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { files, type FileEntry } from '../../api/file';

type FolderProps = {
  path: string;
  name: string;
  activePath: string;
  onOpen: (path: string) => void;
  initiallyOpen?: boolean;
};

function Folder({
  path,
  name,
  activePath,
  onOpen,
  initiallyOpen = false,
}: FolderProps) {
  const [expanded, setExpanded] = useState(initiallyOpen);
  const query = useInfiniteQuery({
    queryKey: ['files', 'editor-tree', path],
    enabled: expanded,
    initialPageParam: 1,
    queryFn: ({ pageParam, signal }) =>
      files<{ list: FileEntry[]; total: number }>('/list', {
        params: {
          path,
          page: pageParam,
          page_size: 200,
          sort: 'name',
          order: 'asc',
        },
        signal,
      }),
    getNextPageParam: (last, pages) =>
      pages.reduce((count, page) => count + page.list.length, 0) < last.total
        ? pages.length + 1
        : undefined,
  });
  return (
    <li role="treeitem" aria-expanded={expanded}>
      <button
        type="button"
        className="file-editor-tree-row"
        title={path}
        onClick={() => setExpanded(!expanded)}
      >
        {expanded ? <DownOutlined /> : <RightOutlined />}
        <FolderFilled className="file-folder-icon" />
        <span>{name}</span>
      </button>
      {expanded && (
        <ul role="group" className="file-editor-tree-group">
          {query.isPending && (
            <li role="none">
              <Spin size="small" />
            </li>
          )}
          {query.error && (
            <li role="none">
              <Alert
                type="error"
                message={query.error.message}
                action={
                  <Button size="small" onClick={() => void query.refetch()}>
                    重试
                  </Button>
                }
              />
            </li>
          )}
          {query.data?.pages
            .flatMap((page) => page.list)
            .map((entry) =>
              entry.isDir && !entry.protected ? (
                <Folder
                  key={entry.path}
                  path={entry.path}
                  name={entry.name}
                  activePath={activePath}
                  onOpen={onOpen}
                />
              ) : (
                <li
                  key={entry.path}
                  role="treeitem"
                  aria-selected={activePath === entry.path}
                >
                  <button
                    type="button"
                    title={entry.path}
                    disabled={entry.protected}
                    className={`file-editor-tree-row${activePath === entry.path ? ' file-editor-tree-selected' : ''}`}
                    onClick={() => onOpen(entry.path)}
                  >
                    <FileOutlined />
                    <span>{entry.name}</span>
                    {entry.protected && <span>受保护</span>}
                  </button>
                </li>
              ),
            )}
          {query.hasNextPage && (
            <li role="none">
              <Button
                type="link"
                size="small"
                loading={query.isFetchingNextPage}
                onClick={() => void query.fetchNextPage()}
              >
                加载更多
              </Button>
            </li>
          )}
          {query.data?.pages[0].total === 0 && (
            <li role="none">
              <Typography.Text type="secondary">空目录</Typography.Text>
            </li>
          )}
        </ul>
      )}
    </li>
  );
}

export default function FileTree({
  directory,
  activePath,
  onDirectoryChange,
  onOpen,
  onCreate,
}: {
  directory: string;
  activePath: string;
  onDirectoryChange: (path: string) => void;
  onOpen: (path: string) => void;
  onCreate: () => void;
}) {
  const client = useQueryClient();
  const [keyword, setKeyword] = useState('');
  const search = useQuery({
    queryKey: ['files', 'editor-search', directory, keyword],
    enabled: Boolean(keyword),
    queryFn: ({ signal }) =>
      files<{ list: FileEntry[]; truncated: boolean }>('/search', {
        params: { path: directory, keyword },
        signal,
      }),
  });
  return (
    <>
      <Typography.Text
        className="file-editor-directory"
        ellipsis
        title={directory}
      >
        目录：{directory}
      </Typography.Text>
      <Space className="file-editor-tree-tools" size="small">
        <Tooltip title="上一级">
          <Button
            type="text"
            size="small"
            aria-label="编辑器目录上一级"
            icon={<ArrowUpOutlined />}
            disabled={directory === '/'}
            onClick={() =>
              onDirectoryChange(
                directory.slice(0, directory.lastIndexOf('/')) || '/',
              )
            }
          />
        </Tooltip>
        <Tooltip title="刷新目录">
          <Button
            type="text"
            size="small"
            aria-label="刷新编辑器目录"
            icon={<ReloadOutlined />}
            onClick={() => {
              void client.invalidateQueries({
                queryKey: ['files', 'editor-tree'],
              });
              if (keyword) void search.refetch();
            }}
          />
        </Tooltip>
        <Tooltip title="新建文件">
          <Button
            type="text"
            size="small"
            aria-label="新建编辑器文件"
            icon={<PlusOutlined />}
            onClick={onCreate}
          />
        </Tooltip>
      </Space>
      <Input.Search
        className="file-editor-tree-search"
        placeholder="搜索目录内文件"
        allowClear
        onSearch={(value) => setKeyword(value.trim())}
        onChange={(event) => {
          if (!event.target.value) setKeyword('');
        }}
      />
      <div className="file-editor-tree-scroll">
        {keyword ? (
          <>
            {search.isFetching && <Spin size="small" />}
            {search.error && (
              <Alert type="error" message={search.error.message} />
            )}
            {search.data?.truncated && (
              <Typography.Text type="secondary">
                已显示前500项或10秒内的结果
              </Typography.Text>
            )}
            <ul role="tree">
              {search.data?.list.map((entry) => (
                <li role="treeitem" key={entry.path}>
                  <button
                    type="button"
                    title={entry.path}
                    className="file-editor-tree-row"
                    onClick={() =>
                      entry.isDir
                        ? onDirectoryChange(entry.path)
                        : onOpen(entry.path)
                    }
                  >
                    {entry.isDir ? (
                      <FolderFilled className="file-folder-icon" />
                    ) : (
                      <FileOutlined />
                    )}
                    <span>{entry.name}</span>
                  </button>
                </li>
              ))}
            </ul>
            {search.data?.list.length === 0 && (
              <Typography.Text type="secondary">未找到文件</Typography.Text>
            )}
          </>
        ) : (
          <ul role="tree">
            <Folder
              key={directory}
              path={directory}
              name={directory.split('/').filter(Boolean).pop() || '根目录'}
              activePath={activePath}
              onOpen={onOpen}
              initiallyOpen
            />
          </ul>
        )}
      </div>
    </>
  );
}
