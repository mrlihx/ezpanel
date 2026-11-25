import { useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Flex,
  Row,
  Segmented,
  Space,
  Statistic,
  theme,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import * as echarts from 'echarts/core';
import { LineChart } from 'echarts/charts';
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import ReactECharts from 'echarts-for-react/esm/core';
import { websiteStatistics } from '../../api/website';
import ContentSizedTable from '../../components/ContentSizedTable';
import { bytes } from '../dashboard/format';

echarts.use([
  LineChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  CanvasRenderer,
]);

export default function SiteStatistics({ id }: { id: string }) {
  const [days, setDays] = useState(7);
  const { token } = theme.useToken();
  const query = useQuery({
    queryKey: ['website-statistics', id, days],
    queryFn: ({ signal }) => websiteStatistics(id, days, signal),
  });
  const data = query.data;
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Flex justify="space-between" wrap gap={8}>
        <Segmented
          options={[
            { label: '今天', value: 1 },
            { label: '近 7 天', value: 7 },
            { label: '近 30 天', value: 30 },
          ]}
          value={days}
          onChange={setDays}
        />
        <Button loading={query.isFetching} onClick={() => void query.refetch()}>
          刷新统计
        </Button>
      </Flex>
      {query.error && <Alert type="error" message={query.error.message} />}
      {data?.partial && (
        <Alert
          type="warning"
          message="部分访问日志读取不完整，统计已显示可读取的记录。"
        />
      )}
      <Row gutter={[12, 12]}>
        {[
          ['请求数', data?.total.requests],
          ['独立 IP', data?.uniqueIPs],
          ['响应流量', data ? bytes(data.total.bytes) : undefined],
          ['4xx / 5xx', data?.total.errors],
        ].map(([title, value]) => (
          <Col xs={12} xl={6} key={title}>
            <Card size="small" loading={query.isPending}>
              <Statistic title={title} value={value ?? '—'} />
            </Card>
          </Col>
        ))}
      </Row>
      {data && days > 1 && (
        <Card title="访问趋势" size="small">
          <ReactECharts
            echarts={echarts}
            style={{ height: 240 }}
            option={{
              textStyle: { color: token.colorTextSecondary },
              color: [token.colorPrimary, token.colorError],
              grid: { top: 38, right: 18, bottom: 24, left: 48 },
              legend: { textStyle: { color: token.colorText } },
              tooltip: {
                trigger: 'axis',
                backgroundColor: token.colorBgElevated,
                borderColor: token.colorBorderSecondary,
                textStyle: { color: token.colorText },
              },
              xAxis: {
                type: 'category',
                data: data.days.map((day) => day.day.slice(5)),
              },
              yAxis: {
                type: 'value',
                minInterval: 1,
                splitLine: { lineStyle: { color: token.colorBorderSecondary } },
              },
              series: [
                {
                  type: 'line',
                  name: '请求数',
                  data: data.days.map((day) => day.requests),
                },
                {
                  type: 'line',
                  name: '错误数',
                  data: data.days.map((day) => day.errors),
                },
              ],
            }}
          />
        </Card>
      )}
      <Card title="请求路径排行" size="small">
        <ContentSizedTable<{ path: string; requests: number }>
          size="small"
          fillColumn="path"
          rowKey="path"
          pagination={false}
          loading={query.isPending}
          dataSource={data?.topPaths}
          locale={{ emptyText: '暂无访问记录' }}
          columns={[
            { title: '请求路径', dataIndex: 'path' },
            { title: '请求数', dataIndex: 'requests', align: 'right' },
          ]}
        />
      </Card>
    </Space>
  );
}
