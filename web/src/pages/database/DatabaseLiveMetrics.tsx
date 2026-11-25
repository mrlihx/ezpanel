import { useState } from 'react';
import { Card, Col, Row, Statistic, Typography, theme } from 'antd';
import { formatSize } from '../../utils/format';

type Sample = { at: number; values: Record<string, string> };
type Point = { sample: Sample; operations?: number; transactions?: number };

function number(values: Sample['values'], key: string) {
  const raw = values[key];
  if (raw == null) return undefined;
  const n = Number(raw);
  return Number.isFinite(n) ? n : undefined;
}

function intervalRate(
  previous: Sample | undefined,
  current: Sample,
  keys: string[],
) {
  if (!previous) return undefined;
  const seconds = (current.at - previous.at) / 1000;
  const oldUptime = number(previous.values, 'uptime');
  const uptime = number(current.values, 'uptime');
  if (
    seconds <= 0 ||
    seconds > 15 ||
    (oldUptime !== undefined && uptime !== undefined && uptime < oldUptime)
  )
    return undefined;
  let delta = 0;
  for (const key of keys) {
    const before = number(previous.values, key);
    const after = number(current.values, key);
    if (before === undefined || after === undefined || after < before)
      return undefined;
    delta += after - before;
  }
  return delta / seconds;
}

export default function DatabaseLiveMetrics({
  mysql,
  sample,
}: {
  mysql: boolean;
  sample: Sample;
}) {
  const { token } = theme.useToken();
  const [history, setHistory] = useState<Point[]>([]);
  const previous = history.at(-1);
  const current =
    previous?.sample.at === sample.at
      ? previous
      : {
          sample,
          operations: mysql
            ? intervalRate(previous?.sample, sample, ['questions'])
            : number(sample.values, 'instantaneous_ops_per_sec'),
          transactions: mysql
            ? intervalRate(previous?.sample, sample, [
                'com_commit',
                'com_rollback',
              ])
            : undefined,
        };
  if (current !== previous) {
    setHistory([...history.slice(-19), current]);
  }
  const opsHistory = history.flatMap(({ operations }) =>
    operations === undefined ? [] : [operations],
  );
  const maxOps = Math.max(1, ...opsHistory);
  const memory = number(
    sample.values,
    mysql ? 'innodb_buffer_pool_bytes_data' : 'used_memory',
  );
  const active = number(
    sample.values,
    mysql ? 'threads_connected' : 'connected_clients',
  );
  const extra = mysql
    ? current.transactions
    : number(sample.values, 'evicted_keys');
  const label = mysql ? '每秒查询' : '每秒命令';

  return (
    <Row gutter={[8, 8]}>
      <Col xs={12} md={6}>
        <Card size="small" style={{ height: '100%' }}>
          <Statistic
            title={label}
            value={current.operations?.toFixed(2) ?? '—'}
          />
          <div
            role="img"
            aria-label={`最近采样${label}，峰值 ${opsHistory.length ? Math.max(...opsHistory).toFixed(2) : '暂无'}`}
            style={{
              display: 'flex',
              alignItems: 'end',
              gap: 2,
              height: 26,
              marginTop: 6,
            }}
          >
            {opsHistory.map((item, index) => (
              <span
                key={index}
                aria-hidden
                style={{
                  flex: '1 1 0',
                  maxWidth: 9,
                  height: `${Math.max(6, (item / maxOps) * 100)}%`,
                  borderRadius: 2,
                  background: token.colorPrimary,
                  opacity: 0.35 + (0.65 * (index + 1)) / opsHistory.length,
                }}
              />
            ))}
            {!opsHistory.length && (
              <Typography.Text type="secondary">等待采样</Typography.Text>
            )}
          </div>
        </Card>
      </Col>
      <Col xs={12} md={6}>
        <Card size="small" style={{ height: '100%' }}>
          <Statistic
            title={mysql ? '当前连接' : '当前客户端'}
            value={active ?? '—'}
          />
        </Card>
      </Col>
      <Col xs={12} md={6}>
        <Card size="small" style={{ height: '100%' }}>
          <Statistic
            title={mysql ? '缓冲池数据' : '已用内存'}
            value={memory === undefined ? '—' : formatSize(memory)}
          />
        </Card>
      </Col>
      <Col xs={12} md={6}>
        <Card size="small" style={{ height: '100%' }}>
          <Statistic
            title={mysql ? '每秒显式提交与回滚' : '累计键淘汰'}
            value={extra === undefined ? '—' : mysql ? extra.toFixed(2) : extra}
          />
        </Card>
      </Col>
    </Row>
  );
}
