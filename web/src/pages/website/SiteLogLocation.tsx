import { Flex, Typography } from 'antd';
import { Link } from 'react-router-dom';
import { SPACING } from '../../styles/tokens';

export default function SiteLogLocation({ directory }: { directory?: string }) {
  return (
    <Flex align="baseline" gap={SPACING.md} style={{ marginBlock: SPACING.md }}>
      <Typography.Text style={{ flexShrink: 0 }}>Web 日志</Typography.Text>
      {directory ? (
        <Link
          style={{ minWidth: 0, overflowWrap: 'anywhere' }}
          to={`/files?path=${encodeURIComponent(directory)}`}
        >
          {directory}
        </Link>
      ) : (
        <Typography.Text type="secondary">—</Typography.Text>
      )}
    </Flex>
  );
}
