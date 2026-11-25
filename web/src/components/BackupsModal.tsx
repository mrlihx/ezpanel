import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Modal,
  Space,
  Tooltip,
  Typography,
  Grid,
} from 'antd';
import { RotateCw } from 'lucide-react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import ContentSizedTable from './ContentSizedTable';
import {
  deleteBackupFiles,
  listBackups,
  manualBackup,
  type BackupFile,
  type BackupScope,
} from '../api/backup';
import { formatSize, formatTime } from '../utils/format';
import { LAYOUT } from '../styles/tokens';

export default function BackupsModal({
  title,
  scope,
  allowCreate,
  createDisabledReason,
  onStarted,
  onClose,
}: {
  title: string;
  scope: BackupScope;
  allowCreate: boolean;
  createDisabledReason?: string;
  onStarted: (id: string) => void;
  onClose: () => void;
}) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [working, setWorking] = useState(false);
  const query = useQuery({
    queryKey: ['backups', scope, page, size],
    queryFn: () => listBackups(scope, page, size),
  });
  const remove = (ids: string[]) =>
    modal.confirm({
      title:
        ids.length === 1 ? '删除这份备份？' : `删除选中的${ids.length}份备份？`,
      content: '仅删除备份文件和记录，不修改网站或数据库。删除后无法恢复。',
      okButtonProps: { danger: true },
      onOk: async () => {
        setWorking(true);
        try {
          const result: Awaited<ReturnType<typeof deleteBackupFiles>> = {
            deletedIds: [],
            failed: [],
          };
          // 跨页选择按接口批量上限分段提交，保留每份备份的处理结果。
          for (let offset = 0; offset < ids.length; offset += 100) {
            const batch = await deleteBackupFiles(
              scope.kind,
              ids.slice(offset, offset + 100),
            );
            result.deletedIds.push(...batch.deletedIds);
            result.failed.push(...batch.failed);
          }
          setSelected((keys) =>
            keys.filter((key) => !result.deletedIds.includes(String(key))),
          );
          setPage(
            Math.max(
              1,
              Math.min(
                page,
                Math.ceil(
                  ((query.data?.total ?? 0) - result.deletedIds.length) / size,
                ),
              ),
            ),
          );
          await Promise.all([
            client.invalidateQueries({ queryKey: ['backups'] }),
            client.invalidateQueries({ queryKey: ['websites'] }),
            client.invalidateQueries({ queryKey: ['db-databases'] }),
          ]);
          if (result.failed.length) {
            modal.error({
              title: `已删除${result.deletedIds.length}份，${result.failed.length}份未删除`,
              content: (
                <Space direction="vertical">
                  {result.failed.map((row) => (
                    <Typography.Text key={row.id}>
                      {query.data?.list.find((file) => file.id === row.id)
                        ?.fileName || row.id}
                      ：{row.message}
                    </Typography.Text>
                  ))}
                </Space>
              ),
            });
          } else message.success('备份已删除');
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
                const result = await manualBackup(scope);
                onStarted(result.taskId);
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
      <ContentSizedTable<BackupFile>
        fillColumn="fileName"
        rowKey="id"
        size="middle"
        loading={query.isFetching}
        dataSource={query.data?.list ?? []}
        rowSelection={{
          selectedRowKeys: selected,
          onChange: setSelected,
          preserveSelectedRowKeys: true,
        }}
        pagination={{
          current: page,
          pageSize: size,
          total: query.data?.total ?? 0,
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
            dataIndex: 'storageType',
            width: 100,
            render: (storage: string, row: BackupFile) =>
              row.localCopy
                ? '本地 + 远程'
                : storage === 'remote'
                  ? '远程'
                  : '本地',
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
            dataIndex: 'method',
            width: 110,
            render: (method: string) =>
              method === 'automatic' ? '自动备份' : '手动备份',
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
                  href={`/api/backups/${scope.kind}/${row.id}/download`}
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
                  onClick={() => remove([row.id])}
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
