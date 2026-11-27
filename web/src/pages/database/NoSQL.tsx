import { useEffect, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Card,
  Empty,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
} from 'antd';
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { request } from '../../api/client';
import { RotateCw } from 'lucide-react';
import type { Application } from '../../api/software';
import ApplicationInstallGuide from '../../components/ApplicationInstallGuide';
import ApplicationServiceControl from '../apps/ApplicationServiceControl';
import RemoteDatabases from './RemoteDatabases';
import DeleteDatabaseModal from './DeleteDatabaseModal';
import RedisBrowser from './RedisBrowser';
import RootPasswordModal from './RootPasswordModal';
import DatabaseInstanceControl from './DatabaseInstanceControl';
import DatabaseInstancesStatus from './DatabaseInstancesStatus';
import { useDatabaseSelection } from './useDatabaseSelection';
import MongoDataModal from './MongoDataModal';
import { LAYOUT, SPACING } from '../../styles/tokens';
import MongoDatabaseList, { type MongoDatabase } from './MongoDatabaseList';
type Connection = {
  local: boolean;
  instanceId?: string;
  version?: string;
  running?: boolean;
  id: string;
  name: string;
  host: string;
  port: number;
  username: string;
  hasPassword: boolean;
  tls: boolean;
  authSource: string;
};
export default function NoSQL({ engine }: { engine: 'mongodb' | 'redis' }) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const [rootOpen, setRootOpen] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [remoteOpen, setRemoteOpen] = useState(false);
  const [deleting, setDeleting] = useState<string>();
  const [editing, setEditing] = useState<Partial<Connection>>();
  const [busy, setBusy] = useState(false);
  const [creating, setCreating] = useState(false);
  const [selected, setSelected] = useState('');
  const [redisEditor, setRedisEditor] = useState<string | null>(null);
  const [redisToolbar, setRedisToolbar] = useState<HTMLDivElement | null>(null);
  const [form] = Form.useForm();
  const [operation] = Form.useForm();
  const base = `/api/db/nosql/${engine}`;
  const revealedPassword = useQuery({
    queryKey: ['nosql-connection-password', engine, editing?.id],
    enabled: Boolean(editing?.id && editing.hasPassword),
    staleTime: 0,
    gcTime: 0,
    queryFn: () =>
      request<{ password: string }>(
        `${base}/connections/${editing!.id}/password/reveal`,
        { method: 'POST', silentError: true },
      ),
  });
  const passwordLoading = Boolean(
    editing?.id && editing.hasPassword && revealedPassword.isFetching,
  );
  const passwordLoaded = Boolean(
    editing?.id && (!editing.hasPassword || revealedPassword.isSuccess),
  );
  useEffect(() => {
    if (editing?.id && revealedPassword.data) {
      form.setFieldValue('password', revealedPassword.data.password ?? '');
    }
  }, [editing?.id, revealedPassword.data, form]);
  const environment = useQuery({
    queryKey: ['nosql-env', engine],
    queryFn: () => request<{ application: Application }>(`${base}/env`),
  });
  const label = engine === 'mongodb' ? 'MongoDB' : 'Redis';
  const [allMode, setAllMode] = useState(engine === 'mongodb');
  const [activeConnId, setActiveConnId] = useState<string>('');
  const connLabel = (row: Connection) =>
    row.local && !row.instanceId ? label : row.name.replace(/^本机\s+/, '');
  const connections = useQuery({
    queryKey: ['nosql-connections', engine],
    queryFn: () => request<Connection[]>(`${base}/connections`),
  });
  const { selected: current, setSid } = useDatabaseSelection(
    engine,
    connections.data,
  );
  const isAllMongo = engine === 'mongodb' && allMode;
  const mongoCreateTarget =
    current && current.running !== false
      ? current
      : isAllMongo
        ? connections.data?.find((row) => row.running !== false)
        : undefined;
  const mongoTargets =
    engine === 'mongodb'
      ? isAllMongo
        ? (connections.data ?? []).filter((c) => c.running !== false)
        : current && current.running !== false
          ? [current]
          : []
      : [];
  const mongoQueries = useQueries({
    queries: mongoTargets.map((conn) => ({
      queryKey: ['nosql-databases', engine, conn.id],
      queryFn: () =>
        request<MongoDatabase[]>(`${base}/connections/${conn.id}/databases`),
    })),
  });
  const mongoRows: MongoDatabase[] = mongoTargets.flatMap((conn, idx) =>
    (mongoQueries[idx]?.data ?? []).map((row) => ({
      ...row,
      _connectionId: conn.id,
      _prefix: `${base}/connections/${conn.id}`,
      _location: connLabel(conn),
    })),
  );
  const mongoFetching = mongoQueries.some((q) => q.isFetching);
  const [previousId, setPreviousId] = useState(current?.id);
  if (previousId !== current?.id) {
    setPreviousId(current?.id);
    setDeleting(undefined);
    setSelected('');
    setRedisEditor(null);
  }
  const localApplication = environment.data?.application;
  const dockerInstance =
    current?.instanceId ??
    (current?.local &&
    localApplication?.installed &&
    localApplication.pkgManager === 'Docker'
      ? 'default'
      : undefined);
  const prefix = `${base}/connections/${current?.id}`;
  const activeMongoPrefix = `${base}/connections/${activeConnId || current?.id}`;
  const dbs = {
    data: mongoRows,
    isFetching: mongoFetching,
    error: mongoQueries.find((q) => q.error)?.error ?? null,
    refetch: () =>
      client.invalidateQueries({ queryKey: ['nosql-databases', engine] }),
  };
  const refresh = () => {
    for (const queryKey of [
      ['nosql-connections', engine],
      ['nosql-env', engine],
      ['backups'],
      ['nosql-databases', engine],
      ['mongo-collections'],
      ['mongo-documents'],
      ['redis-keys'],
      ['redis-value'],
    ])
      void client.invalidateQueries({ queryKey });
  };
  async function run(job: () => Promise<unknown>) {
    setBusy(true);
    try {
      await job();
      refresh();
      message.success('操作已完成');
    } finally {
      setBusy(false);
    }
  }
  function open(kind: 'database' | 'key') {
    operation.resetFields();
    if (kind === 'database') {
      operation.setFieldsValue({ serverId: mongoCreateTarget?.id });
      setCreating(true);
    }
    if (kind === 'key') {
      setRedisEditor('');
    }
  }
  const errors = [environment.error, connections.error, dbs.error].filter(
    Boolean,
  );
  const needsInstallation =
    environment.isSuccess &&
    !environment.data.application.installed &&
    connections.isSuccess &&
    connections.data.length === 0;
  return (
    <>
      {needsInstallation ? (
        <ApplicationInstallGuide
          name={label}
          install={engine}
          onDetect={refresh}
          extra={
            <Button onClick={() => setRemoteOpen(true)}>添加远程数据库</Button>
          }
        />
      ) : (
        <div style={{ minHeight: 500 }}>
          <Card style={{ marginBottom: SPACING.lg }}>
            <Flex justify="space-between" align="center" gap={SPACING.md} wrap>
              <Space wrap>
                {engine === 'redis' && (
                  <Button
                    type="primary"
                    disabled={!current || current.running === false}
                    onClick={() => open('key')}
                  >
                    添加Key
                  </Button>
                )}
                {engine === 'mongodb' && (
                  <Button
                    type="primary"
                    disabled={!mongoCreateTarget}
                    onClick={() => open('database')}
                  >
                    添加数据库
                  </Button>
                )}

                {engine === 'mongodb' && (
                  <Button
                    disabled={!current || current.running === false}
                    onClick={() => setRootOpen(true)}
                  >
                    ROOT密码
                  </Button>
                )}
                <Button onClick={() => setRemoteOpen(true)}>远程数据库</Button>
                {isAllMongo &&
                connections.data &&
                connections.data.length > 1 ? (
                  <DatabaseInstancesStatus
                    engine={engine}
                    instances={connections.data.map((connection) => ({
                      id: connection.id,
                      name: connLabel(connection),
                      version: connection.version,
                      running: connection.local
                        ? (connection.running ?? !!localApplication?.running)
                        : connection.running,
                      instanceId:
                        connection.instanceId ??
                        (connection.local &&
                        localApplication?.pkgManager === 'Docker'
                          ? 'default'
                          : undefined),
                      native:
                        connection.local && !!localApplication?.manageable,
                    }))}
                    onChanged={refresh}
                  />
                ) : dockerInstance && current ? (
                  <DatabaseInstanceControl
                    key={current.id}
                    engine={engine}
                    instanceId={dockerInstance}
                    name={current.name}
                    version={
                      current.version ?? localApplication?.version ?? undefined
                    }
                    running={current.running ?? !!localApplication?.running}
                    onAuthentication={() => setEditing(current)}
                    onChanged={refresh}
                  />
                ) : (
                  (!current || current.local) &&
                  environment.data?.application && (
                    <ApplicationServiceControl
                      app={environment.data.application}
                      onAuthentication={
                        current?.local ? () => setEditing(current) : undefined
                      }
                      onChanged={refresh}
                    />
                  )
                )}
              </Space>
              <Space wrap>
                <Select
                  style={{
                    width: LAYOUT.databaseSelectWidth,
                    maxWidth: '100%',
                  }}
                  placeholder={`选择 ${label} 实例`}
                  value={isAllMongo ? '__all__' : current?.id}
                  options={[
                    ...(engine === 'mongodb'
                      ? [{ value: '__all__', label: '全部' }]
                      : []),
                    ...(connections.data?.map((row) => ({
                      value: row.id,
                      label: `${connLabel(row)}${row.running === false ? '（已停止）' : ''}`,
                    })) ?? []),
                  ]}
                  onChange={(v) => {
                    if (v === '__all__') {
                      setAllMode(true);
                    } else {
                      setAllMode(false);
                      setSid(v);
                    }
                    setDeleting(undefined);
                    setSelected('');
                    setRedisEditor(null);
                  }}
                />
                {engine === 'mongodb' ? (
                  <>
                    <Input.Search
                      allowClear
                      placeholder="请输入数据库名称"
                      onSearch={setKeyword}
                    />
                    <Button
                      aria-label="刷新"
                      title="刷新"
                      icon={<RotateCw size={16} strokeWidth={1.9} />}
                      onClick={refresh}
                    />
                  </>
                ) : (
                  current &&
                  current.running !== false && <div ref={setRedisToolbar} />
                )}
              </Space>
            </Flex>
          </Card>
          <Card>
            {errors.map((err, i) => (
              <Alert
                key={i}
                type="error"
                message={err?.message}
                style={{ marginBottom: 12 }}
              />
            ))}
            {!isAllMongo && current?.running === false ? (
              <Empty description="所选实例已停止" />
            ) : !current && !isAllMongo ? (
              <Empty description={`添加${label}连接后管理数据库`} />
            ) : engine === 'mongodb' ? (
              <>
                <MongoDatabaseList
                  key={isAllMongo ? '__all__' : current?.id}
                  prefix={prefix}
                  connectionId={current?.id ?? ''}
                  locationLabel={current ? connLabel(current) : label}
                  loading={dbs.isFetching}
                  rows={
                    dbs.data?.filter(
                      (row) =>
                        !row.system &&
                        row.name.toLowerCase().includes(keyword.toLowerCase()),
                    ) ?? []
                  }
                  onData={(name, cid) => {
                    setActiveConnId(cid || current?.id || '');
                    setSelected(name);
                  }}
                  onDelete={(name, cid) => {
                    setActiveConnId(cid || current?.id || '');
                    setDeleting(name);
                  }}
                  onChanged={refresh}
                />
                {selected && (
                  <MongoDataModal
                    key={`${activeConnId || current?.id}-${selected}`}
                    prefix={activeMongoPrefix}
                    database={selected}
                    readOnly={
                      dbs.data?.find((row) => row.name === selected)?.system ??
                      false
                    }
                    onClose={() => setSelected('')}
                    onChanged={() => void dbs.refetch()}
                  />
                )}
              </>
            ) : (
              <RedisBrowser
                key={current!.id}
                prefix={prefix}
                toolbar={redisToolbar}
                editing={redisEditor}
                onEdit={setRedisEditor}
              />
            )}
          </Card>
        </div>
      )}
      {rootOpen && current && (
        <RootPasswordModal
          base={prefix}
          onClose={() => setRootOpen(false)}
          onSaved={refresh}
        />
      )}
      {remoteOpen && (
        <RemoteDatabases
          label={label}
          rows={connections.data?.filter((row) => !row.local) ?? []}
          onClose={() => setRemoteOpen(false)}
          onAdd={() => {
            form.resetFields();
            setEditing({
              port: engine === 'mongodb' ? 27017 : 6379,
              username: '',
              tls: false,
              authSource: 'admin',
            });
          }}
          onEdit={(row) => {
            form.resetFields();
            setEditing(row);
          }}
          onDelete={(row) =>
            modal.confirm({
              title: `移除远程数据库 ${row.name}？`,
              content: '只移除连接，保留服务器数据。',
              onOk: () =>
                run(async () => {
                  await request(`${base}/connections/${row.id}`, {
                    method: 'DELETE',
                  });
                  client.setQueryData<Connection[]>(
                    ['nosql-connections', engine],
                    (rows) => rows?.filter((item) => item.id !== row.id),
                  );
                  if (current?.id === row.id) setSid(undefined);
                }),
            })
          }
        />
      )}
      {deleting &&
        (() => {
          const targetConnId = activeConnId || current?.id;
          const targetConn =
            connections.data?.find((c) => c.id === targetConnId) ?? current;
          const targetRow = dbs.data?.find(
            (row) =>
              row.name === deleting &&
              (row._connectionId || current?.id) === targetConnId,
          );
          return (
            <DeleteDatabaseModal
              name={deleting}
              size={targetRow?.size}
              createdAt={targetRow?.createdAt}
              location={
                targetConn?.local && !targetConn.instanceId
                  ? '本地数据库'
                  : `${targetConn?.name}（${targetConn?.host}:${targetConn?.port}）`
              }
              url={`${activeMongoPrefix}/databases/${encodeURIComponent(deleting)}`}
              recyclable={false}
              onClose={() => setDeleting(undefined)}
              onDeleted={() => {
                const removed = deleting;
                if (targetConnId) {
                  client.setQueryData<MongoDatabase[]>(
                    ['nosql-databases', engine, targetConnId],
                    (rows) => rows?.filter((row) => row.name !== removed),
                  );
                }
                setSelected('');
              }}
            />
          );
        })()}
      <Modal
        open={!!editing}
        title={
          editing?.local
            ? `${label} · 本机认证`
            : `${editing?.id ? '编辑' : '添加'}${label}远程数据库`
        }
        width={LAYOUT.databaseModalWidth}
        onCancel={() => setEditing(undefined)}
        confirmLoading={busy}
        onOk={async () => {
          const values = await form.validateFields();
          const submittedPassword =
            values.password !== undefined && values.password !== ''
              ? values.password
              : editing && !passwordLoaded && editing.hasPassword
                ? undefined
                : '';
          await run(async () => {
            await request(`${base}/connections`, {
              method: 'POST',
              data: {
                ...values,
                username: engine === 'redis' ? '' : (values.username ?? ''),
                name: values.name?.trim() || `${values.host}:${values.port}`,
                tls: editing?.tls ?? false,
                authSource: editing?.authSource ?? 'admin',
                id: editing?.id,
                password: submittedPassword,
              },
            });
            setEditing(undefined);
            await connections.refetch();
            if (engine === 'redis') {
              await client.invalidateQueries({
                queryKey: ['nosql-databases', 'redis'],
              });
              await client.invalidateQueries({ queryKey: ['redis-keys'] });
            }
          });
        }}
        destroyOnHidden
      >
        <Form
          key={editing?.id ?? 'new'}
          form={form}
          preserve={false}
          initialValues={editing}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
        >
          <Form.Item
            name="host"
            label="服务器地址"
            rules={[{ required: true }]}
          >
            <Input disabled={editing?.local} />
          </Form.Item>
          <Form.Item
            name="port"
            label="数据库端口"
            rules={[{ required: true }]}
          >
            <InputNumber min={1} max={65535} disabled={editing?.local} />
          </Form.Item>
          {engine !== 'redis' && (
            <Form.Item name="username" label="管理员名称">
              <Input placeholder="无需认证时留空" />
            </Form.Item>
          )}
          <Form.Item
            name="password"
            label={engine === 'redis' ? '密码' : '管理员密码'}
          >
            <Input.Password
              disabled={passwordLoading}
              placeholder={
                passwordLoading
                  ? '正在读取当前密码…'
                  : editing?.hasPassword && !passwordLoaded
                    ? '留空保留原密码'
                    : '无需认证时留空'
              }
            />
          </Form.Item>
          <Form.Item name="name" label="备注" rules={[{ max: 64 }]}>
            <Input disabled={editing?.local} />
          </Form.Item>
        </Form>
      </Modal>
      <Modal
        open={creating}
        title="添加MongoDB数据库"
        maskClosable={false}
        width={LAYOUT.databaseModalWidth}
        onCancel={() => setCreating(false)}
        confirmLoading={busy}
        onOk={async () => {
          const values = await operation.validateFields();
          await run(async () => {
            await request(`${base}/connections/${values.serverId}/databases`, {
              method: 'POST',
              data: { name: values.name },
            });
            const queryKey = ['nosql-databases', engine, values.serverId];
            await client.cancelQueries({ queryKey, exact: true });
            await client.invalidateQueries({ queryKey, exact: true });
            setSid(values.serverId);
            setSelected('');
            setCreating(false);
          });
        }}
        destroyOnHidden
      >
        <Form
          form={operation}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
        >
          <Form.Item
            name="name"
            label="数据库名称"
            rules={[{ required: true, pattern: /^[a-zA-Z0-9_-]{1,63}$/ }]}
            extra="自动创建同名账号，密码可在数据库列表查看。"
          >
            <Input />
          </Form.Item>
          <Form.Item
            name="serverId"
            label="添加至"
            rules={[{ required: true }]}
          >
            <Select
              options={connections.data?.map((row) => ({
                value: row.id,
                disabled: row.running === false,
                label: `${row.name}（${row.host}:${row.port}）`,
              }))}
            />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
