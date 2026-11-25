import { Skeleton } from 'antd';
import { useParams } from 'react-router-dom';
import SectionTabs from '../../components/SectionTabs';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import Overview from './Overview';
import Events from './Events';
import Rules from './Rules';
import Sites from './Sites';
import PolicyAndBans from './PolicyAndBans';
import './waf.css';

const SECTIONS = ['overview', 'events', 'rules', 'sites', 'policy'] as const;

export default function WAFPage() {
  const { section: requested } = useParams();
  const {
    tab: section,
    loading,
    invalid,
    selectTab,
  } = useRoutedTab('waf', '/waf', requested, [...SECTIONS]);
  if (invalid) return <RouteNotFound back="/waf" />;
  if (loading) return <Skeleton active />;
  return (
    <div className="waf-page">
      <SectionTabs
        activeKey={section}
        onChange={selectTab}
        items={[
          { key: 'overview', label: '安全概览' },
          { key: 'events', label: '攻击日志' },
          { key: 'rules', label: '防护规则' },
          { key: 'sites', label: '站点防护' },
          { key: 'policy', label: '全局策略与封禁' },
        ]}
      />
      {section === 'overview' ? (
        <Overview />
      ) : section === 'events' ? (
        <Events />
      ) : section === 'rules' ? (
        <Rules />
      ) : section === 'sites' ? (
        <Sites />
      ) : (
        <PolicyAndBans />
      )}
    </div>
  );
}
