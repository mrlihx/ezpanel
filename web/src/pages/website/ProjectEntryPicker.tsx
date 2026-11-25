import { useState } from 'react';
import { Alert, Button, Modal, Space, Typography } from 'antd';
import {
  ArrowUpOutlined,
  FileOutlined,
  FolderOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { files, type FileEntry } from '../../api/file';
import type { ProjectRuntime } from '../../api/website';
import ContentSizedTable from '../../components/ContentSizedTable';

export default function ProjectEntryPicker({
  root,
  runtime,
  onSelect,
  onClose,
}: {
  root: string;
  runtime: ProjectRuntime;
  onSelect: (file: string) => void;
  onClose: () => void;
}) {
  const [path, setPath] = useState(root);
  const [page, setPage] = useState(1);
  const prefix = root === '/' ? '/' : `${root}/`;
  const entries = useQuery({
    queryKey: ['project-entry-files', runtime, path, page],
    queryFn: ({ signal }) =>
      files<{ list: FileEntry[]; total: number }>('/list', {
        params: {
          path,
          page,
          page_size: 20,
          extension: runtime === 'python' ? '.py' : undefined,
        },
        signal,
        silentError: true,
      }),
  });
  const go = (next: string) => {
    setPath(next);
    setPage(1);
  };
  return (
    <Modal
      title={runtime === 'go' ? '选择项目执行文件' : '选择入口文件'}
      open
      footer={null}
      onCancel={onClose}
    >
      <Space style={{ marginBottom: 12 }}>
        <Button
          icon={<ArrowUpOutlined />}
          disabled={path === root}
          onClick={() => go(path.slice(0, path.lastIndexOf('/')) || '/')}
        >
          上一级
        </Button>
        <Typography.Text code>/{path.slice(prefix.length)}</Typography.Text>
      </Space>
      {entries.error && <Alert type="error" message={entries.error.message} />}
      <ContentSizedTable<FileEntry>
        fillColumn="name"
        rowKey="path"
        size="small"
        loading={entries.isFetching}
        dataSource={entries.data?.list}
        scroll={{ y: 320 }}
        pagination={{
          current: page,
          pageSize: 20,
          total: entries.data?.total,
          showSizeChanger: false,
          onChange: setPage,
        }}
        columns={[
          {
            title: '名称',
            dataIndex: 'name',
            render: (_, file) => {
              const relative = file.path.slice(prefix.length);
              const valid =
                runtime === 'python'
                  ? /^[A-Za-z_]\w*(?:\/[A-Za-z_]\w*)*$/.test(
                      file.isDir ? relative : relative.slice(0, -3),
                    )
                  : /^[a-zA-Z0-9/._-]+$/.test(relative) &&
                    (file.isDir ||
                      runtime === 'go' ||
                      /\.[cm]?js$/.test(relative));
              return (
                <Button
                  type="link"
                  icon={file.isDir ? <FolderOutlined /> : <FileOutlined />}
                  disabled={
                    !valid ||
                    file.protected ||
                    file.isLink ||
                    !file.path.startsWith(prefix)
                  }
                  onClick={() =>
                    file.isDir ? go(file.path) : onSelect(relative)
                  }
                >
                  {file.name}
                </Button>
              );
            },
          },
        ]}
      />
    </Modal>
  );
}
