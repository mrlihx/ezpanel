import { useQuery } from '@tanstack/react-query';
import { Alert, Skeleton, Tabs } from 'antd';
import { getWAFSitePolicy } from '../../api/waf';
import Events from '../waf/Events';
import { SitePolicyForm } from '../waf/Sites';

export default function SiteWAF({
  id,
  manageable,
}: {
  id: string;
  manageable: boolean;
}) {
  const policy = useQuery({
    queryKey: ['waf-site', id],
    queryFn: () => getWAFSitePolicy(id),
  });
  return (
    <Tabs
      items={[
        {
          key: 'policy',
          label: '防护设置',
          children: policy.error ? (
            <Alert type="error" message={policy.error.message} />
          ) : policy.data ? (
            <SitePolicyForm
              key={id}
              policy={policy.data}
              disabled={!manageable}
            />
          ) : (
            <Skeleton active />
          ),
        },
        {
          key: 'events',
          label: '命中记录',
          children: (
            <Events key={id} fixedSiteId={id} manageable={manageable} />
          ),
        },
      ]}
    />
  );
}
