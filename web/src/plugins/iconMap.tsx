import {
  LayoutGrid,
  Database,
  Server,
  Settings,
  Wrench,
  Shield,
  FileText,
} from 'lucide-react';

const icons = {
  AppstoreOutlined: <LayoutGrid size={16} strokeWidth={1.85} />,
  DatabaseOutlined: <Database size={16} strokeWidth={1.85} />,
  CloudServerOutlined: <Server size={16} strokeWidth={1.85} />,
  SettingOutlined: <Settings size={16} strokeWidth={1.85} />,
  ToolOutlined: <Wrench size={16} strokeWidth={1.85} />,
  SafetyOutlined: <Shield size={16} strokeWidth={1.85} />,
  FileOutlined: <FileText size={16} strokeWidth={1.85} />,
};

export function pluginIcon(name: string) {
  return icons[name as keyof typeof icons] ?? icons.AppstoreOutlined;
}
