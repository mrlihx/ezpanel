import { useState } from 'react';
import { Alert, Button, Flex, Input, Segmented, Tag } from 'antd';
import { useQuery } from '@tanstack/react-query';
import {
  alertChannelName,
  alertLogs,
  alertRules,
  type AlertLog,
  type AlertLogStatus,
} from '../../api/alerts';
import ContentSizedTable from '../../components/ContentSizedTable';
import { SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';

export default function AlertLogs({
  ruleId,
  clearRule,
}: {
  ruleId?: string;
  clearRule: () => void;
}) {
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [keyword, setKeyword] = useState('');
  const [status, setStatus] = useState<AlertLogStatus>();
  const filter = { page, page_size: size, keyword, status, ruleId };
  const logs = useQuery({
    queryKey: ['settings-alerts', 'logs', filter],
    queryFn: () => alertLogs(filter),
  });
  const rules = useQuery({
    queryKey: ['settings-alerts', 'rules'],
    queryFn: alertRules,
    enabled: !!ruleId,
  });
  const ruleName = rules.data?.list.find((row) => row.id === ruleId)?.title;
  return (
    <Flex vertical gap={SPACING.lg}>
      <Flex wrap gap={SPACING.md} justify="space-between">
        <Flex align="center" wrap gap={SPACING.sm}>
          <Button onClick={() => void logs.refetch()} loading={logs.isFetching}>
            刷新日志
          </Button>
          {ruleId && (
            <Tag closable onClose={clearRule}>
              {ruleName ?? '指定任务'}
            </Tag>
          )}
        </Flex>
        <Flex wrap gap={SPACING.sm}>
          <Segmented<AlertLogStatus | 'all'>
            aria-label="发送结果"
            value={status ?? 'all'}
            options={[
              { value: 'all', label: '全部' },
              { value: 'success', label: '发送成功' },
              { value: 'failed', label: '发送失败' },
            ]}
            onChange={(value) => {
              setPage(1);
              setStatus(value === 'all' ? undefined : value);
            }}
          />
          <Input.Search
            aria-label="搜索告警日志"
            placeholder="任务名称、渠道或失败原因"
            allowClear
            style={{ width: 240, maxWidth: '100%' }}
            onSearch={(value) => {
              setPage(1);
              setKeyword(value.trim());
            }}
          />
        </Flex>
      </Flex>
      {logs.error && (
        <Alert type="error" showIcon message={logs.error.message} />
      )}
      <ContentSizedTable<AlertLog>
        fillColumn="error"
        rowKey="id"
        size="middle"
        loading={logs.isFetching}
        dataSource={logs.data?.list}
        locale={{
          emptyText:
            status || keyword ? '没有符合条件的告警日志' : '暂无告警日志',
        }}
        pagination={{
          current: page,
          pageSize: size,
          total: logs.data?.total ?? 0,
          showSizeChanger: true,
          onChange: (nextPage, nextSize) => {
            setPage(nextSize === size ? nextPage : 1);
            setSize(nextSize);
          },
        }}
        columns={[
          { title: '发送时间', dataIndex: 'createdAt', render: formatTime },
          { title: '任务名称', dataIndex: 'title' },
          {
            title: '告警渠道',
            dataIndex: 'channelName',
            render: (_, row) =>
              `${row.channelName}（${alertChannelName(row.kind)}）`,
          },
          {
            title: '发送结果',
            dataIndex: 'success',
            render: (success: boolean) => (
              <Tag color={success ? 'success' : 'error'}>
                {success ? '成功' : '失败'}
              </Tag>
            ),
          },
          {
            title: '失败原因',
            dataIndex: 'error',
            render: (error?: string) => error || '—',
          },
        ]}
      />
    </Flex>
  );
}
