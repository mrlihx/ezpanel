import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Descriptions,
  Form,
  Input,
  InputNumber,
  Segmented,
  Select,
  Skeleton,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  databaseDiagnostics,
  purgeMySQLBinaryLog,
  type BinaryLog,
  type DatabaseMetrics,
} from '../../api/database';
import {
  pgExtensions,
  pgInstallExtension,
  pgUninstallExtension,
  type PGExtension,
} from '../../api/postgres';
import {
  clearMySQLInstanceErrorLog,
  databaseInstanceLog,
  databaseInstanceOptions,
  saveDatabaseInstanceOptions,
  saveDatabaseInstancePort,
  type DatabaseInstance,
} from '../../api/software';
import type { TextLog } from '../../api/oplog';
import { formatSize, formatTime } from '../../utils/format';
import WebsiteLogContent from '../website/WebsiteLogContent';
import DatabaseLiveMetrics from './DatabaseLiveMetrics';
import DatabaseTuningModal from './DatabaseTuningModal';
import SlowQueryAnalysis from './SlowQueryAnalysis';

type Target = { engine: string; id: string };
type Values = Record<string, string>;

import { DatabaseRemoteAccess } from './DatabaseConnectionDetails';

const fill = { width: '100%' };
const appliesOnRestart = '保存后，下次启动或重启实例时生效';

export function DatabasePort({
  engine,
  instance,
}: {
  engine: string;
  instance: DatabaseInstance;
}) {
  const { message } = App.useApp();
  const client = useQueryClient();
  const [port, setPort] = useState<number | null>();
  const current = instance.pendingPort ?? instance.port;
  const save = useMutation({
    mutationFn: (value: number) =>
      saveDatabaseInstancePort(engine, instance.id, value),
    onSuccess: async () => {
      await client.invalidateQueries({
        queryKey: ['database-instance', engine, instance.id],
        exact: true,
      });
      setPort(undefined);
      void message.success(appliesOnRestart);
    },
  });
  const value = port === undefined ? current : port;
  return (
    <ApplicationSettingsForm style={fill}>
      <Form.Item
        label="本机端口"
        extra={
          instance.pendingPort
            ? `待应用端口 ${instance.pendingPort}，当前端口 ${instance.port}`
            : undefined
        }
      >
        <Space wrap>
          <InputNumber
            aria-label="本机端口"
            min={1}
            max={65535}
            precision={0}
            value={value}
            onChange={setPort}
          />
          <Button
            disabled={!value || value === current}
            loading={save.isPending}
            onClick={() => value && save.mutate(value)}
          >
            保存端口
          </Button>
        </Space>
      </Form.Item>
      {(engine !== 'memcached' || instance.remoteAccess) && (
        <Form.Item label="允许远程访问" style={{ marginBottom: 0 }}>
          <DatabaseRemoteAccess engine={engine} instance={instance} />
        </Form.Item>
      )}
    </ApplicationSettingsForm>
  );
}

type Option = { key: string; label: string; hint?: string; min?: number };
const tuning: Record<string, Option[]> = {
  mysql: [
    { key: 'table_open_cache', label: '打开表缓存' },
    { key: 'thread_cache_size', label: '线程缓存', min: 0 },
    { key: 'key_buffer_size', label: 'MyISAM 索引缓冲区', hint: '字节' },
    { key: 'tmp_table_size', label: '内存临时表大小', hint: '字节' },
    {
      key: 'innodb_buffer_pool_size',
      label: 'InnoDB 缓冲池大小',
      hint: '字节',
    },
    { key: 'innodb_log_buffer_size', label: 'InnoDB 日志缓冲区', hint: '字节' },
    { key: 'max_connections', label: '最大连接数' },
    { key: 'sort_buffer_size', label: '排序缓冲区', hint: '每连接，字节' },
    { key: 'read_buffer_size', label: '顺序读缓冲区', hint: '每连接，字节' },
    {
      key: 'read_rnd_buffer_size',
      label: '随机读缓冲区',
      hint: '每连接，字节',
    },
    { key: 'join_buffer_size', label: '联表缓冲区', hint: '每连接，字节' },
    { key: 'thread_stack', label: '线程栈大小', hint: '字节' },
    {
      key: 'binlog_cache_size',
      label: '二进制日志缓存',
      hint: '字节，4096 的倍数',
    },
  ],
  redis: [
    { key: 'timeout', label: '空闲连接超时', hint: '秒，0 表示不限', min: 0 },
    { key: 'maxclients', label: '最大客户端数' },
    { key: 'databases', label: '逻辑数据库数量' },
    { key: 'maxmemory', label: '最大内存', hint: '字节，0 表示不限', min: 0 },
    { key: 'tcp-keepalive', label: 'TCP 保活时间', hint: '秒', min: 0 },
  ],
  memcached: [
    { key: 'm', label: '最大缓存容量', hint: 'MiB' },
    { key: 'c', label: '最大连接数' },
    { key: 't', label: '工作线程数' },
  ],
  mongodb: [
    { key: 'maxIncomingConnections', label: '最大连接数' },
    { key: 'verbosity', label: '日志详细级别', hint: '0–5', min: 0 },
    {
      key: 'slowOpThresholdMs',
      label: '慢查询阈值',
      hint: '毫秒，默认 100',
      min: 0,
    },
  ],
  postgresql: [],
};

type PGOption = {
  key: string;
  label: string;
  unit?: 'MB' | 'GB';
  hint: string;
  min?: number;
  defaultVal: number;
};

const pgTuningOptions: PGOption[] = [
  {
    key: 'shared_buffers',
    label: 'shared_buffers',
    unit: 'MB',
    defaultVal: 128,
    min: 1,
    hint: 'MB，postgresql通过shared_buffers和内核和磁盘打交道，通常设置为实际内存的10%。',
  },
  {
    key: 'work_mem',
    label: 'work_mem',
    unit: 'MB',
    defaultVal: 4,
    min: 1,
    hint: 'MB，增加work_mem有助于提高排序的速度。通常设置为实际RAM的2%-4%。',
  },
  {
    key: 'effective_cache_size',
    label: 'effective_cache_size',
    unit: 'GB',
    defaultVal: 4,
    min: 1,
    hint: 'GB，pgsql能够使用的最大缓存，比如4G的内存，可以设置为3GB。',
  },
  {
    key: 'temp_buffers',
    label: 'temp_buffers',
    unit: 'MB',
    defaultVal: 8,
    min: 1,
    hint: 'MB，设置每个数据库会话使用的临时缓冲区的最大数目，默认是 8 兆字节（8MB）。',
  },
  {
    key: 'max_connections',
    label: 'max_connections',
    defaultVal: 100,
    min: 1,
    hint: '最大连接数',
  },
  {
    key: 'max_prepared_transactions',
    label: 'max_prepared_transactions',
    defaultVal: 0,
    min: 0,
    hint: '设置可以同时处于 prepared 状态的事务的最大数目',
  },
  {
    key: 'max_stack_depth',
    label: 'max_stack_depth',
    unit: 'MB',
    defaultVal: 2,
    min: 1,
    hint: 'MB，指定服务器的执行堆栈的最大安全深度，默认是 2 兆字节（2MB）。',
  },
  {
    key: 'bgwriter_lru_maxpages',
    label: 'bgwriter_lru_maxpages',
    defaultVal: 100,
    min: 0,
    hint: '一个周期最多写多少脏页',
  },
  {
    key: 'max_worker_processes',
    label: 'max_worker_processes',
    defaultVal: 8,
    min: 1,
    hint: '如果要使用worker process，最多可以允许fork 多少个worker进程。',
  },
];

function parsePGUnitNumber(raw: string | undefined, unit?: 'MB' | 'GB') {
  if (!raw) return undefined;
  const trimmed = raw.trim();
  const m = /^([0-9]+)\s*([kmg]b?)?$/i.exec(trimmed);
  if (!m) return undefined;
  const num = Number(m[1]);
  const u = (m[2] ?? '').toLowerCase();
  if (!unit) return num;
  if (unit === 'GB') {
    if (u.startsWith('m')) return Math.max(1, Math.round(num / 1024));
    return num;
  }
  if (unit === 'MB') {
    if (u.startsWith('g')) return num * 1024;
    if (u.startsWith('k')) return Math.max(1, Math.round(num / 1024));
    return num;
  }
  return num;
}
const evictionPolicies = [
  'noeviction',
  'allkeys-lru',
  'allkeys-lfu',
  'allkeys-random',
  'volatile-lru',
  'volatile-lfu',
  'volatile-random',
  'volatile-ttl',
];

function useOptionEditor({ engine, id }: Target) {
  const { message } = App.useApp();
  const client = useQueryClient();
  const key = ['database-instance', engine, id];
  const config = useQuery({
    queryKey: [...key, 'options'],
    queryFn: ({ signal }) => databaseInstanceOptions(engine, id, signal),
  });
  const save = useMutation({
    mutationFn: (values: Values) =>
      saveDatabaseInstanceOptions(engine, id, config.data!.revision, values),
    onSuccess: (result) => {
      client.setQueryData([...key, 'options'], result);
      void client.invalidateQueries({ queryKey: [...key, 'settings'] });
      void message.success(appliesOnRestart);
    },
  });
  return { config, save };
}

function parseConfiguredNumber(raw: string | undefined) {
  if (!raw) return undefined;
  const [, digits, unit] = /^([0-9]+)([kmg]b?)?$/i.exec(raw.trim()) ?? [];
  if (!digits) return undefined;
  return (
    Number(digits) *
    1024 ** (unit ? 'kmg'.indexOf(unit[0].toLowerCase()) + 1 : 0)
  );
}

export function DatabaseOptions({
  engine,
  id,
  instance,
  running,
  onRestart,
}: Target & {
  instance?: DatabaseInstance;
  running: boolean;
  onRestart: () => void;
}) {
  const { modal } = App.useApp();
  const { config, save } = useOptionEditor({ engine, id });
  const live = useQuery({
    queryKey: ['database-instance', engine, id, 'performance'],
    queryFn: ({ signal }) =>
      databaseDiagnostics<DatabaseMetrics>(engine, id, 'performance', signal),
    enabled: running && engine !== 'mongodb' && engine !== 'postgresql',
  });
  const [draft, setDraft] = useState<Values>({});
  if (config.isPending) return <Skeleton active />;
  if (config.error)
    return <Alert type="error" showIcon message={config.error.message} />;
  const variables = live.data?.variables ?? {};
  const defaults: Values =
    engine === 'memcached'
      ? {
          m: String(Number(variables.maxbytes ?? 67108864) / 1048576),
          c: variables.maxconns ?? '1024',
          t: variables.num_threads ?? '4',
        }
      : variables;
  const original = { ...defaults, ...config.data?.values };
  const value = (key: string) => draft[key] ?? original[key];
  const change = (key: string, next: string) =>
    setDraft((previous) => ({ ...previous, [key]: next }));
  const finish = () => {
    const changes = Object.fromEntries(
      Object.entries(draft).filter(
        ([key, val]) =>
          val !== (original[key] ?? '') &&
          (val !== '' || key === 'cacheSizeGB'),
      ),
    );
    if (!Object.keys(changes).length) return;
    const commit = () =>
      save.mutateAsync(changes, { onSuccess: () => setDraft({}) });
    if (changes.authorization === 'disabled') {
      modal.confirm({
        title: '关闭 MongoDB 安全认证？',
        content: '重启后，能连接此实例的客户端可以直接访问数据库。',
        onOk: commit,
      });
    } else {
      void commit().catch(() => undefined);
    }
  };
  return (
    <Space direction="vertical" size="middle" style={fill}>
      {live.error && (
        <Alert type="warning" showIcon message={live.error.message} />
      )}
      {(engine === 'mysql' || engine === 'redis') && (
        <DatabaseTuningModal
          engine={engine}
          id={id}
          name={instance?.name}
          current={original}
          redisUsed={running ? live.data?.metrics?.used_memory : undefined}
          onApply={(values) =>
            save.mutateAsync(values, {
              onSuccess: () =>
                setDraft((draft) =>
                  Object.fromEntries(
                    Object.entries(draft).filter(([key]) => !(key in values)),
                  ),
                ),
            })
          }
        />
      )}
      <ApplicationSettingsForm onFinish={finish}>
        {engine === 'mongodb' && (
          <>
            <Form.Item label="容器监听地址">
              <Typography.Text>{original.bindIp}</Typography.Text>
            </Form.Item>
            <Form.Item label="容器数据目录">
              <Typography.Text>{original.dbPath}</Typography.Text>
            </Form.Item>
          </>
        )}
        {engine === 'memcached' && (
          <Form.Item label="本机监听地址">
            <Typography.Text>127.0.0.1</Typography.Text>
          </Form.Item>
        )}
        {tuning[engine]?.map(({ key, label, hint, min }) => (
          <Form.Item
            key={key}
            label={label}
            htmlFor={key}
            extra={
              engine === 'mysql'
                ? [key, hint].filter(Boolean).join(' · ')
                : hint
            }
          >
            <InputNumber
              id={key}
              min={min ?? 1}
              max={key === 'verbosity' ? 5 : undefined}
              precision={0}
              value={parseConfiguredNumber(value(key)) ?? null}
              onChange={(next) => change(key, next == null ? '' : String(next))}
              style={{ width: 230, maxWidth: '100%' }}
            />
          </Form.Item>
        ))}
        {engine === 'postgresql' && (
          <>
            {pgTuningOptions.map(
              ({ key, label, unit, hint, min, defaultVal }) => (
                <Form.Item key={key} label={label} htmlFor={key} extra={hint}>
                  <InputNumber
                    id={key}
                    min={min ?? 1}
                    precision={0}
                    suffix={unit}
                    value={parsePGUnitNumber(value(key), unit) ?? defaultVal}
                    onChange={(next) =>
                      change(key, next == null ? '' : `${next}${unit ?? ''}`)
                    }
                    style={{ width: 230, maxWidth: '100%' }}
                  />
                </Form.Item>
              ),
            )}
            <Form.Item label="listen_addresses">
              <Select
                aria-label="listen_addresses"
                value={
                  (value('listen_addresses') || '*').replace(
                    /^['"]|['"]$/g,
                    '',
                  ) === 'localhost'
                    ? 'localhost'
                    : '*'
                }
                onChange={(next) => change('listen_addresses', next)}
                options={[
                  { label: '本机 (localhost)', value: 'localhost' },
                  { label: '所有地址 (*)', value: '*' },
                ]}
                style={{ width: 230, maxWidth: '100%' }}
              />
            </Form.Item>
          </>
        )}
        {engine === 'redis' && (
          <Form.Item label="内存淘汰策略">
            <Select
              aria-label="内存淘汰策略"
              value={value('maxmemory-policy')}
              onChange={(next) => change('maxmemory-policy', next)}
              options={evictionPolicies.map((policy) => ({
                label: policy,
                value: policy,
              }))}
              style={{ width: 230, maxWidth: '100%' }}
            />
          </Form.Item>
        )}
        {engine === 'mongodb' && (
          <>
            <Form.Item
              label="WiredTiger 缓存上限"
              extra="单位 GB（支持小数，如 0.5、1、2），留空使用系统默认上限。"
            >
              <InputNumber
                aria-label="WiredTiger 缓存上限"
                min={0.25}
                max={1024}
                step={0.25}
                placeholder="默认"
                value={parseConfiguredNumber(value('cacheSizeGB')) ?? null}
                onChange={(next) =>
                  change('cacheSizeGB', next == null ? '' : String(next))
                }
                style={{ width: 230, maxWidth: '100%' }}
              />
            </Form.Item>
            <Form.Item label="慢查询分析">
              <Select
                aria-label="慢查询分析"
                value={value('profilingMode') || 'off'}
                onChange={(next) => change('profilingMode', next)}
                options={[
                  { label: '关闭 (off)', value: 'off' },
                  { label: '仅记录慢查询 (slowOp)', value: 'slowOp' },
                  { label: '记录全部操作 (all)', value: 'all' },
                ]}
                style={{ width: 230, maxWidth: '100%' }}
              />
            </Form.Item>
            <Form.Item label="安全认证">
              <Switch
                aria-label="安全认证"
                checked={value('authorization') !== 'disabled'}
                onChange={(enabled) =>
                  change('authorization', enabled ? 'enabled' : 'disabled')
                }
              />
            </Form.Item>
          </>
        )}
        <Form.Item className="application-settings-actions">
          <Space wrap>
            <Button
              type="primary"
              htmlType="submit"
              loading={save.isPending}
              disabled={
                !Object.keys(draft).some(
                  (key) => draft[key] !== (original[key] ?? ''),
                )
              }
            >
              保存配置
            </Button>
            {running && <Button onClick={onRestart}>重启实例</Button>}
            <Typography.Text type="secondary">
              {appliesOnRestart}
            </Typography.Text>
          </Space>
        </Form.Item>
      </ApplicationSettingsForm>
    </Space>
  );
}

function ratio(value: number, total: number) {
  return total > 0 ? `${((value / total) * 100).toFixed(2)}%` : '—';
}
const redisMetrics = [
  ['uptime_in_days', '已运行天数'],
  ['tcp_port', '容器监听端口'],
  ['connected_clients', '当前客户端数'],
  ['used_memory_rss_human', '实际占用的系统内存'],
  ['used_memory_human', '当前分配的内存'],
  ['used_memory_peak_human', '历史分配峰值'],
  ['mem_fragmentation_ratio', '内存碎片率'],
  ['total_connections_received', '累计客户端连接'],
  ['total_commands_processed', '累计执行命令'],
  ['instantaneous_ops_per_sec', '每秒命令数'],
  ['keyspace_hits', '键命中次数'],
  ['keyspace_misses', '键未命中次数'],
  ['hit', '键命中率'],
  ['latest_fork_usec', '最近一次 fork 耗时（微秒）'],
];
const memcachedMetrics = [
  ['bind', '本机监听地址'],
  ['host_port', '本机端口'],
  ['limit_maxbytes', '最大缓存容量'],
  ['maxconns', '最大连接数'],
  ['curr_connections', '当前连接数'],
  ['cmd_get', 'GET 请求数'],
  ['get_hits', 'GET 命中次数'],
  ['get_misses', 'GET 未命中次数'],
  ['hit', 'GET 命中率'],
  ['curr_items', '当前缓存条目数'],
  ['evictions', '累计淘汰条目数'],
  ['bytes', '已使用缓存容量'],
  ['bytes_read', '累计接收'],
  ['bytes_written', '累计发送'],
];
function bytes(value: string | undefined) {
  return value == null ? '—' : formatSize(Number(value));
}
export function DatabaseStatus({
  engine,
  id,
  running,
}: Target & { running: boolean }) {
  const realtime = engine === 'mysql' || engine === 'redis';
  const [live, setLive] = useState(true);
  const query = useQuery({
    queryKey: ['database-instance', engine, id, 'status'],
    queryFn: ({ signal }) =>
      databaseDiagnostics<DatabaseMetrics>(engine, id, 'status', signal),
    enabled: running,
    refetchInterval: (observer) =>
      realtime && live && running && !observer.state.error ? 3000 : false,
    refetchOnWindowFocus: realtime && live,
    retry: false,
  });
  const values = query.data?.metrics ?? {};
  const n = (key: string) => Number(values[key] ?? 0);
  const uptime = n('uptime');
  const paired = (key: string, max: string) =>
    `${values[key] ?? '—'} / ${values[max] ?? '—'}`;
  const mysql = engine === 'mysql';
  const display =
    engine === 'redis'
      ? {
          ...values,
          hit: ratio(
            n('keyspace_hits'),
            n('keyspace_hits') + n('keyspace_misses'),
          ),
        }
      : {
          ...values,
          bind: '127.0.0.1',
          maxconns: query.data?.variables?.maxconns,
          hit: ratio(n('get_hits'), n('get_hits') + n('get_misses')),
          limit_maxbytes: bytes(values.limit_maxbytes),
          bytes: bytes(values.bytes),
          bytes_read: bytes(values.bytes_read),
          bytes_written: bytes(values.bytes_written),
        };
  const rows = mysql
    ? [
        [
          '活动 / 峰值连接数',
          paired('threads_connected', 'max_used_connections'),
          'threads_connected / max_used_connections',
        ],
        [
          '线程缓存命中率',
          ratio(n('connections') - n('threads_created'), n('connections')),
          'thread_cache_size',
        ],
        [
          'MyISAM 索引命中率',
          ratio(
            n('key_read_requests') - n('key_reads'),
            n('key_read_requests'),
          ),
          'key_buffer_size',
        ],
        [
          'InnoDB 缓冲池命中率',
          ratio(
            n('innodb_buffer_pool_read_requests') -
              n('innodb_buffer_pool_reads'),
            n('innodb_buffer_pool_read_requests'),
          ),
          'innodb_buffer_pool_size',
        ],
        [
          '磁盘临时表占比',
          ratio(n('created_tmp_disk_tables'), n('created_tmp_tables')),
          'created_tmp_disk_tables / created_tmp_tables',
        ],
        ['已打开的表', values.open_tables, 'open_tables'],
        ['未使用索引扫描次数', values.select_scan, 'select_scan'],
        ['未使用索引的联表次数', values.select_full_join, 'select_full_join'],
        ['排序合并次数', values.sort_merge_passes, 'sort_merge_passes'],
      ]
    : engine === 'postgresql'
      ? [
          ['当前postgresql相关进程数', values.processCount, 'postgres 进程数'],
          ['总连接数', values.connections, 'pg_stat_activity'],
          ['数据库占用空间', values.sizeKB, '所有数据库总计'],
          [
            '启动时间',
            values.startTime ? formatTime(values.startTime) : '—',
            'pg_postmaster_start_time',
          ],
          ['数据库进程号pid', values.pid, '主进程 PID'],
          ['数据库占用内存', values.VmRSS, 'VmRSS'],
          ['表进程已经锁住的物理内存的大小', values.VmLck, 'VmLck'],
          ['数据库分配到物理内存的峰值', values.VmHWM, 'VmHWM'],
          ['进程数据段的大小', values.VmData, 'VmData'],
          ['进程堆栈段的大小', values.VmStk, 'VmStk'],
          ['进程代码的大小', values.VmExe, 'VmExe'],
          ['进程所使用LIB库的大小', values.VmLib, 'VmLib'],
          ['进程占用Swap的大小', values.VmSwap, 'VmSwap'],
          ['占用的页表的大小', values.VmPTE, 'VmPTE'],
          ['当前待处理信号的个数', values.SigQ, 'SigQ'],
        ]
      : (engine === 'redis' ? redisMetrics : memcachedMetrics).map(
          ([key, label]) => [
            key.replace(/_human$/, ''),
            display[key as keyof typeof display],
            label,
          ],
        );
  return (
    <Space direction="vertical" style={fill}>
      <Space wrap>
        <Button
          disabled={!running}
          loading={query.isFetching}
          onClick={() => void query.refetch()}
        >
          刷新状态
        </Button>
        {realtime && running && (
          <label>
            <Space>
              <Switch
                size="small"
                aria-label="实时刷新"
                checked={live}
                onChange={(active) => {
                  setLive(active);
                  if (active) void query.refetch();
                }}
              />
              实时刷新
            </Space>
          </label>
        )}
        {running && query.data?.sampledAt && (
          <Typography.Text type="secondary">
            最近采样 {new Date(query.data.sampledAt).toLocaleTimeString()}
          </Typography.Text>
        )}
      </Space>
      {!running && (
        <Alert type="info" showIcon message="启动实例后可查看负载指标。" />
      )}
      {query.error && (
        <Alert type="error" showIcon message={query.error.message} />
      )}
      {running && query.isPending && <Skeleton active />}
      {running && query.data && (
        <>
          {realtime && query.data.sampledAt && (
            <DatabaseLiveMetrics
              key={`${engine}:${id}`}
              mysql={mysql}
              sample={{ at: query.data.sampledAt, values }}
            />
          )}
          {mysql && (
            <Descriptions
              bordered
              size="small"
              column={{ xs: 1, sm: 2 }}
              items={[
                {
                  label: '启动时间',
                  children: uptime
                    ? formatTime(
                        new Date(
                          query.dataUpdatedAt - uptime * 1000,
                        ).toISOString(),
                      )
                    : '—',
                },
                { label: '总连接次数', children: values.connections ?? '—' },
                { label: '发送', children: bytes(values.bytes_sent) },
                { label: '接收', children: bytes(values.bytes_received) },
                {
                  label: '平均每秒查询',
                  children: uptime ? (n('questions') / uptime).toFixed(2) : '—',
                },
                {
                  label: '平均每秒显式提交与回滚',
                  children: uptime
                    ? ((n('com_commit') + n('com_rollback')) / uptime).toFixed(
                        2,
                      )
                    : '—',
                },
                { label: '二进制日志', children: query.data.binlogFile || '—' },
                {
                  label: '写入位置',
                  children: query.data.binlogPosition || '—',
                },
              ]}
            />
          )}
          <Table
            size="small"
            rowKey="key"
            scroll={{ x: 'max-content' }}
            pagination={false}
            dataSource={rows.map(([key, value, description]) => ({
              key,
              value,
              description,
            }))}
            columns={[
              { title: '字段', dataIndex: 'key' },
              {
                title: '当前值',
                dataIndex: 'value',
                render: (val: string | undefined) => val ?? '—',
              },
              { title: '说明', dataIndex: 'description' },
            ]}
          />
        </>
      )}
    </Space>
  );
}

type SlowLog = { enabled: boolean; seconds: string; log: TextLog };
type Binlogs = { enabled: boolean; files: BinaryLog[] };
type LogKind = 'error' | 'slow' | 'binlog' | 'log';

export function DatabaseLogs({ engine, id, kind }: Target & { kind: LogKind }) {
  const { modal, message } = App.useApp();
  const [slowView, setSlowView] = useState<'log' | 'analysis'>('log');
  const analyzing = kind === 'slow' && slowView === 'analysis';
  const query = useQuery({
    queryKey: ['database-instance', engine, id, kind],
    queryFn: async ({
      signal,
    }): Promise<{ log?: TextLog; slow?: SlowLog; binlog?: Binlogs }> => {
      if (kind === 'slow') {
        const slow = await databaseDiagnostics<SlowLog>(
          engine,
          id,
          kind,
          signal,
        );
        return { log: slow.log, slow };
      }
      if (kind === 'binlog') {
        return {
          binlog: await databaseDiagnostics<Binlogs>(engine, id, kind, signal),
        };
      }
      const log =
        engine === 'redis'
          ? await databaseDiagnostics<TextLog>(engine, id, kind, signal)
          : await databaseInstanceLog(engine, id, signal);
      return { log };
    },
    enabled: !analyzing,
  });
  const clear = useMutation({
    mutationFn: () => clearMySQLInstanceErrorLog(id),
    onSuccess: () => {
      void query.refetch();
      void message.success('错误日志已清空');
    },
  });
  const purge = useMutation({
    mutationFn: (name: string) => purgeMySQLBinaryLog(id, name),
    onSuccess: () => {
      void query.refetch();
      void message.success('已清理选中的历史日志');
    },
  });
  const { log, slow, binlog } = query.data ?? {};
  const slowTabs = kind === 'slow' && engine === 'mysql' && (
    <Segmented
      aria-label="慢日志视图"
      options={[
        { value: 'log', label: '原始日志' },
        { value: 'analysis', label: '聚合分析' },
      ]}
      value={slowView}
      onChange={setSlowView}
    />
  );
  if (analyzing) {
    return (
      <Space direction="vertical" size="middle" style={fill}>
        {slowTabs}
        <SlowQueryAnalysis id={id} />
      </Space>
    );
  }
  return (
    <Space direction="vertical" size="middle" style={fill}>
      {slowTabs}
      <Space wrap>
        <Button loading={query.isFetching} onClick={() => void query.refetch()}>
          刷新日志
        </Button>
        {kind === 'error' && log?.clearable && (
          <Button
            danger
            loading={clear.isPending}
            onClick={() =>
              modal.confirm({
                title: '清空该实例的错误日志？',
                onOk: () => clear.mutateAsync(),
              })
            }
          >
            清空日志
          </Button>
        )}
        {log?.path && (
          <Typography.Text type="secondary">{log.path}</Typography.Text>
        )}
      </Space>
      {query.error && (
        <Alert type="error" showIcon message={query.error.message} />
      )}
      {query.isPending && <Skeleton active />}
      {slow && engine === 'mysql' && (
        <MySQLLogSettings
          engine={engine}
          id={id}
          kind="slow"
          enabled={slow.enabled}
          seconds={slow.seconds}
        />
      )}
      {slow && engine === 'postgresql' && (
        <PostgresSlowLogSettings
          id={id}
          enabled={slow.enabled}
          duration={slow.seconds}
        />
      )}
      {binlog && (
        <>
          <MySQLLogSettings
            engine={engine}
            id={id}
            kind="binlog"
            enabled={binlog.enabled}
          />
          <Typography.Text type="secondary">
            日志总大小{' '}
            {formatSize(
              binlog.files.reduce((total, file) => total + file.size, 0),
            )}
          </Typography.Text>
          <Table
            rowKey="name"
            size="small"
            scroll={{ x: 'max-content' }}
            pagination={false}
            dataSource={binlog.files}
            locale={{ emptyText: '暂无二进制日志' }}
            columns={[
              { title: '文件名', dataIndex: 'name' },
              {
                title: '大小',
                dataIndex: 'size',
                render: (size: number) => formatSize(size),
              },
              {
                title: '最后修改时间',
                dataIndex: 'updatedAt',
                render: (time?: string) => (time ? formatTime(time) : '—'),
              },
              {
                title: '操作',
                align: 'right',
                render: (_, file, index) =>
                  index >= binlog.files.length - 1 ? (
                    <Typography.Text type="secondary">写入中</Typography.Text>
                  ) : (
                    <Button
                      type="link"
                      danger
                      size="small"
                      disabled={purge.isPending}
                      onClick={() =>
                        modal.confirm({
                          title: `清理至 ${file.name}？`,
                          content:
                            '会清理该文件以及更早的二进制日志。请先确认复制节点和恢复备份已不再使用这些文件。',
                          okButtonProps: { danger: true },
                          onOk: () => purge.mutateAsync(file.name),
                        })
                      }
                    >
                      清理至此
                    </Button>
                  ),
              },
            ]}
          />
        </>
      )}
      {log && (
        <>
          {log.truncated && (
            <Typography.Text type="secondary">
              仅显示最近一段日志。
            </Typography.Text>
          )}
          <WebsiteLogContent
            content={log.content}
            loading={query.isFetching}
            dark
            height={
              kind === 'slow'
                ? 'max(260px, calc(72dvh - 300px))'
                : 'max(380px, calc(72dvh - 130px))'
            }
          >
            {slow && !slow.enabled ? '慢日志尚未启用' : '暂无日志'}
          </WebsiteLogContent>
        </>
      )}
    </Space>
  );
}

function MySQLLogSettings({
  engine,
  id,
  kind,
  enabled,
  seconds,
}: Target & {
  kind: 'slow' | 'binlog';
  enabled: boolean;
  seconds?: string;
}) {
  const { config, save } = useOptionEditor({ engine, id });
  const [draft, setDraft] = useState<boolean>();
  const [time, setTime] = useState<number | null>();
  if (config.isPending) return <Skeleton active paragraph={false} />;
  if (config.error)
    return <Alert type="error" showIcon message={config.error.message} />;
  const key = kind === 'slow' ? 'slow_query_log' : 'binlog';
  const values = config.data?.values ?? {};
  const stored =
    values[key] === undefined
      ? enabled
      : ['on', 'true', '1', ''].includes(values[key].toLowerCase());
  const currentTime = Number(values.long_query_time ?? seconds ?? 10);
  const commit = () => {
    const changes: Values = {};
    if (draft !== undefined && draft !== stored)
      changes[key] = draft ? '1' : '0';
    if (kind === 'slow' && time != null && time !== currentTime)
      changes.long_query_time = String(time);
    save.mutate(changes, {
      onSuccess: () => {
        setDraft(undefined);
        setTime(undefined);
      },
    });
  };
  const label = kind === 'slow' ? '慢查询日志' : '二进制日志';
  return (
    <ApplicationSettingsForm style={fill}>
      <Form.Item
        label={label}
        extra={`当前${enabled ? '已启用' : '已停用'}，${appliesOnRestart}`}
      >
        <Switch
          aria-label={label}
          checked={draft ?? stored}
          onChange={setDraft}
        />
      </Form.Item>
      {kind === 'slow' && (
        <Form.Item label="慢查询阈值">
          <InputNumber
            aria-label="慢查询阈值"
            min={0}
            max={31536000}
            suffix="秒"
            value={time === undefined ? currentTime : time}
            onChange={setTime}
          />
        </Form.Item>
      )}
      <Form.Item className="application-settings-actions">
        <Button
          loading={save.isPending}
          onClick={commit}
          disabled={
            (draft === undefined || draft === stored) &&
            (time == null || time === currentTime)
          }
        >
          保存
        </Button>
      </Form.Item>
    </ApplicationSettingsForm>
  );
}

function PostgresSlowLogSettings({
  id,
  enabled,
  duration,
}: {
  id: string;
  enabled: boolean;
  duration?: string;
}) {
  const { config, save } = useOptionEditor({ engine: 'postgresql', id });
  const raw = config.data?.values.log_min_duration_statement;
  const parsed = raw !== undefined ? Number(raw) : enabled ? 1000 : -1;
  const currentEnabled = Number.isFinite(parsed) && parsed >= 0;
  const currentMs = currentEnabled ? parsed : 1000;
  const [draftEnabled, setDraftEnabled] = useState<boolean>();
  const [draftMs, setDraftMs] = useState<number | null>();

  if (config.isPending || config.error) return null;

  const activeEnabled = draftEnabled ?? currentEnabled;
  const activeMs = draftMs ?? currentMs;
  const dirty =
    (draftEnabled !== undefined && draftEnabled !== currentEnabled) ||
    (activeEnabled && draftMs != null && draftMs !== currentMs);

  const commit = () => {
    const val = activeEnabled
      ? String(Math.max(0, Math.round(activeMs)))
      : '-1';
    void save.mutateAsync(
      { log_min_duration_statement: val },
      {
        onSuccess: () => {
          setDraftEnabled(undefined);
          setDraftMs(undefined);
        },
      },
    );
  };

  return (
    <ApplicationSettingsForm style={fill}>
      <Form.Item
        label="慢查询日志"
        extra={`当前${enabled ? `已启用（${duration || '0ms'}）` : '已停用'}，${appliesOnRestart}`}
      >
        <Switch
          aria-label="慢查询日志"
          checked={activeEnabled}
          onChange={setDraftEnabled}
        />
      </Form.Item>
      {activeEnabled && (
        <Form.Item label="慢查询阈值">
          <InputNumber
            aria-label="慢查询阈值"
            min={0}
            max={3600000}
            precision={0}
            suffix="ms"
            value={activeMs}
            onChange={setDraftMs}
          />
        </Form.Item>
      )}
      <Form.Item className="application-settings-actions">
        <Button loading={save.isPending} onClick={commit} disabled={!dirty}>
          保存
        </Button>
      </Form.Item>
    </ApplicationSettingsForm>
  );
}

export function PostgresExtensions({
  id,
  running,
}: {
  id: string;
  running: boolean;
}) {
  const { message, modal } = App.useApp();
  const [dbName, setDbName] = useState('postgres');
  const [keyword, setKeyword] = useState('');

  const query = useQuery({
    queryKey: ['database-instance', 'postgresql', id, 'extensions', dbName],
    queryFn: ({ signal }) => pgExtensions(id, dbName, signal),
    enabled: running,
  });

  const install = useMutation({
    mutationFn: (name: string) => pgInstallExtension(id, dbName, name),
    onSuccess: (_data, name) => {
      void query.refetch();
      void message.success(`扩展 ${name} 已安装`);
    },
  });

  const uninstall = useMutation({
    mutationFn: (name: string) => pgUninstallExtension(id, dbName, name),
    onSuccess: (_data, name) => {
      void query.refetch();
      void message.success(`扩展 ${name} 已卸载`);
    },
  });

  const list = (query.data?.list ?? []).filter((item) => {
    const q = keyword.trim().toLowerCase();
    if (!q) return true;
    return (
      item.name.toLowerCase().includes(q) ||
      item.comment.toLowerCase().includes(q)
    );
  });

  return (
    <Space direction="vertical" size="middle" style={fill}>
      {!running && (
        <Alert
          type="info"
          showIcon
          message="启动实例后可管理 PostgreSQL 扩展。"
        />
      )}
      {query.error && (
        <Alert type="error" showIcon message={query.error.message} />
      )}
      <Space wrap>
        <Select
          aria-label="目标数据库"
          style={{ width: 200 }}
          value={dbName}
          disabled={!running}
          options={(query.data?.databases ?? ['postgres']).map((db) => ({
            value: db,
            label: db,
          }))}
          onChange={setDbName}
        />
        <Input.Search
          allowClear
          placeholder="搜索扩展名称或说明"
          onSearch={setKeyword}
          onChange={(e) => setKeyword(e.target.value)}
          style={{ width: 240, maxWidth: '100%' }}
        />
        <Button
          disabled={!running}
          loading={query.isFetching}
          onClick={() => void query.refetch()}
        >
          刷新
        </Button>
      </Space>
      {running && (
        <Table<PGExtension>
          size="small"
          rowKey="name"
          loading={query.isFetching}
          dataSource={list}
          scroll={{ x: 640, y: 'calc(72dvh - 210px)' }}
          pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 个扩展` }}
          columns={[
            {
              title: '扩展名称',
              dataIndex: 'name',
              width: 170,
              ellipsis: true,
              render: (v: string) => (
                <Typography.Text strong>{v}</Typography.Text>
              ),
            },
            {
              title: '版本',
              width: 90,
              render: (_, row) =>
                row.installedVersion || row.defaultVersion || '—',
            },
            {
              title: '状态',
              width: 85,
              render: (_, row) =>
                row.installedVersion ? (
                  <Tag color="success">已安装</Tag>
                ) : (
                  <Tag>未安装</Tag>
                ),
            },
            {
              title: '说明',
              dataIndex: 'comment',
              width: 240,
              ellipsis: true,
            },
            {
              title: '操作',
              width: 80,
              fixed: 'right',
              render: (_, row) =>
                row.installedVersion ? (
                  <Button
                    type="link"
                    danger
                    size="small"
                    style={{ padding: 0 }}
                    loading={uninstall.isPending}
                    onClick={() =>
                      modal.confirm({
                        title: `卸载扩展 ${row.name}？`,
                        content: `将从数据库 ${dbName} 中移除该扩展。`,
                        okButtonProps: { danger: true },
                        onOk: () => uninstall.mutateAsync(row.name),
                      })
                    }
                  >
                    卸载
                  </Button>
                ) : (
                  <Button
                    type="link"
                    size="small"
                    style={{ padding: 0 }}
                    loading={install.isPending}
                    onClick={() => install.mutate(row.name)}
                  >
                    安装
                  </Button>
                ),
            },
          ]}
        />
      )}
    </Space>
  );
}
