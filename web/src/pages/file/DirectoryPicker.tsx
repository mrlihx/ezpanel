import { useState } from 'react';
import {
  Alert,
  App,
  Breadcrumb,
  Button,
  Input,
  Modal,
  Space,
  Typography,
} from 'antd';
import {
  ArrowUpOutlined,
  FolderFilled,
  PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import { files, type FileEntry } from '../../api/file';
import { formatTime } from '../../utils/format';
import { LAYOUT } from '../../styles/tokens';
import './DirectoryPicker.css';

export default function DirectoryPicker({
  initialPath,
  onSelect,
  onClose,
}: {
  initialPath: string;
  onSelect: (path: string) => void;
  onClose: () => void;
}) {
  const { modal, message } = App.useApp();
  const [path, setPath] = useState(initialPath),
    [address, setAddress] = useState(initialPath),
    [keyword, setKeyword] = useState(''),
    [page, setPage] = useState(1),
    [selected, setSelected] = useState<string>();
  const query = useQuery({
    queryKey: ['file-directories', path, keyword, page],
    queryFn: ({ signal }) =>
      files<{ path: string; list: FileEntry[]; total: number }>(
        '/directories',
        { params: { path, keyword, page, page_size: 20 }, signal },
      ),
  });
  const current = query.data?.path || path;
  const target = selected || current;
  function go(next: string) {
    setPath(next);
    setAddress(next);
    setSelected(undefined);
    setPage(1);
    setKeyword('');
  }
  const parts = current.split('/').filter(Boolean);
  return (
    <Modal
      title="选择文件夹"
      open
      width={LAYOUT.trashModalWidth}
      onCancel={onClose}
      onOk={() => onSelect(target)}
      okButtonProps={{ disabled: query.isFetching || !!query.error }}
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
      <div className="file-picker-toolbar">
        <Button
          icon={<PlusOutlined />}
          onClick={() => {
            let name = '';
            modal.confirm({
              title: '新建文件夹',
              content: (
                <Input
                  autoFocus
                  aria-label="文件夹名称"
                  placeholder="请输入文件夹名称"
                  onChange={(event) => {
                    name = event.target.value;
                  }}
                />
              ),
              onOk: async () => {
                name = name.trim();
                if (
                  !name ||
                  name === '.' ||
                  name === '..' ||
                  name.includes('/')
                ) {
                  void message.error('请输入有效的文件夹名称');
                  return Promise.reject(new Error('文件夹名称无效'));
                }
                const path = `${current === '/' ? '' : current}/${name}`;
                await files('/create', {
                  method: 'POST',
                  data: { path, type: 'dir' },
                });
                setSelected(path);
                await query.refetch();
              },
            });
          }}
        >
          新建文件夹
        </Button>
        <Input.Search
          key={current}
          placeholder="搜索文件夹"
          allowClear
          onSearch={(value) => {
            setKeyword(value);
            setPage(1);
            setSelected(undefined);
          }}
        />
      </div>
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
          getCheckboxProps: (row) => ({ disabled: row.protected }),
        }}
        onRow={(row) => ({
          onClick: () => {
            if (!row.protected) setSelected(row.path);
          },
          onDoubleClick: () => {
            if (!row.protected) go(row.path);
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
            render: (_, row) => (
              <Button
                type="link"
                size="small"
                disabled={row.protected}
                onClick={() => go(row.path)}
              >
                <Space>
                  <FolderFilled />
                  {row.name}
                </Space>
              </Button>
            ),
          },
          {
            title: '修改时间',
            dataIndex: 'modTime',
            width: 185,
            render: formatTime,
          },
          {
            title: '权限/所有者',
            width: 160,
            render: (_, row) => `${row.mode} / ${row.owner}`,
          },
        ]}
      />
      <Typography.Text>当前选中文件夹：</Typography.Text>
      <Input value={target} readOnly aria-label="当前选中文件夹" />
    </Modal>
  );
}
