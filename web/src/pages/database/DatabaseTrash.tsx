import { useState } from 'react';
import { Alert, Button, Input, Modal, Space, Typography, Grid } from 'antd';
import { RotateCw } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import { request } from '../../api/client';
import { formatTime } from '../../utils/format';
type Entry = {
  id: string;
  backupId: string;
  serverId: string | null;
  serverName: string;
  name: string;
  deletedAt: string;
  state: string;
};
export default function DatabaseTrash({
  onTask,
}: {
  onTask: (id: string) => void;
}) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const [page, setPage] = useState(1);
  const [operation, setOperation] = useState<{
    kind: 'restore' | 'purge' | 'empty';
    row?: Entry;
  }>();
  const [confirmation, setConfirmation] = useState('');
  const [busy, setBusy] = useState(false);
  const query = useQuery({
    queryKey: ['database-trash', page],
    queryFn: () =>
      request<{ list: Entry[]; total: number }>('/api/db/trash', {
        params: { page, page_size: 20 },
      }),
  });
  const expected =
    operation?.kind === 'empty'
      ? '确认清空数据库回收站'
      : (operation?.row?.name ?? '');
  const begin = (kind: 'restore' | 'purge' | 'empty', row?: Entry) => {
    setConfirmation('');
    setOperation({ kind, row });
  };
  return (
    <>
      <Alert
        type="info"
        showIcon
        message="数据库回收站支持MySQL逻辑备份恢复。MongoDB、Redis暂不支持；远程MySQL需本机具备兼容的备份工具。"
        style={{ marginBottom: 16 }}
      />
      <Space style={{ marginBottom: 16 }}>
        <Button
          danger
          disabled={!query.data?.total}
          onClick={() => begin('empty')}
        >
          清空回收站
        </Button>
        <Button
          aria-label="刷新"
          icon={<RotateCw size={16} strokeWidth={1.9} />}
          onClick={() => void query.refetch()}
        />
      </Space>
      {query.error && <Alert type="error" message={query.error.message} />}
      <ContentSizedTable<Entry>
        fillColumn="name"
        rowKey="id"
        dataSource={query.data?.list}
        loading={query.isFetching}
        pagination={{
          current: page,
          pageSize: 20,
          total: query.data?.total,
          showSizeChanger: false,
          onChange: setPage,
        }}
        columns={[
          { title: '数据库名', dataIndex: 'name', width: 180, ellipsis: true },
          {
            title: '数据库位置',
            dataIndex: 'serverName',
            width: 220,
            ellipsis: true,
          },
          {
            title: '删除时间',
            dataIndex: 'deletedAt',
            render: formatTime,
            width: 185,
          },
          {
            title: '状态',
            render: (_, row) =>
              ({
                recycled: '已回收',
                importing: '导入中或已中断',
                import_failed: '导入失败，请检查目标库',
                registration_pending: 'SQL 已导入，待登记',
              })[row.state] ?? '已备份，请检查原库',
            width: 170,
          },
          {
            title: '操作',
            fixed: fixedActions,
            width: 230,
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  href={`/api/db/backups/${row.backupId}/download`}
                >
                  下载
                </Button>
                <Button
                  type="link"
                  disabled={!row.serverId}
                  onClick={() => begin('restore', row)}
                >
                  {row.state === 'registration_pending' ? '补登记' : '恢复'}
                </Button>
                <Button type="link" danger onClick={() => begin('purge', row)}>
                  彻底删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        open={!!operation}
        title={
          operation?.kind === 'restore'
            ? '恢复数据库'
            : operation?.kind === 'empty'
              ? '清空数据库回收站'
              : '彻底删除回收备份'
        }
        onCancel={() => setOperation(undefined)}
        confirmLoading={busy}
        okButtonProps={{
          danger: operation?.kind !== 'restore',
          disabled: confirmation !== expected,
        }}
        onOk={async () => {
          setBusy(true);
          try {
            if (operation?.kind === 'restore') {
              const result = await request<{ taskId: string }>(
                `/api/db/trash/${operation.row!.id}/restore`,
                { method: 'POST', data: { confirmName: confirmation } },
              );
              onTask(result.taskId);
            } else
              await request(
                operation?.kind === 'empty'
                  ? '/api/db/trash/empty'
                  : `/api/db/trash/${operation?.row?.id}`,
                {
                  method: operation?.kind === 'empty' ? 'POST' : 'DELETE',
                  data:
                    operation?.kind === 'empty'
                      ? { confirmation }
                      : { confirmName: confirmation },
                },
              );
            setOperation(undefined);
            await query.refetch();
          } finally {
            setBusy(false);
          }
        }}
      >
        <Typography.Paragraph>
          {operation?.kind === 'restore'
            ? operation.row?.state === 'registration_pending'
              ? 'SQL 已导入。本次只恢复面板登记。'
              : '恢复至原服务器。已有同名库时会停止恢复。导入失败后请检查目标库，原备份会保留。'
            : '将永久删除恢复备份，无法撤销。'}
        </Typography.Paragraph>
        <Input
          aria-label="确认文案"
          value={confirmation}
          placeholder={`输入 ${expected} 确认`}
          onChange={(e) => setConfirmation(e.target.value)}
        />
      </Modal>
    </>
  );
}
