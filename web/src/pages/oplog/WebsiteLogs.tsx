import { useEffect, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Col,
  DatePicker,
  Empty,
  Input,
  Row,
  Segmented,
  Select,
  Statistic,
  Tabs,
  Typography,
} from 'antd';
import { RotateCw } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import ContentSizedTable from '../../components/ContentSizedTable';
import { request } from '../../api/client';
import { clearSiteLog } from '../../api/website';
import { downloadLogFile, type TextLog } from '../../api/oplog';
import { useRememberedTab } from '../../hooks/useRememberedTab';
import { useAuth } from '../../stores/auth';
import AuditLogs from './AuditLogs';
import SiteLogLocation from '../website/SiteLogLocation';
import WebsiteLogContent from '../website/WebsiteLogContent';
import { formatSize } from '../../utils/format';

type LogFilter = { keyword?: string; startTime?: string; endTime?: string };
export default function WebsiteLogs({
  section,
  onChange,
}: {
  section: string;
  onChange: (tab: string) => void;
}) {
  const [params, setParams] = useSearchParams();
  const selected = params.get('site');
  const sites = useQuery({
    queryKey: ['log-sites'],
    queryFn: () =>
      request<{ id: string; name: string; logDirectory: string }[]>(
        '/api/logs/sites',
      ),
  });
  const { tab: savedSite, loading } = useRememberedTab(
    'oplogSite',
    selected,
    sites.data?.map(({ id }) => id) ?? [],
  );
  const site = loading
    ? undefined
    : sites.data?.find(({ id }) => id === (selected ?? savedSite));
  const id = site?.id;
  useEffect(() => {
    if (!id || selected !== null) return;
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.set('site', id);
        return next;
      },
      { replace: true },
    );
  }, [id, selected, setParams]);
  return (
    <>
      <div className="logs-controls">
        <Typography.Text>网站：</Typography.Text>
        <Select
          aria-label="选择网站"
          showSearch
          optionFilterProp="label"
          placeholder="请选择网站"
          value={id}
          style={{ width: 280, maxWidth: '100%' }}
          options={sites.data?.map((site) => ({
            value: site.id,
            label: site.name,
          }))}
          onChange={(next) =>
            setParams((previous) => {
              const query = new URLSearchParams(previous);
              query.set('site', next);
              return query;
            })
          }
        />
      </div>
      {sites.error && <Alert type="error" message={sites.error.message} />}
      {!id && !sites.isFetching && !loading ? (
        <Empty
          description={
            selected !== null
              ? '链接指定的网站不存在，请重新选择。'
              : '暂无网站'
          }
        />
      ) : (
        site && (
          <SiteLogs
            key={site.id}
            id={site.id}
            directory={site.logDirectory}
            section={section}
            onChange={onChange}
          />
        )
      )}
    </>
  );
}
function SiteLogs({
  id,
  directory,
  section,
  onChange,
}: {
  id: string;
  directory: string;
  section: string;
  onChange: (tab: string) => void;
}) {
  const { modal, message } = App.useApp();
  const timezone = useAuth((state) => state.user?.timezone ?? 'Asia/Shanghai');
  const [logKind, setKind] = useState<'access' | 'error'>('access');
  const kind = section === 'analysis' ? 'access' : logKind;
  const [filters, setFilters] = useState<LogFilter>({});
  const url = `/api/websites/${encodeURIComponent(id)}/logs`;
  const query = useQuery({
    queryKey: ['website-log-view', id, kind, filters],
    queryFn: () =>
      request<TextLog>(url, {
        params: { type: kind, lines: 1000, ...filters },
      }),
    enabled: section === 'logs' || section === 'analysis',
    staleTime: 0,
    refetchOnMount: 'always',
  });
  function clear(all = false) {
    modal.confirm({
      title: all ? '清空此网站的访问和错误日志？' : '清空当前网站日志？',
      content: '仅清空当前站点的日志，删除后无法恢复。',
      okButtonProps: { danger: true },
      onOk: async () => {
        if (all) {
          await clearSiteLog(id, 'access');
          await clearSiteLog(id, 'error');
        } else await clearSiteLog(id, kind);
        void query.refetch();
      },
    });
  }
  const records = (query.data?.content || '').split('\n').flatMap((line) => {
    const match =
      /^(\S+) .*?\[([^\]]+)\] "\S+ (\S+)(?: [^"]*)?" (\d{3}) (\d+|-)/.exec(
        line,
      );
    return match
      ? [
          {
            ip: match[1],
            url: match[3],
            status: match[4],
            bytes: match[5] === '-' ? 0 : Number(match[5]),
          },
        ]
      : [];
  });
  const statuses = new Map<string, number>(),
    addresses = new Map<string, number>(),
    urls = new Map<string, number>();
  for (const record of records) {
    statuses.set(record.status, (statuses.get(record.status) || 0) + 1);
    addresses.set(record.ip, (addresses.get(record.ip) || 0) + 1);
    urls.set(record.url, (urls.get(record.url) || 0) + 1);
  }
  const rank = (values: Map<string, number>) =>
    [...values]
      .map(([name, count]) => ({ name, count }))
      .sort((a, b) => b.count - a.count);
  return (
    <>
      <SiteLogLocation directory={directory} />
      <Tabs
        type="card"
        activeKey={section}
        onChange={onChange}
        items={[
          { key: 'logs', label: '网站日志' },
          { key: 'operations', label: '操作日志' },
          { key: 'analysis', label: 'web日志分析' },
        ]}
      />
      {section === 'operations' ? (
        <AuditLogs key={id} siteId={id} />
      ) : (
        <>
          <div className="logs-toolbar">
            <div className="logs-controls">
              <Button onClick={() => clear()}>清空当前日志</Button>
              <Button
                onClick={() =>
                  void downloadLogFile(`${url}/download`, `${id}-${kind}.log`, {
                    type: kind,
                    ...filters,
                  }).catch((error) => message.error(error.message))
                }
              >
                导出日志
              </Button>
            </div>
            <div className="logs-controls">
              {section === 'logs' && (
                <Segmented
                  value={kind}
                  options={[
                    { value: 'access', label: '运行日志' },
                    { value: 'error', label: '异常日志' },
                  ]}
                  onChange={(value) => setKind(value as 'access' | 'error')}
                />
              )}
              <DatePicker.RangePicker
                showTime
                style={{ maxWidth: '100%' }}
                onChange={(range) =>
                  setFilters((previous) => ({
                    ...previous,
                    startTime: range?.[0]?.tz(timezone, true).toISOString(),
                    endTime: range?.[1]?.tz(timezone, true).toISOString(),
                  }))
                }
              />
              <Input.Search
                className="logs-search"
                placeholder="请输入搜索关键字"
                allowClear
                onSearch={(keyword) =>
                  setFilters((previous) => ({ ...previous, keyword }))
                }
              />
              <Button
                icon={<RotateCw size={16} strokeWidth={1.9} />}
                aria-label="刷新网站日志"
                loading={query.isFetching}
                onClick={() => void query.refetch()}
              />
            </div>
          </div>
          {section === 'logs' ? (
            <>
              {query.error && (
                <Alert type="error" showIcon message={query.error.message} />
              )}
              {query.data && (
                <Typography.Text type="secondary">
                  日志大小 {formatSize(query.data.size)}
                </Typography.Text>
              )}
              <WebsiteLogContent
                key={kind}
                content={query.data?.content}
                loading={query.isFetching}
                truncated={query.data?.truncated}
                dark
                fill
              >
                {!query.isPending && !query.isError && (
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description="暂无日志记录"
                  />
                )}
              </WebsiteLogContent>
            </>
          ) : (
            <>
              {query.error && (
                <Alert type="error" message={query.error.message} />
              )}
              <Typography.Paragraph type="secondary">
                统计当前筛选下最近读取的最多1000行访问日志，不代表全站历史总量。
              </Typography.Paragraph>
              <Row gutter={[24, 16]} style={{ marginBlock: 24 }}>
                <Col xs={12} md={6}>
                  <Statistic title="请求数量" value={records.length} />
                </Col>
                <Col xs={12} md={6}>
                  <Statistic title="独立IP" value={addresses.size} />
                </Col>
                <Col xs={12} md={6}>
                  <Statistic
                    title="异常请求（4xx/5xx）"
                    value={
                      records.filter((row) => Number(row.status) >= 400).length
                    }
                  />
                </Col>
                <Col xs={12} md={6}>
                  <Statistic
                    title="响应流量"
                    value={formatSize(
                      records.reduce((sum, row) => sum + row.bytes, 0),
                    )}
                  />
                </Col>
              </Row>
              <Row gutter={[24, 16]}>
                {[
                  { title: '状态码', data: rank(statuses) },
                  { title: '访问IP', data: rank(addresses) },
                  { title: '请求路径', data: rank(urls) },
                ].map((item) => (
                  <Col xs={24} lg={8} key={item.title}>
                    <ContentSizedTable
                      fillColumn="name"
                      size="small"
                      rowKey="name"
                      dataSource={item.data}
                      loading={query.isFetching}
                      columns={[
                        {
                          title: item.title,
                          dataIndex: 'name',
                          ellipsis: true,
                        },
                        { title: '请求次数', dataIndex: 'count', width: 90 },
                      ]}
                      pagination={{ pageSize: 10, hideOnSinglePage: true }}
                    />
                  </Col>
                ))}
              </Row>
            </>
          )}
        </>
      )}
    </>
  );
}
