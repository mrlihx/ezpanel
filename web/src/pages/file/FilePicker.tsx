import { useState } from 'react';
import {
  Alert,
  Breadcrumb,
  Button,
  Input,
  Modal,
  Space,
  Typography,
} from 'antd';
import {
  ArrowUpOutlined,
  FileOutlined,
  FolderFilled,
  ReloadOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import { files, type FileEntry } from '../../api/file';
import { formatSize, formatTime } from '../../utils/format';
import { LAYOUT } from '../../styles/tokens';
import './DirectoryPicker.css';

export default function FilePicker({
  title = '选择文件',
  initialPath = '/',
  onSelect,
  onClose,
}: {
  title?: string;
  initialPath?: string;
  onSelect: (path: string) => void;
  onClose: () => void;
}) {
  const startDir =
    initialPath && initialPath !== '/'
      ? initialPath.replace(/\/+$/, '') || '/'
      : '/';
  const [path, setPath] = useState(startDir);
  const [address, setAddress] = useState(startDir);
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<string>();

  const query = useQuery({
    queryKey: ['file-picker-list', path, page],
    queryFn: ({ signal }) =>
      files<{ path: string; list: FileEntry[]; total: number }>('/list', {
        params: {
          path,
          page,
          page_size: 20,
          sort: 'name',
          order: 'asc',
        },
        signal,
      }),
  });

  const current = query.data?.path || path;

  function go(next: string) {
    const cleaned = next.trim() || '/';
    setPath(cleaned);
    setAddress(cleaned);
    setSelected(undefined);
    setPage(1);
  }

  const parts = current.split('/').filter(Boolean);

  return (
    <Modal
      title={title}
      open
      centered
      width={LAYOUT.trashModalWidth}
      onCancel={onClose}
      onOk={() => {
        if (selected) onSelect(selected);
      }}
      okButtonProps={{
        disabled: !selected || query.isFetching || !!query.error,
      }}
      okText="确认"
    >
      <div className="file-picker-toolbar">
        <Button
          icon={<ArrowUpOutlined />}
          aria-label="上一级目录"
          disabled={current === '/'}
          onClick={() => go(current.slice(0, current.lastIndexOf('/')) || '/')}
        />
        <Input.Search
          aria-label="目录绝对路径"
          value={address}
          onChange={(event) => setAddress(event.target.value)}
          onSearch={go}
          enterButton="前往"
        />
        <Button
          icon={<ReloadOutlined />}
          aria-label="刷新目录"
          loading={query.isFetching}
          onClick={() => void query.refetch()}
        />
      </div>
      <Breadcrumb
        items={[
          {
            title: (
              <Button type="link" size="small" onClick={() => go('/')}>
                根目录
              </Button>
            ),
          },
          ...parts.map((name, index) => ({
            title: (
              <Button
                type="link"
                size="small"
                onClick={() => go('/' + parts.slice(0, index + 1).join('/'))}
              >
                {name}
              </Button>
            ),
          })),
        ]}
      />
      {query.error && <Alert type="error" message={query.error.message} />}
      <ContentSizedTable<FileEntry>
        legacySizing
        fillColumn="name"
        rowKey="path"
        size="small"
        loading={query.isFetching}
        dataSource={query.data?.list}
        scroll={{ y: 340 }}
        rowSelection={{
          type: 'radio',
          selectedRowKeys: selected ? [selected] : [],
          onChange: (keys) => setSelected(String(keys[0])),
          getCheckboxProps: (row) => ({
            disabled: row.protected || row.isDir,
          }),
        }}
        onRow={(row) => ({
          onClick: () => {
            if (row.protected) return;
            if (!row.isDir) setSelected(row.path);
          },
          onDoubleClick: () => {
            if (row.protected) return;
            if (row.isDir) {
              go(row.path);
            } else {
              onSelect(row.path);
            }
          },
        })}
        pagination={{
          current: page,
          pageSize: 20,
          total: query.data?.total,
          showSizeChanger: false,
          onChange: (value) => {
            setPage(value);
            setSelected(undefined);
          },
        }}
        columns={[
          {
            title: '名称',
            dataIndex: 'name',
            render: (_, row) =>
              row.isDir ? (
                <Button
                  type="link"
                  size="small"
                  disabled={row.protected}
                  onClick={(e) => {
                    e.stopPropagation();
                    go(row.path);
                  }}
                >
                  <Space>
                    <FolderFilled />
                    {row.name}
                  </Space>
                </Button>
              ) : (
                <Space>
                  <FileOutlined />
                  <Typography.Text disabled={row.protected}>
                    {row.name}
                  </Typography.Text>
                </Space>
              ),
          },
          {
            title: '大小',
            dataIndex: 'size',
            width: 100,
            render: (size: number | null, row) =>
              row.isDir ? '—' : formatSize(size),
          },
          {
            title: '修改时间',
            dataIndex: 'modTime',
            width: 175,
            render: formatTime,
          },
        ]}
      />
      <div style={{ marginTop: 12 }}>
        <Typography.Text>当前选中文件：</Typography.Text>
        <Input
          value={selected ?? ''}
          placeholder="请在上方列表点击选择文件（双击文件夹进入目录）"
          readOnly
          aria-label="当前选中文件"
        />
      </div>
    </Modal>
  );
}
