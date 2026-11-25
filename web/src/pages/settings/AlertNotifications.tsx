import { Skeleton } from 'antd';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import SectionTabs from '../../components/SectionTabs';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import AlertChannels from './AlertChannels';
import AlertLogs from './AlertLogs';
import AlertRules from './AlertRules';

export default function AlertNotifications({ active }: { active: boolean }) {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const { alertTab } = useParams();
  const { tab, loading, invalid } = useRoutedTab(
    'alerts',
    '/settings/alerts',
    active ? alertTab : undefined,
    ['rules', 'channels', 'logs'],
    { keys: ['alertTab'], tab: active ? params.get('alertTab') : null },
    !active,
  );
  const ruleId = params.get('ruleId') ?? undefined;
  function selectTab(next: string, recordId?: string) {
    const query = recordId
      ? `?${new URLSearchParams({ ruleId: recordId })}`
      : '';
    void navigate(`/settings/alerts/${next}${query}`);
  }
  if (invalid) return <RouteNotFound back="/settings/alerts" />;
  if (loading) return <Skeleton active />;
  return (
    <SectionTabs
      activeKey={tab}
      onChange={(key) => selectTab(key)}
      items={[
        {
          key: 'rules',
          label: '告警列表',
          children: (
            <AlertRules
              active={active && tab === 'rules'}
              openLogs={(rule) => selectTab('logs', rule.id)}
              openChannels={() => selectTab('channels')}
            />
          ),
        },
        { key: 'channels', label: '告警设置', children: <AlertChannels /> },
        {
          key: 'logs',
          label: '告警日志',
          children: (
            <AlertLogs
              key={ruleId}
              ruleId={ruleId}
              clearRule={() => selectTab('logs')}
            />
          ),
        },
      ]}
    />
  );
}
