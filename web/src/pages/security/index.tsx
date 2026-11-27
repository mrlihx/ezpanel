import SectionTabs from '../../components/SectionTabs';
import { useParams } from 'react-router-dom';
import { Skeleton } from 'antd';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import Firewall from './Firewall';
import SSH from './SSH';
import Hardening from './Hardening';
import './security.css';
export default function SecurityPage() {
  const { section: requested, view } = useParams();
  const {
    tab: section,
    loading,
    invalid,
    selectTab,
  } = useRoutedTab('security', '/security', requested, [
    'firewall',
    'ssh',
    'hardening',
  ]);
  if (invalid || (view && requested !== 'ssh'))
    return <RouteNotFound back="/security" />;
  if (loading) return <Skeleton active />;
  return (
    <div className="security-page">
      <SectionTabs
        activeKey={section}
        onChange={selectTab}
        items={[
          { key: 'firewall', label: '系统防火墙' },
          { key: 'ssh', label: 'SSH管理' },
          { key: 'hardening', label: '系统加固' },
        ]}
      />
      {section === 'firewall' ? (
        <Firewall />
      ) : section === 'ssh' ? (
        <SSH />
      ) : (
        <Hardening />
      )}
    </div>
  );
}
