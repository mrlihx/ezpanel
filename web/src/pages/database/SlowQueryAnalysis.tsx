import {
  Alert,
  Button,
  Descriptions,
  Progress,
  Skeleton,
  Space,
  Table,
  Typography,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import {
  databaseDiagnostics,
  type SlowQueryAnalysis as Analysis,
} from '../../api/database';

const duration = (seconds: number) =>
  seconds >= 1
    ? `${seconds.toFixed(2)} 秒`
    : `${(seconds * 1000).toFixed(2)} 毫秒`;

export default function SlowQueryAnalysis({ id }: { id: string }) {
  const query = useQuery({
    queryKey: ['database-instance', 'mysql', id, 'slow-analysis'],
    queryFn: ({ signal }) =>
      databaseDiagnostics<Analysis>('mysql', id, 'slow-analysis', signal),
    refetchOnWindowFocus: false,
    retry: false,
  });
  const analysis = query.data;

  return (
    <Space direction="vertical" style={{ width: '100%' }}>
      <Button loading={query.isFetching} onClick={() => void query.refetch()}>
        刷新分析
      </Button>
      {query.error && (
        <Alert type="error" showIcon message={query.error.message} />
      )}
      {query.isPending && <Skeleton active />}
      {analysis && (
        <>
          <Descriptions
            size="small"
            column={{ xs: 1, sm: 3 }}
            items={[
              { label: '采样语句', children: analysis.samples },
              { label: '语句类型', children: analysis.groups.length },
              { label: '累计耗时', children: duration(analysis.totalSeconds) },
            ]}
          />
          <Typography.Text type="secondary">
            {analysis.source === 'FILE'
              ? '采样当前日志文件末尾的完整语句。'
              : analysis.source === 'TABLE'
                ? '采样 mysql.slow_log 最近 500 条记录。'
                : '当前实例的慢日志没有配置输出目标。'}
            {analysis.truncated && '采样范围受大小或记录数限制。'}
            {analysis.source !== 'NONE' &&
              '同类语句合并统计，字面量显示为占位符。'}
          </Typography.Text>
          <Table
            size="small"
            rowKey="id"
            scroll={{ x: 730 }}
            dataSource={analysis.groups}
            pagination={{ pageSize: 10, hideOnSinglePage: true }}
            locale={{ emptyText: '采样范围内暂无慢查询' }}
            columns={[
              {
                title: '语句模式',
                dataIndex: 'statement',
                width: 300,
                render: (statement: string) => (
                  <Typography.Text
                    code
                    title={statement}
                    style={{ overflowWrap: 'anywhere', whiteSpace: 'normal' }}
                  >
                    {statement}
                  </Typography.Text>
                ),
              },
              {
                title: '次数',
                dataIndex: 'count',
                sorter: (a, b) => a.count - b.count,
              },
              {
                title: '累计耗时',
                dataIndex: 'totalSeconds',
                defaultSortOrder: 'descend',
                sorter: (a, b) => a.totalSeconds - b.totalSeconds,
                render: (seconds: number) => (
                  <div style={{ minWidth: 82 }}>
                    {duration(seconds)}
                    <Progress
                      percent={
                        analysis.totalSeconds > 0
                          ? (seconds / analysis.totalSeconds) * 100
                          : 0
                      }
                      size="small"
                      showInfo={false}
                    />
                  </div>
                ),
              },
              {
                title: '平均耗时',
                sorter: (a, b) =>
                  a.totalSeconds / a.count - b.totalSeconds / b.count,
                render: (_, row) => duration(row.totalSeconds / row.count),
              },
              {
                title: '最长耗时',
                dataIndex: 'maxSeconds',
                sorter: (a, b) => a.maxSeconds - b.maxSeconds,
                render: duration,
              },
              {
                title: '扫描行数',
                dataIndex: 'rowsExamined',
                sorter: (a, b) => a.rowsExamined - b.rowsExamined,
              },
            ]}
          />
        </>
      )}
    </Space>
  );
}
