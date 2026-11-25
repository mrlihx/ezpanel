import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Card,
  Descriptions,
  Drawer,
  Input,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import dayjs from 'dayjs';
import {
  allowWAFEvent,
  clearWAFEvents,
  createWAFBan,
  listWAFEvents,
  listWAFSites,
  type WAFEvent,
} from '../../api/waf';
import { LAYOUT, MONO_FONT } from '../../styles/tokens';
import './waf.css';

const GROUP_OPTIONS = [
  { label: '全部分组', value: '' },
  { label: '敏感路径与泄露', value: 'sensitive' },
  { label: 'SQL 注入', value: 'sqli' },
  { label: 'XSS 跨站脚本', value: 'xss' },
  { label: '命令注入与 RCE', value: 'rce' },
  { label: '目录穿越', value: 'traversal' },
  { label: '恶意扫描器', value: 'scanner' },
  { label: '高危脚本与上传', value: 'upload' },
  { label: 'CC 频率限制', value: 'cc' },
  { label: '动态封禁', value: 'ban' },
  { label: 'IP 黑名单', value: 'ip_deny' },
  { label: '自定义规则', value: 'custom' },
];

export default function Events({
  fixedSiteId,
  manageable = true,
}: { fixedSiteId?: string; manageable?: boolean } = {}) {
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [siteFilter, setSiteId] = useState('');
  const siteId = fixedSiteId ?? siteFilter;
  const [ruleGroup, setRuleGroup] = useState('');
  const [action, setAction] = useState('');
  const [ip, setIP] = useState('');
  const [keyword, setKeyword] = useState('');
  const [detail, setDetail] = useState<WAFEvent | null>(null);

  const sitesQuery = useQuery({
    queryKey: ['waf-sites'],
    queryFn: listWAFSites,
    enabled: !fixedSiteId,
  });

  const eventsQuery = useQuery({
    queryKey: [
      'waf-events',
      page,
      pageSize,
      siteId,
      ruleGroup,
      action,
      ip,
      keyword,
    ],
    queryFn: () =>
      listWAFEvents({
        page,
        pageSize,
        siteId: siteId || undefined,
        ruleGroup: ruleGroup || undefined,
        action: action || undefined,
        ip: ip || undefined,
        keyword: keyword || undefined,
      }),
  });

  const allowMutation = useMutation({
    mutationFn: allowWAFEvent,
    onSuccess: () => {
      message.success('已添加误报放行并热更新至 WAF 引擎');
      void queryClient.invalidateQueries({ queryKey: ['waf-exemptions'] });
    },
  });

  const banMutation = useMutation({
    mutationFn: (ev: WAFEvent) =>
      createWAFBan({
        ip: ev.clientIP,
        siteId: fixedSiteId,
        reason: `攻击日志手动封禁（${ev.ruleName || ev.ruleId}）`,
        duration: 86400,
      }),
    onSuccess: () => {
      message.success('已封禁该 IP（24 小时）');
      void queryClient.invalidateQueries({ queryKey: ['waf-bans'] });
      void queryClient.invalidateQueries({ queryKey: ['waf-overview'] });
    },
  });

  const clearMutation = useMutation({
    mutationFn: clearWAFEvents,
    onSuccess: () => {
      message.success('攻击日志已清空');
      setPage(1);
      void queryClient.invalidateQueries({ queryKey: ['waf-events'] });
      void queryClient.invalidateQueries({ queryKey: ['waf-overview'] });
    },
  });

  const siteOptions = [
    { label: '全部站点', value: '' },
    ...(sitesQuery.data?.list ?? []).map((s) => ({
      label: s.name,
      value: s.siteId,
    })),
  ];

  return (
    <Card>
      <div className="waf-toolbar">
        {!fixedSiteId && (
          <Select
            style={{ width: LAYOUT.dashboardNetworkSelectWidth }}
            value={siteId}
            onChange={(v) => {
              setSiteId(v);
              setPage(1);
            }}
            options={siteOptions}
          />
        )}
        <Select
          style={{ width: LAYOUT.dashboardNetworkSelectWidth }}
          value={ruleGroup}
          onChange={(v) => {
            setRuleGroup(v);
            setPage(1);
          }}
          options={GROUP_OPTIONS}
        />
        <Select
          style={{ width: LAYOUT.fileToolbarPrimaryWidth }}
          value={action}
          onChange={(v) => {
            setAction(v);
            setPage(1);
          }}
          options={[
            { label: '全部动作', value: '' },
            { label: '拦截', value: 'block' },
            { label: '观察', value: 'observe' },
            { label: '质询', value: 'challenge' },
          ]}
        />
        <Input
          allowClear
          placeholder="筛选 IP"
          style={{ width: LAYOUT.dashboardNetworkSelectWidth }}
          value={ip}
          onChange={(e) => {
            setIP(e.target.value);
            setPage(1);
          }}
        />
        <Input
          allowClear
          placeholder="搜索 URI / 规则 / 命中片段"
          style={{ width: LAYOUT.dashboardProcessSearchWidth }}
          value={keyword}
          onChange={(e) => {
            setKeyword(e.target.value);
            setPage(1);
          }}
        />
        <div className="waf-toolbar-right">
          <Button onClick={() => void eventsQuery.refetch()}>刷新</Button>
          {!fixedSiteId && (
            <Popconfirm
              title="确认清空全部攻击日志？"
              onConfirm={() => clearMutation.mutate()}
            >
              <Button danger loading={clearMutation.isPending}>
                清空日志
              </Button>
            </Popconfirm>
          )}
        </div>
      </div>

      {eventsQuery.error && (
        <Alert type="error" message={eventsQuery.error.message} />
      )}
      <Table
        rowKey="id"
        loading={eventsQuery.isLoading}
        dataSource={eventsQuery.data?.list ?? []}
        scroll={{ x: 960 }}
        pagination={{
          current: page,
          pageSize,
          total: eventsQuery.data?.total ?? 0,
          showSizeChanger: true,
          onChange: (p, ps) => {
            setPage(p);
            setPageSize(ps);
          },
        }}
        columns={[
          {
            title: '时间',
            dataIndex: 'occurredAt',
            width: 170,
            render: (ts: number) =>
              dayjs.unix(ts).format('YYYY-MM-DD HH:mm:ss'),
          },
          {
            title: '来源 IP',
            dataIndex: 'clientIP',
            width: 190,
            render: (val: string, row) => (
              <Space direction="vertical" size={0}>
                <Typography.Text style={{ fontFamily: MONO_FONT }}>
                  {val}
                </Typography.Text>
                {row.location ? (
                  <Typography.Text type="secondary">
                    {row.location}
                  </Typography.Text>
                ) : null}
              </Space>
            ),
          },
          {
            title: '站点 / 请求',
            render: (_, row) => (
              <Space direction="vertical" size={0}>
                <Typography.Text strong>{row.domain || '-'}</Typography.Text>
                <Typography.Text
                  ellipsis
                  style={{ fontFamily: MONO_FONT, maxWidth: 320 }}
                >
                  {row.method} {row.uri}
                </Typography.Text>
              </Space>
            ),
          },
          {
            title: '命中规则',
            width: 200,
            render: (_, row) => (
              <Space direction="vertical" size={0}>
                <Typography.Text>{row.ruleName || row.ruleId}</Typography.Text>
                <Typography.Text type="secondary">
                  {row.ruleGroup}
                </Typography.Text>
              </Space>
            ),
          },
          {
            title: '动作',
            dataIndex: 'action',
            width: 90,
            render: (act: string) => (
              <Tag
                color={
                  act === 'block'
                    ? 'red'
                    : act === 'challenge'
                      ? 'blue'
                      : 'orange'
                }
              >
                {act === 'block'
                  ? '拦截'
                  : act === 'challenge'
                    ? '质询'
                    : '观察'}
              </Tag>
            ),
          },
          {
            title: '操作',
            width: 190,
            fixed: 'right',
            render: (_, row) => (
              <Space size={4}>
                <Button type="link" size="small" onClick={() => setDetail(row)}>
                  详情
                </Button>
                <Popconfirm
                  title="将该站点 + URI + 规则加入误报放行？"
                  onConfirm={() => allowMutation.mutate(row.id)}
                >
                  <Button type="link" size="small" disabled={!manageable}>
                    误报加白
                  </Button>
                </Popconfirm>
                <Popconfirm
                  title={`封禁 ${row.clientIP} 24 小时？`}
                  onConfirm={() => banMutation.mutate(row)}
                >
                  <Button
                    type="link"
                    size="small"
                    danger
                    disabled={!manageable}
                  >
                    拉黑
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />

      <Drawer
        title="攻击事件详情"
        width={LAYOUT.drawerWidth}
        open={detail != null}
        onClose={() => setDetail(null)}
        extra={
          detail ? (
            <Space>
              <Button
                loading={allowMutation.isPending}
                onClick={() => allowMutation.mutate(detail.id)}
              >
                误报加白
              </Button>
              <Button
                danger
                loading={banMutation.isPending}
                onClick={() => banMutation.mutate(detail)}
              >
                拉黑 IP
              </Button>
            </Space>
          ) : null
        }
      >
        {detail ? (
          <Descriptions column={1} bordered size="small">
            <Descriptions.Item label="发生时间">
              {dayjs.unix(detail.occurredAt).format('YYYY-MM-DD HH:mm:ss')}
            </Descriptions.Item>
            <Descriptions.Item label="来源 IP">
              <span style={{ fontFamily: MONO_FONT }}>{detail.clientIP}</span>{' '}
              {detail.location ? `(${detail.location})` : ''}
            </Descriptions.Item>
            <Descriptions.Item label="目标域名">
              {detail.domain || '-'}
            </Descriptions.Item>
            <Descriptions.Item label="请求方法与 URI">
              <span style={{ fontFamily: MONO_FONT, wordBreak: 'break-all' }}>
                {detail.method} {detail.uri}
              </span>
            </Descriptions.Item>
            <Descriptions.Item label="查询参数">
              <span style={{ fontFamily: MONO_FONT, wordBreak: 'break-all' }}>
                {detail.queryString || '-'}
              </span>
            </Descriptions.Item>
            <Descriptions.Item label="User-Agent">
              <span style={{ wordBreak: 'break-all' }}>
                {detail.userAgent || '-'}
              </span>
            </Descriptions.Item>
            <Descriptions.Item label="命中规则">
              {detail.ruleName || detail.ruleId} ({detail.ruleGroup} /{' '}
              {detail.ruleId})
            </Descriptions.Item>
            <Descriptions.Item label="命中片段">
              <Typography.Text code style={{ wordBreak: 'break-all' }}>
                {detail.matchedData || '-'}
              </Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label="处置动作 / 状态码">
              {detail.action} / HTTP {detail.statusCode}
            </Descriptions.Item>
          </Descriptions>
        ) : null}
      </Drawer>
    </Card>
  );
}
