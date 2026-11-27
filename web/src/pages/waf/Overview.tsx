import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Badge,
  Button,
  Card,
  Empty,
  Flex,
  Segmented,
  Skeleton,
  Space,
  Statistic,
  Table,
  Tag,
  Typography,
  theme,
} from 'antd';
import * as echarts from 'echarts/core';
import { LineChart, PieChart } from 'echarts/charts';
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import ReactECharts from 'echarts-for-react/esm/core';
import dayjs from 'dayjs';
import { createWAFBan, getWAFOverview, syncWAFEngine } from '../../api/waf';
import { CHART_COLORS, LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';

echarts.use([
  LineChart,
  PieChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  CanvasRenderer,
]);

export default function Overview() {
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const queryClient = useQueryClient();
  const [range, setRange] = useState<'24h' | '7d'>('24h');

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['waf-overview', range],
    queryFn: () => getWAFOverview(range),
    refetchInterval: 15000,
  });

  const syncMutation = useMutation({
    mutationFn: syncWAFEngine,
    onSuccess: () => {
      message.success('WAF 引擎已同步并重载');
      void queryClient.invalidateQueries({ queryKey: ['waf-overview'] });
    },
  });

  const quickBanMutation = useMutation({
    mutationFn: (ip: string) =>
      createWAFBan({
        ip,
        reason: '从 WAF 概览 Top 攻击源手动封禁',
        duration: 86400,
      }),
    onSuccess: () => {
      message.success('已封禁该 IP（24 小时）');
      void queryClient.invalidateQueries({ queryKey: ['waf-overview'] });
      void queryClient.invalidateQueries({ queryKey: ['waf-bans'] });
    },
  });

  if (isError) {
    return (
      <Alert
        type="error"
        showIcon
        message="加载 WAF 概览失败"
        description={error instanceof Error ? error.message : '请稍后重试'}
        action={
          <Button size="small" onClick={() => void refetch()}>
            重试
          </Button>
        }
      />
    );
  }

  if (isLoading || !data) {
    return <Skeleton active />;
  }

  const tooltipStyle = {
    backgroundColor: token.colorBgElevated,
    borderColor: token.colorBorderSecondary,
    textStyle: { color: token.colorText },
  };

  const trendOption = {
    tooltip: { trigger: 'axis', ...tooltipStyle },
    legend: {
      top: 0,
      textStyle: { color: token.colorTextSecondary },
    },
    grid: { left: 40, right: 16, top: 36, bottom: 28 },
    xAxis: {
      type: 'category',
      data: data.trend.map((p) =>
        dayjs.unix(p.ts).format(range === '24h' ? 'HH:mm' : 'MM-DD HH:mm'),
      ),
      axisLabel: { color: token.colorTextTertiary },
      axisLine: { lineStyle: { color: token.colorBorderSecondary } },
    },
    yAxis: {
      type: 'value',
      minInterval: 1,
      axisLabel: { color: token.colorTextTertiary },
      splitLine: { lineStyle: { color: token.colorBorderSecondary } },
    },
    series: [
      {
        name: '拦截',
        type: 'line',
        smooth: true,
        showSymbol: false,
        itemStyle: { color: token.colorError },
        areaStyle: { opacity: 0.14 },
        data: data.trend.map((p) => p.blocked),
      },
      {
        name: '观察记录',
        type: 'line',
        smooth: true,
        showSymbol: false,
        itemStyle: { color: token.colorWarning },
        areaStyle: { opacity: 0.1 },
        data: data.trend.map((p) => p.observed),
      },
    ],
  };

  const pieOption = {
    tooltip: { trigger: 'item' },
    color: CHART_COLORS,
    series: [
      {
        type: 'pie',
        radius: ['46%', '72%'],
        itemStyle: {
          borderRadius: token.borderRadiusSM,
          borderColor: token.colorBgContainer,
          borderWidth: 2,
        },
        label: { color: token.colorTextSecondary },
        data: data.ruleGroups.map((g) => ({ name: g.label, value: g.count })),
      },
    ],
  };

  return (
    <Flex vertical gap={SPACING.lg}>
      <Card>
        <div className="waf-status-bar">
          <Space wrap size={SPACING.md}>
            <Badge
              status={
                data.enabled && data.engine.engineMounted
                  ? 'processing'
                  : 'default'
              }
              text={
                <Typography.Text strong>
                  全局 WAF：{data.enabled ? '已开启' : '未开启'}
                </Typography.Text>
              }
            />
            <Tag color={data.mode === 'block' ? 'red' : 'orange'}>
              默认模式：{data.mode === 'block' ? '拦截' : '仅观察'}
            </Tag>
            <Tag
              color={
                data.engine.openRestyRunning
                  ? 'green'
                  : data.engine.openRestyInstalled
                    ? 'orange'
                    : 'default'
              }
            >
              OpenResty：
              {data.engine.openRestyRunning
                ? '运行中'
                : data.engine.openRestyInstalled
                  ? '未运行'
                  : '未安装'}
            </Tag>
            <Tag color={data.engine.engineMounted ? 'blue' : 'default'}>
              Lua 引擎：{data.engine.engineMounted ? '已挂载' : '未挂载'}
            </Tag>
          </Space>
          <Space>
            <Segmented
              value={range}
              onChange={(v) => setRange(v as '24h' | '7d')}
              options={[
                { label: '近 24 小时', value: '24h' },
                { label: '近 7 天', value: '7d' },
              ]}
            />
            <Button
              type="primary"
              loading={syncMutation.isPending}
              onClick={() => syncMutation.mutate()}
            >
              同步并重载引擎
            </Button>
          </Space>
        </div>
      </Card>

      <div className="waf-kpi-grid">
        <Card>
          <Statistic
            title="今日拦截次数"
            value={data.todayBlocked}
            valueStyle={{ color: token.colorError }}
          />
          <Typography.Text type="secondary">
            今日观察记录：{data.todayObserved}
          </Typography.Text>
        </Card>
        <Card>
          <Statistic title="近 7 天拦截" value={data.weekBlocked} />
          <Typography.Text type="secondary">含自动与规则拦截</Typography.Text>
        </Card>
        <Card>
          <Statistic
            title="当前生效封禁 IP"
            value={data.activeBans}
            valueStyle={{ color: token.colorWarning }}
          />
          <Typography.Text type="secondary">实时共享内存生效</Typography.Text>
        </Card>
        <Card>
          <Statistic
            title="受保护站点"
            value={data.protectedSites}
            suffix={`/ ${data.totalSites}`}
            valueStyle={{ color: token.colorSuccess }}
          />
          <Typography.Text type="secondary">支持继承或独立策略</Typography.Text>
        </Card>
      </div>

      <div className="waf-chart-grid">
        <Card title="攻击拦截趋势">
          <ReactECharts
            option={trendOption}
            style={{ height: LAYOUT.chartHeight }}
            notMerge
          />
        </Card>
        <Card title="攻击类型分布">
          {data.ruleGroups.length > 0 ? (
            <ReactECharts
              option={pieOption}
              style={{ height: LAYOUT.chartHeight }}
              notMerge
            />
          ) : (
            <Empty description="暂无拦截记录" />
          )}
        </Card>
      </div>

      <div className="waf-top-grid">
        <Card title="受攻击站点 Top 5">
          <Table
            rowKey="siteId"
            size="small"
            pagination={false}
            dataSource={data.topSites}
            locale={{ emptyText: '暂无数据' }}
            columns={[
              { title: '站点', dataIndex: 'name' },
              {
                title: '命中次数',
                dataIndex: 'count',
                width: 110,
                align: 'right',
                render: (v: number) => <Tag color="red">{v}</Tag>,
              },
            ]}
          />
        </Card>
        <Card title="高频攻击来源 IP Top 10">
          <Table
            rowKey="ip"
            size="small"
            pagination={false}
            dataSource={data.topIPs}
            locale={{ emptyText: '暂无数据' }}
            columns={[
              {
                title: '来源 IP',
                dataIndex: 'ip',
                render: (ip: string) => (
                  <Typography.Text style={{ fontFamily: MONO_FONT }}>
                    {ip}
                  </Typography.Text>
                ),
              },
              {
                title: '归属地',
                dataIndex: 'location',
                render: (v: string) => v || '-',
              },
              {
                title: '次数',
                dataIndex: 'count',
                width: 80,
                align: 'right',
              },
              {
                title: '操作',
                width: 88,
                render: (_, row) => (
                  <Button
                    type="link"
                    size="small"
                    danger
                    loading={quickBanMutation.isPending}
                    onClick={() => quickBanMutation.mutate(row.ip)}
                  >
                    封禁
                  </Button>
                ),
              },
            ]}
          />
        </Card>
      </div>
    </Flex>
  );
}
