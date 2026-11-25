import { Alert, Button, Space, theme } from 'antd';
import { useNavigate } from 'react-router-dom';
export default function EnvironmentOverlay({
  engine,
  label,
  onRemote,
}: {
  engine: string;
  label: string;
  onRemote: () => void;
}) {
  const navigate = useNavigate();
  const { token } = theme.useToken();
  return (
    <div
      style={{
        position: 'absolute',
        inset: 0,
        zIndex: 5,
        background: token.colorBgContainer,
        opacity: 0.96,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 24,
        borderRadius: 8,
      }}
    >
      <Alert
        type="warning"
        showIcon
        message={`当前未安装${label}环境或未添加远程数据库`}
        description={
          <Space wrap>
            <Button type="link" onClick={onRemote}>
              添加远程数据库
            </Button>
            <Button
              type="link"
              onClick={() => navigate(`/apps?install=${engine}`)}
            >
              安装{label}环境
            </Button>
          </Space>
        }
      />
    </div>
  );
}
