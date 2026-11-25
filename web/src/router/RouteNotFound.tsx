import { Button, Result } from 'antd';
import { useNavigate } from 'react-router-dom';

export default function RouteNotFound({ back = '/' }: { back?: string }) {
  const navigate = useNavigate();
  return (
    <Result
      status="404"
      title="页面不存在"
      extra={
        <Button type="primary" onClick={() => void navigate(back)}>
          返回
        </Button>
      }
    />
  );
}
