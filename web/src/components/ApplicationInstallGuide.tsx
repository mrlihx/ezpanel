import { Button, Result, Space } from 'antd';
import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';

export default function ApplicationInstallGuide({
  name,
  install,
  onDetect,
  description = '在应用商店选择官方版本安装，保留已有配置。',
  installText = `选择版本安装 ${name}`,
  installDisabled,
  extra,
}: {
  name: string;
  install: string | (() => void);
  onDetect: () => void;
  description?: string;
  installText?: string;
  installDisabled?: boolean;
  extra?: ReactNode;
}) {
  const navigate = useNavigate();
  return (
    <Result
      status="info"
      title={`未检测到 ${name}`}
      subTitle={description}
      extra={
        <Space wrap>
          <Button
            type="primary"
            disabled={installDisabled}
            onClick={
              typeof install === 'string'
                ? () => void navigate(`/apps?install=${install}`)
                : install
            }
          >
            {installText}
          </Button>
          <Button onClick={onDetect}>重新检测</Button>
          {extra}
        </Space>
      }
    />
  );
}
