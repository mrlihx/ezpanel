import { useState } from 'react';
import {
  Alert,
  Button,
  Descriptions,
  Modal,
  Segmented,
  Skeleton,
  Space,
  Table,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import { databaseDiagnostics, type DatabaseTuning } from '../../api/database';
import { formatSize } from '../../utils/format';

export default function DatabaseTuningModal({
  engine,
  id,
  name,
  current,
  redisUsed,
  onApply,
}: {
  engine: string;
  id: string;
  name?: string;
  current: Record<string, string>;
  redisUsed?: string;
  onApply: (values: Record<string, string>) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] =
    useState<DatabaseTuning['profiles'][number]['id']>('conservative');
  const [saving, setSaving] = useState(false);
  const query = useQuery({
    queryKey: ['database-instance', engine, id, 'tuning'],
    queryFn: ({ signal }) =>
      databaseDiagnostics<DatabaseTuning>(engine, id, 'tuning', signal),
    enabled: open,
    staleTime: 0,
    retry: false,
  });
  const profiles = query.data?.profiles ?? [];
  const profile =
    profiles.find((entry) => entry.id === selected) ?? profiles[0];
  const values = profile?.values ?? {};
  const used = Number(redisUsed);
  const redis = engine === 'redis';
  const redisUnavailable = redis && (!redisUsed || !Number.isFinite(used));
  const redisLowLimit = redis && Number(values.maxmemory) < used * 1.2;
  const error = redisUnavailable
    ? '启动 Redis 并取得当前内存用量后，才能应用模板。'
    : redisLowLimit
      ? '建议上限低于当前内存用量与预留空间，请保留现有配置或手动调整。'
      : undefined;
  const changes = Object.fromEntries(
    Object.entries(values).filter(([key, next]) => current[key] !== next),
  );

  async function apply() {
    setSaving(true);
    try {
      await onApply(changes);
      setOpen(false);
    } catch {
      // 请求错误由统一 API 提示。
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <Button onClick={() => setOpen(true)}>一键调优</Button>
      <Modal
        open={open}
        title={`性能调优 · ${name ?? id}`}
        okText="应用模板并保存"
        confirmLoading={saving}
        okButtonProps={{
          disabled:
            query.isFetching ||
            !!query.error ||
            !!error ||
            !Object.keys(changes).length,
        }}
        onOk={() => void apply()}
        onCancel={() => setOpen(false)}
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          {query.isPending && <Skeleton active />}
          {query.error && (
            <Alert
              type="error"
              showIcon
              message={query.error.message}
              action={
                <Button size="small" onClick={() => void query.refetch()}>
                  重试
                </Button>
              }
            />
          )}
          {query.data && (
            <>
              <Descriptions
                size="small"
                column={1}
                items={[
                  {
                    label: '宿主机内存',
                    children: formatSize(query.data.hostMemory),
                  },
                  {
                    label: '当前可用内存',
                    children: formatSize(query.data.available),
                  },
                  {
                    label: '容器内存限额',
                    children: query.data.instanceLimit
                      ? formatSize(query.data.instanceLimit)
                      : '未单独设置',
                  },
                  ...(redis && !redisUnavailable
                    ? [{ label: 'Redis 当前使用', children: formatSize(used) }]
                    : []),
                ]}
              />
              {profiles.length ? (
                <>
                  <Segmented
                    aria-label="调优模板"
                    value={profile.id}
                    options={profiles.map(({ id }) => ({
                      label: id === 'conservative' ? '保守' : '均衡',
                      value: id,
                    }))}
                    onChange={setSelected}
                  />
                  <Table
                    size="small"
                    rowKey="key"
                    pagination={false}
                    dataSource={Object.entries(values).map(([key, next]) => ({
                      key,
                      next,
                    }))}
                    columns={[
                      {
                        title: '参数',
                        dataIndex: 'key',
                        render: (key: string) => <code>{key}</code>,
                      },
                      {
                        title: '当前配置',
                        render: (_, row) => {
                          const raw = current[row.key];
                          if (raw === undefined) return '—';
                          return /^\d+$/.test(raw)
                            ? formatSize(Number(raw))
                            : raw;
                        },
                      },
                      {
                        title: '建议',
                        dataIndex: 'next',
                        render: (next: string) => formatSize(Number(next)),
                      },
                    ]}
                    scroll={{ x: 420 }}
                  />
                  {error ? (
                    <Alert type="warning" showIcon message={error} />
                  ) : (
                    <Alert
                      type="info"
                      showIcon
                      message={`保存后在下次启动或重启此实例时应用。${redis ? '现有内存淘汰策略会保留。' : ''}`}
                    />
                  )}
                </>
              ) : (
                <Alert
                  type="warning"
                  showIcon
                  message="当前可用内存不足以生成调优建议。"
                />
              )}
            </>
          )}
        </Space>
      </Modal>
    </>
  );
}
