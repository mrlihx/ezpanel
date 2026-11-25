import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Grid,
  Modal,
  Space,
  Tooltip,
  Typography,
} from 'antd';
import { RotateCw } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import ContentSizedTable from './ContentSizedTable';
import { formatSize, formatTime } from '../utils/format';
import { LAYOUT } from '../styles/tokens';

export type LocalBackupRow = {
  fileName: string;
  size: number;
  createdAt: string;
  path: string;
};

export default function LocalBackupsModal({
  title,
  queryKey,
  allowCreate = true,
  createDisabledReason,
  fetchList,
  onCreate,
  onDelete,
  downloadUrl,
  onChanged,
  onClose,
}: {
  title: string;
  queryKey: unknown[];
  allowCreate?: boolean;
  createDisabledReason?: string;
  fetchList: (signal?: AbortSignal) => Promise<{ list: LocalBackupRow[] }>;
  onCreate: () => Promise<unknown>;
  onDelete: (fileName: string) => Promise<unknown>;
  downloadUrl: (fileName: string) => string;
  onChanged: () => void;
  onClose: () => void;
}) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { modal, message } = App.useApp();
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [working, setWorking] = useState(false);

  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => fetchList(signal),
  });

  const allRows = query.data?.list ?? [];
  const pagedRows = allRows.slice((page - 1) * size, page * size);

  const remove = (names: string[]) =>
    modal.confirm({
      title:
        names.length === 1
          ? '删除这份备份？'
          : `删除选中的${names.length}份备份？`,
      content: '仅删除备份文件和记录，不修改网站或数据库。删除后无法恢复。',
      okButtonProps: { danger: true },
      onOk: async () => {
        setWorking(true);
        try {
          const deleted: string[] = [];
          const failed: { name: string; message: string }[] = [];
          for (const name of names) {
            try {
              await onDelete(name);
              deleted.push(name);
            } catch (err) {
              failed.push({
                name,
                message: err instanceof Error ? err.message : '删除失败',
              });
            }
          }
          setSelected((keys) =>
            keys.filter((key) => !deleted.includes(String(key))),
          );
          const nextTotal = Math.max(0, allRows.length - deleted.length);
          setPage(
            Math.max(1, Math.min(page, Math.ceil(nextTotal / size) || 1)),
          );
          await query.refetch();
          onChanged();
          if (failed.length) {
            modal.error({
              title: `已删除${deleted.length}份，${failed.length}份未删除`,
              content: (
                <Space direction="vertical">
                  {failed.map((row) => (
                    <Typography.Text key={row.name}>
                      {row.name}：{row.message}
                    </Typography.Text>
                  ))}
                </Space>
              ),
            });
          } else {
            void message.success('备份已删除');
          }
        } finally {
          setWorking(false);
        }
      },
    });

  return (
    <Modal
      open
      title={title}
      width={LAYOUT.toolboxWidth}
      footer={null}
      maskClosable={false}
      onCancel={onClose}
    >
      {query.error && (
        <Alert
          type="error"
          message={query.error.message}
          style={{ marginBottom: 16 }}
        />
      )}
      <Space wrap style={{ marginBottom: 16, width: '100%' }}>
        <Tooltip
          title={!allowCreate ? createDisabledReason : '手动备份固定保存到本地'}
        >
          <Button
            type="primary"
            disabled={!allowCreate}
            loading={working}
            onClick={async () => {
              setWorking(true);
              try {
                await onCreate();
                await query.refetch();
                onChanged();
                void message.success('备份已创建');
              } catch (err) {
                void message.error(
                  err instanceof Error ? err.message : '备份失败',
                );
              } finally {
                setWorking(false);
              }
            }}
          >
            手动备份
          </Button>
        </Tooltip>
        <Button
          danger
          disabled={!selected.length || working}
          onClick={() => remove(selected.map(String))}
        >
          批量删除{selected.length ? `（${selected.length}）` : ''}
        </Button>
        <Button
          icon={<RotateCw size={15} strokeWidth={1.9} />}
          aria-label="刷新备份列表"
          loading={query.isFetching}
          onClick={() => void query.refetch()}
        />
      </Space>
      <ContentSizedTable<LocalBackupRow>
        fillColumn="fileName"
        rowKey="fileName"
        size="middle"
        loading={query.isFetching}
        dataSource={pagedRows}
        rowSelection={{
          selectedRowKeys: selected,
          onChange: setSelected,
          preserveSelectedRowKeys: true,
        }}
        pagination={{
          current: page,
          pageSize: size,
          total: allRows.length,
          showSizeChanger: true,
          pageSizeOptions: [10, 20, 50, 100],
          showTotal: (total) => `共 ${total} 条`,
          onChange: (next, nextSize) => {
            setPage(nextSize === size ? next : 1);
            setSize(nextSize);
          },
        }}
        columns={[
          {
            title: '文件名称',
            dataIndex: 'fileName',
            width: 320,
            ellipsis: true,
            render: (name: string) => <Tooltip title={name}>{name}</Tooltip>,
          },
          {
            title: '存储位置',
            key: 'storageType',
            width: 100,
            render: () => '本地',
          },
          { title: '大小', dataIndex: 'size', width: 110, render: formatSize },
          {
            title: '备份时间',
            dataIndex: 'createdAt',
            width: 170,
            render: formatTime,
          },
          {
            title: '备份方式',
            key: 'method',
            width: 110,
            render: () => '手动备份',
          },
          {
            title: '操作',
            width: 130,
            fixed: fixedActions,
            render: (_, row) => (
              <Space size={0}>
                <Button
                  type="link"
                  size="small"
                  href={downloadUrl(row.fileName)}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  下载
                </Button>
                <Button
                  type="link"
                  size="small"
                  danger
                  disabled={working}
                  onClick={() => remove([row.fileName])}
                >
                  删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
    </Modal>
  );
}
