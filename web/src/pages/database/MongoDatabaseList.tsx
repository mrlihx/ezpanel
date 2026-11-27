import { useState } from 'react';
import ResourceActions, {
  useResourceActions,
} from '../../plugins/ResourceActions';
import {
  Alert,
  App,
  Button,
  Flex,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Tooltip,
  Typography,
  Grid,
} from 'antd';
import { Eye, EyeOff } from 'lucide-react';
import ContentSizedTable from '../../components/ContentSizedTable';
import { request } from '../../api/client';
import InlineRemark from '../../components/InlineRemark';
import BackupsModal from '../../components/BackupsModal';
import TaskDrawer from '../../components/TaskDrawer';
import { LAYOUT } from '../../styles/tokens';
import DatabaseSelectAll from './DatabaseSelectAll';

export type MongoDatabase = {
  name: string;
  size: number;
  system: boolean;
  createdAt?: string;
  users: string[];
  username: string;
  hasPassword: boolean;
  remark: string;
  backupCount: number;
  userNotice?: string;
  _connectionId?: string;
  _prefix?: string;
  _location?: string;
};
type Action = 'password' | 'backup' | 'delete';
export default function MongoDatabaseList({
  prefix,
  connectionId,
  locationLabel,
  rows,
  loading,
  onData,
  onDelete,
  onChanged,
}: {
  prefix: string;
  connectionId: string;
  locationLabel?: string;
  rows: MongoDatabase[];
  loading: boolean;
  onData: (name: string, connectionId?: string) => void;
  onDelete: (name: string, connectionId?: string) => void;
  onChanged: () => void;
}) {
  const pluginActions = useResourceActions('databases');
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { message, modal } = App.useApp();
  const [page, setPage] = useState(1),
    [size, setSize] = useState(20);
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [action, setAction] = useState<Action>('password');
  const [operation, setOperation] = useState<{
    action: Action | 'createAccount';
    names: string[];
    targetPrefix: string;
  }>();
  const [verify, setVerify] = useState(false),
    [confirmation, setConfirmation] = useState('');
  const [busy, setBusy] = useState(false);
  const [backup, setBackup] = useState<{
    name: string;
    connectionId: string;
  }>();
  const [passwordRevision, setPasswordRevision] = useState(0);
  const [task, setTask] = useState<{ id: string; title: string }>();
  const [form] = Form.useForm();
  const currentPage = Math.min(
    page,
    Math.max(1, Math.ceil(rows.length / size)),
  );
  const pageRows = rows.slice((currentPage - 1) * size, currentPage * size);
  const rowKey = (row: MongoDatabase) =>
    `${row._connectionId || connectionId}:${row.name}`;
  const selectedRows = rows.filter((row) => selected.includes(rowKey(row)));
  async function submit(
    selectedAction: Action,
    databases: string[],
    values: Record<string, string> = {},
    targetPrefix?: string,
  ) {
    setBusy(true);
    try {
      const result = await request<{ taskId: string }>(
        `${targetPrefix || prefix}/batch`,
        {
          method: 'POST',
          data: { action: selectedAction, databases, ...values },
        },
      );
      setTask({
        id: result.taskId,
        title:
          selectedAction === 'password'
            ? '修改MongoDB密码'
            : selectedAction === 'backup'
              ? '备份MongoDB数据库'
              : '删除MongoDB数据库',
      });
      setOperation(undefined);
      setVerify(false);
      setSelected([]);
    } finally {
      setBusy(false);
    }
  }
  function open(
    selectedAction: Action | 'createAccount',
    databases: MongoDatabase[],
  ) {
    if (databases.length > 100) {
      message.info('每批最多操作100个数据库');
      return;
    }
    const resolvedPrefix = databases[0]?._prefix || prefix;
    if (databases.some((row) => (row._prefix || prefix) !== resolvedPrefix)) {
      message.warning(
        '批量操作仅支持同一 MongoDB 实例下的数据库，请先筛选单个实例',
      );
      return;
    }
    const names = databases.map((row) => row.name);
    form.resetFields();
    setConfirmation('');
    setVerify(false);
    if (selectedAction === 'backup') {
      modal.confirm({
        title: `备份选中的${databases.length}个数据库？`,
        content:
          '使用mongodump创建本地BSON压缩归档，包含集合及索引定义。备份期间有写入时不保证跨集合时间点一致。',
        onOk: () => submit('backup', names, {}, resolvedPrefix),
      });
    } else {
      setOperation({
        action: selectedAction,
        names,
        targetPrefix: resolvedPrefix,
      });
      if (databases.length === 1)
        form.setFieldValue(
          'username',
          selectedAction === 'createAccount'
            ? databases[0].name
            : databases[0].username,
        );
    }
  }
  const passwordRow =
    operation?.names.length === 1
      ? rows.find(
          (row) =>
            row.name === operation.names[0] &&
            (row._prefix || prefix) === operation.targetPrefix,
        )
      : undefined;
  return (
    <>
      <ContentSizedTable<MongoDatabase>
        fillColumn="name"
        growColumns={['username', 'remark']}
        rowKey={rowKey}
        loading={loading}
        dataSource={rows}
        rowSelection={{
          selectedRowKeys: selected,
          onChange: setSelected,
          preserveSelectedRowKeys: true,
        }}
        pagination={{
          current: currentPage,
          pageSize: size,
          showSizeChanger: true,
          pageSizeOptions: [10, 20, 50, 100],
          showTotal: (total) => `共 ${total} 条`,
          onChange: (next, nextSize) => {
            setPage(nextSize === size ? next : 1);
            setSize(nextSize);
          },
        }}
        footer={() => (
          <Flex align="center" gap={16} wrap>
            <DatabaseSelectAll
              pageKeys={pageRows.map(rowKey)}
              selected={selected}
              onChange={setSelected}
              disabled={busy}
            />
            <Select
              aria-label="批量操作类型"
              style={{ width: 144 }}
              value={action}
              onChange={setAction}
              options={[
                { value: 'password', label: '修改密码' },
                { value: 'backup', label: '备份数据库' },
                { value: 'delete', label: '删除数据库' },
              ]}
            />
            <Button
              disabled={!selectedRows.length || busy}
              onClick={() => open(action, selectedRows)}
            >
              批量操作（已选{selectedRows.length}项）
            </Button>
          </Flex>
        )}
        columns={[
          {
            title: '数据库名',
            dataIndex: 'name',
            minWidth: 160,
            render: (name: string, row) => (
              <Button
                type="link"
                onClick={() => onData(name, row._connectionId || connectionId)}
              >
                {name}
              </Button>
            ),
          },
          {
            title: '用户名',
            key: 'username',
            minWidth: 136,
            render: (_, row) => (
              <Tooltip title={row.userNotice}>
                {row.userNotice
                  ? '无法读取'
                  : row.username || row.users.join('、') || '无本库用户'}
              </Tooltip>
            ),
          },
          {
            title: '密码',
            minWidth: 120,
            render: (_, row) => (
              <MongoPassword
                key={`${row._connectionId || connectionId}-${row.name}-${row.hasPassword}-${row.username}-${passwordRevision}`}
                prefix={row._prefix || prefix}
                row={row}
              />
            ),
          },
          {
            title: '备份',
            minWidth: 102,
            render: (_, row) => (
              <Button
                type="link"
                style={{ padding: 0 }}
                onClick={() =>
                  setBackup({
                    name: row.name,
                    connectionId: row._connectionId || connectionId,
                  })
                }
              >
                {row.backupCount ? `${row.backupCount}份备份` : '无备份'}
              </Button>
            ),
          },
          {
            title: '数据库位置',
            key: 'location',
            minWidth: 132,
            width: 150,
            ellipsis: true,
            render: (_, row) => row._location || locationLabel || 'MongoDB',
          },
          {
            title: '备注',
            key: 'remark',
            minWidth: 136,
            width: 220,
            render: (_, row) => (
              <InlineRemark
                key={`${row._connectionId || connectionId}-${row.name}-${row.remark}`}
                value={row.remark}
                label={`编辑${row.name}备注`}
                onSave={async (remark) => {
                  await request(
                    `${row._prefix || prefix}/databases/${encodeURIComponent(row.name)}/metadata`,
                    { method: 'PUT', data: { remark } },
                  );
                  onChanged();
                }}
              />
            ),
          },
          {
            title: '操作',
            minWidth: 128,
            fixed: fixedActions,
            width: 180,
            render: (_, row) => (
              <Space size={4}>
                <Button
                  type="link"
                  size="small"
                  onClick={() =>
                    onData(row.name, row._connectionId || connectionId)
                  }
                >
                  数据
                </Button>
                <Tooltip title={row.userNotice}>
                  <Button
                    type="link"
                    size="small"
                    disabled={!!row.userNotice && !row.users.length}
                    onClick={() =>
                      open(row.users.length ? 'password' : 'createAccount', [
                        row,
                      ])
                    }
                  >
                    {row.users.length ? '改密' : '补建账号'}
                  </Button>
                </Tooltip>
                <Button
                  type="link"
                  size="small"
                  danger
                  onClick={() =>
                    onDelete(row.name, row._connectionId || connectionId)
                  }
                >
                  删除
                </Button>
                <ResourceActions
                  items={pluginActions(
                    row._connectionId || connectionId,
                    row.name,
                    'mongodb',
                  )}
                />
              </Space>
            ),
          },
        ]}
      />
      {backup && (
        <BackupsModal
          title={`[${backup.name}] 数据库备份`}
          scope={{
            kind: 'mongodb',
            connectionId: backup.connectionId,
            database: backup.name,
          }}
          allowCreate
          onStarted={(id) => setTask({ id, title: '备份MongoDB数据库' })}
          onClose={() => setBackup(undefined)}
        />
      )}
      <Modal
        open={!!operation}
        title={
          operation?.action === 'password'
            ? '修改MongoDB数据库密码'
            : operation?.action === 'createAccount'
              ? '补建 MongoDB 专属账号'
              : '删除MongoDB数据库'
        }
        width={LAYOUT.databaseModalWidth}
        maskClosable={false}
        confirmLoading={busy}
        onCancel={() => setOperation(undefined)}
        okButtonProps={{ danger: operation?.action === 'delete' }}
        onOk={async () => {
          if (!operation) return;
          if (operation.action === 'delete') {
            setVerify(true);
            return;
          }
          const values = await form.validateFields();
          if (operation.action === 'createAccount') {
            setBusy(true);
            try {
              await request(
                `${operation.targetPrefix}/databases/${encodeURIComponent(operation.names[0])}/account`,
                { method: 'POST', data: values },
              );
              setOperation(undefined);
              onChanged();
              message.success('专属账号已创建');
            } finally {
              setBusy(false);
            }
            return;
          }
          await submit(
            'password',
            operation.names,
            values,
            operation.targetPrefix,
          );
        }}
      >
        <Typography.Paragraph>
          {operation?.names.join('、')}
        </Typography.Paragraph>
        {operation?.action === 'delete' ? (
          <Alert
            type="warning"
            showIcon
            message="数据库内的所有数据将永久删除，无法恢复，请核对以上数据库。"
          />
        ) : (
          <Form form={form} layout="vertical">
            {operation?.action === 'createAccount' ? (
              <Form.Item
                name="username"
                label="专属用户名"
                rules={[{ required: true, max: 128 }]}
              >
                <Input autoComplete="off" />
              </Form.Item>
            ) : passwordRow ? (
              <Form.Item
                name="username"
                label="实际用户名"
                rules={[{ required: true, message: '请选择此库实际用户' }]}
              >
                <Select
                  options={passwordRow.users.map((user) => ({
                    value: user,
                    label: user,
                  }))}
                />
              </Form.Item>
            ) : (
              <Alert
                type="info"
                style={{ marginBottom: 16 }}
                message="逐库修改已记录且仍存在的专属用户，或本库唯一用户；无用户或多个用户无法确定时，该项失败并保留原密码。"
              />
            )}
            <Form.Item
              name="password"
              label="新密码"
              rules={[
                { required: true, message: '请输入新密码' },
                { max: 1024 },
              ]}
            >
              <Input.Password autoComplete="new-password" />
            </Form.Item>
          </Form>
        )}
      </Modal>
      <Modal
        open={verify}
        title="二次验证信息—删除数据库"
        maskClosable={false}
        confirmLoading={busy}
        onCancel={() => setVerify(false)}
        okButtonProps={{
          danger: true,
          disabled: confirmation !== '删除数据库',
        }}
        onOk={() =>
          operation &&
          submit(
            'delete',
            operation.names,
            { confirmation },
            operation.targetPrefix,
          )
        }
      >
        <Typography.Paragraph>
          请输入「删除数据库」确认永久删除已选{operation?.names.length}
          个数据库。
        </Typography.Paragraph>
        <Input
          value={confirmation}
          onChange={(event) => setConfirmation(event.target.value)}
        />
      </Modal>
      <TaskDrawer
        taskId={task?.id ?? null}
        title={task?.title ?? 'MongoDB任务'}
        open={!!task}
        onClose={() => setTask(undefined)}
        onSettled={() => {
          setPasswordRevision((value) => value + 1);
          onChanged();
        }}
      />
    </>
  );
}
function MongoPassword({
  prefix,
  row,
}: {
  prefix: string;
  row: MongoDatabase;
}) {
  const [value, setValue] = useState<string>();
  const [busy, setBusy] = useState(false);
  if (!row.hasPassword)
    return (
      <Tooltip title="只能显示面板记录的专属账号密码，MongoDB不能还原已有密码">
        未记录
      </Tooltip>
    );
  return (
    <Space size={0}>
      <Typography.Text copyable={value === undefined ? false : { text: value }}>
        {value === undefined ? '********' : value}
      </Typography.Text>
      <Button
        type="text"
        size="small"
        loading={busy}
        aria-label={value === undefined ? '显示密码' : '隐藏密码'}
        icon={
          value === undefined ? (
            <Eye size={14} strokeWidth={1.9} />
          ) : (
            <EyeOff size={14} strokeWidth={1.9} />
          )
        }
        onClick={async () => {
          if (value !== undefined) {
            setValue(undefined);
            return;
          }
          setBusy(true);
          try {
            const result = await request<{ password: string }>(
              `${prefix}/databases/${encodeURIComponent(row.name)}/password/reveal`,
              { method: 'POST' },
            );
            setValue(result.password);
          } finally {
            setBusy(false);
          }
        }}
      />
    </Space>
  );
}
