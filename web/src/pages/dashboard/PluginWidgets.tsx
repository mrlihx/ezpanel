import { Button, Card, Col, Row, Skeleton, Typography } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { request } from '../../api/client';
import { usePluginList, type Plugin } from '../../api/plugin';
import { SPACING } from '../../styles/tokens';

export default function PluginWidgets() {
  const plugins = usePluginList(true);
  const widgets = plugins.data?.filter(
    (plugin) => plugin.runtimeStatus === 'running' && plugin.dashboard,
  );
  if (!widgets?.length) return null;
  return (
    <Row gutter={[SPACING.lg, SPACING.lg]}>
      {widgets.map((plugin) => (
        <Col xs={24} sm={12} xl={8} key={plugin.id}>
          <PluginWidget plugin={plugin} />
        </Col>
      ))}
    </Row>
  );
}

function PluginWidget({ plugin }: { plugin: Plugin }) {
  const id = encodeURIComponent(plugin.id);
  const summary = useQuery({
    queryKey: ['plugin-widget', plugin.id, plugin.version],
    queryFn: async ({ signal }) => {
      const item = await request<{
        value?: unknown;
        description?: unknown;
      }>(`/plugin/${id}/api/dashboard`, { signal, silentError: true });
      if (!item || typeof item.value !== 'string')
        throw new Error('卡片数据无效');
      return {
        value: item.value.slice(0, 80),
        description:
          typeof item.description === 'string'
            ? item.description.slice(0, 240)
            : '',
      };
    },
    retry: false,
    refetchInterval: 60000,
  });
  return (
    <Card
      size="small"
      title={plugin.dashboard?.title}
      extra={<Link to={`/p/${id}`}>查看</Link>}
    >
      {summary.isPending ? (
        <Skeleton active paragraph={false} />
      ) : summary.isError ? (
        <Button type="link" onClick={() => void summary.refetch()}>
          重新加载
        </Button>
      ) : (
        <>
          <Typography.Title level={4}>{summary.data.value}</Typography.Title>
          {summary.data.description && (
            <Typography.Text type="secondary">
              {summary.data.description}
            </Typography.Text>
          )}
        </>
      )}
    </Card>
  );
}
