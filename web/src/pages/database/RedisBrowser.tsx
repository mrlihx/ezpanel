import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Tabs,
  Tooltip,
  Typography,
  Grid,
} from 'antd';
import { RotateCw } from 'lucide-react';
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import { request } from '../../api/client';
import TaskDrawer from '../../components/TaskDrawer';
import { LAYOUT } from '../../styles/tokens';
type RedisKey = {
  name: string;
  value: string;
  type: string;
  length: number | null;
  ttl: number;
};
export default function RedisBrowser({
  prefix,
  editing,
  onEdit,
}: {
  prefix: string;
  editing: string | null;
  onEdit: (key: string | null) => void;
}) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { modal } = App.useApp();
  const client = useQueryClient();
  const [database, setDatabase] = useState(0);
  const [pattern, setPattern] = useState('*');
  const [task, setTask] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [batchAction, setBatchAction] = useState('delete');
  const [selected, setSelected] = useState<React.Key[]>([]);
  const dbs = useQuery({
    queryKey: ['nosql-databases', 'redis', prefix],
    queryFn: () =>
      request<{ list: { index: number; keys: number }[] }>(
        `${prefix}/databases`,
      ),
  });
  const keyQuery = ['redis-keys', prefix, database, pattern] as const;
  const keys = useInfiniteQuery({
    queryKey: keyQuery,
    initialPageParam: '0',
    queryFn: ({ pageParam }) =>
      request<{ nextCursor: string; list: RedisKey[] }>(`${prefix}/key-list`, {
        params: { database, pattern, cursor: pageParam },
      }),
    getNextPageParam: (last) =>
      last.nextCursor === '0' ? undefined : last.nextCursor,
    refetchOnWindowFocus: false,
  });
  const rows = [
    ...new Map(
      keys.data?.pages.flatMap((part) =>
        part.list.map((row) => [row.name, row] as const),
      ),
    ).values(),
  ];
  const refresh = () => {
    void client.resetQueries({ queryKey: keyQuery, exact: true });
    void client.invalidateQueries({ queryKey: ['nosql-databases', 'redis'] });
    setSelected([]);
  };
  const remove = (names: string[]) =>
    modal.confirm({
      title: `删除${names.length}个键？`,
      content: '删除后无法恢复。',
      okButtonProps: { danger: true },
      onOk: async () => {
        setBusy(true);
        try {
          const result = await request<{ taskId: string }>(
            `${prefix}/key-list`,
            {
              method: 'DELETE',
              params: { database },
              data: { keys: names },
            },
          );
          setTask(result.taskId);
          setSelected([]);
        } finally {
          setBusy(false);
        }
      },
    });
  const list = (
    <>
      <Flex justify="flex-end" gap={12} wrap style={{ marginBottom: 16 }}>
        <Space>
          <Input.Search
            key={database}
            allowClear
            placeholder="请输入键名称"
            onSearch={(value) => {
              setPattern(value || '*');
              setSelected([]);
            }}
          />
          <Button
            aria-label="刷新键列表"
            title="刷新"
            icon={<RotateCw size={16} strokeWidth={1.9} />}
            onClick={refresh}
          />
        </Space>
      </Flex>
      {keys.error && <Alert type="error" message={keys.error.message} />}
      <ContentSizedTable<RedisKey>
        fillColumn="value"
        growColumns={['name']}
        rowKey="name"
        loading={keys.isPending}
        dataSource={rows}
        rowSelection={{ selectedRowKeys: selected, onChange: setSelected }}
        pagination={false}
        footer={() => {
          const count = rows.filter((row) =>
            selected.includes(row.name),
          ).length;
          return (
            <Flex align="center" gap={16} wrap>
              <Checkbox
                checked={rows.length > 0 && count === rows.length}
                indeterminate={count > 0 && count < rows.length}
                disabled={!rows.length || busy}
                onChange={(event) =>
                  setSelected(
                    event.target.checked ? rows.map((row) => row.name) : [],
                  )
                }
              >
                全选
              </Checkbox>
              <Select
                aria-label="批量操作类型"
                style={{ width: 144 }}
                value={batchAction}
                onChange={setBatchAction}
                options={[{ value: 'delete', label: '删除键' }]}
              />
              <Button
                disabled={!selected.length || busy}
                onClick={() => remove(selected.map(String))}
              >
                批量操作（已选{selected.length}项）
              </Button>
              <Typography.Text type="secondary">
                已加载 {rows.length} 条
              </Typography.Text>
              {keys.hasNextPage && (
                <Button
                  loading={keys.isFetchingNextPage}
                  onClick={() => void keys.fetchNextPage()}
                >
                  继续扫描
                </Button>
              )}
            </Flex>
          );
        }}
        columns={[
          { title: '键', dataIndex: 'name', width: 260, ellipsis: true },
          {
            title: '值',
            dataIndex: 'value',
            width: 340,
            ellipsis: true,
            render: (value: string) => <Tooltip title={value}>{value}</Tooltip>,
          },
          { title: '数据类型', dataIndex: 'type', width: 130 },
          {
            title: '数据长度',
            dataIndex: 'length',
            width: 120,
            render: (value: number | null) => value ?? '—',
          },
          {
            title: '有效期',
            dataIndex: 'ttl',
            width: 210,
            render: (seconds: number) =>
              seconds === -1
                ? '永久'
                : seconds < 0
                  ? '已过期'
                  : `${Math.floor(seconds / 86400)}天${Math.floor((seconds % 86400) / 3600)}小时${Math.floor((seconds % 3600) / 60)}分钟${seconds % 60}秒`,
          },
          {
            title: '操作',
            fixed: fixedActions,
            width: 130,
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  style={{ padding: 0 }}
                  onClick={() => onEdit(row.name)}
                >
                  编辑
                </Button>
                <Button
                  type="link"
                  danger
                  style={{ padding: 0 }}
                  onClick={() => remove([row.name])}
                >
                  删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
    </>
  );
  return (
    <>
      {dbs.error && <Alert type="error" message={dbs.error.message} />}
      <Tabs
        type="card"
        activeKey={String(database)}
        onChange={(value) => {
          setDatabase(Number(value));
          setPattern('*');
          setSelected([]);
        }}
        items={(dbs.data?.list ?? [{ index: 0, keys: 0 }]).map((db) => ({
          key: String(db.index),
          label: `DB${db.index}【${db.keys}】`,
          children: list,
        }))}
      />
      <TaskDrawer
        taskId={task ?? null}
        title="Redis键删除"
        open={!!task}
        onClose={() => setTask(undefined)}
        onSettled={refresh}
      />
      {editing !== null && (
        <RedisEditor
          key={`${database}:${editing}`}
          prefix={prefix}
          database={database}
          name={editing}
          onClose={() => onEdit(null)}
          onSaved={refresh}
        />
      )}
    </>
  );
}
function RedisEditor({
  prefix,
  database,
  name,
  onClose,
  onSaved,
}: {
  prefix: string;
  database: number;
  name: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const value = useQuery({
    queryKey: ['redis-value', prefix, database, name],
    queryFn: () =>
      request<{ type: string; value: unknown; ttl: number; notice?: string }>(
        `${prefix}/value`,
        { params: { database, key: name } },
      ),
    enabled: !!name,
  });
  const editable = !name || value.data?.type === 'string';
  return (
    <Modal
      open
      title={name ? '编辑Key' : '添加Key'}
      width={LAYOUT.databaseModalWidth}
      onCancel={onClose}
      confirmLoading={busy}
      okButtonProps={{
        disabled: !editable || (!!name && (!value.data || value.isError)),
      }}
      onOk={() => form.submit()}
    >
      {value.error && <Alert type="error" message={value.error.message} />}
      {name && value.isPending ? (
        <div>正在读取键值…</div>
      ) : editable ? (
        <Form
          key={value.dataUpdatedAt}
          form={form}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
          initialValues={{
            key: name,
            value: value.data?.value ?? '',
            ttl: Math.max(0, value.data?.ttl ?? 0),
          }}
          onFinish={async (fields) => {
            setBusy(true);
            try {
              await request(`${prefix}/value`, {
                method: 'PUT',
                params: { database },
                data: fields,
              });
              onSaved();
              onClose();
            } finally {
              setBusy(false);
            }
          }}
        >
          <Form.Item name="key" label="键" rules={[{ required: true }]}>
            <Input disabled={!!name} />
          </Form.Item>
          <Form.Item name="value" label="值">
            <Input.TextArea rows={8} />
          </Form.Item>
          <Form.Item name="ttl" label="有效期" extra="单位秒，0表示永久">
            <InputNumber min={0} precision={0} />
          </Form.Item>
        </Form>
      ) : (
        <>
          <Alert type="info" message="当前类型仅供查看，暂不支持在线修改。" />
          <pre
            style={{ maxHeight: 400, overflow: 'auto', whiteSpace: 'pre-wrap' }}
          >
            {JSON.stringify(value.data, null, 2)}
          </pre>
        </>
      )}
    </Modal>
  );
}
