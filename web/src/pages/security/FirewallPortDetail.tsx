import { Descriptions, Modal, Space, Typography } from 'antd';
import { LAYOUT, MONO_FONT } from '../../styles/tokens';

import type { FirewallPageState } from './Firewall';

export default function FirewallPortDetail({
  detail,
  listening,
  setDetail,
}: Pick<FirewallPageState, 'detail' | 'listening' | 'setDetail'>) {
  return (
    <Modal
      open={!!detail}
      title="端口监听详情"
      width={LAYOUT.shellWidth}
      footer={null}
      onCancel={() => setDetail(undefined)}
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        {(detail ? (listening(detail) ?? []) : []).map((row) => (
          <div
            key={`${row.protocol}:${row.address}:${row.port}:${row.pid}`}
            style={{
              padding: '12px 16px',
              borderRadius: 14,
              border: '1px solid rgba(128, 128, 136, 0.14)',
              background: 'rgba(118, 118, 128, 0.04)',
            }}
          >
            <Descriptions
              column={1}
              size="small"
              styles={{
                label: {
                  width: 76,
                  color: 'var(--ant-color-text-secondary)',
                },
                content: { overflowWrap: 'anywhere', minWidth: 0 },
              }}
              items={[
                {
                  key: 'protocol',
                  label: '协议',
                  children: row.protocol.toUpperCase(),
                },
                {
                  key: 'address',
                  label: '监听地址',
                  children: row.address,
                },
                {
                  key: 'port',
                  label: '端口',
                  children: row.port,
                },
                {
                  key: 'pid',
                  label: 'PID',
                  children: row.pid || '—',
                },
                {
                  key: 'command',
                  label: '启动命令',
                  children: row.command ? (
                    <Typography.Paragraph
                      copyable
                      style={{
                        fontFamily: MONO_FONT,
                        whiteSpace: 'pre-wrap',
                        overflowWrap: 'anywhere',
                        marginBottom: 0,
                      }}
                    >
                      {row.command}
                    </Typography.Paragraph>
                  ) : (
                    '无法读取'
                  ),
                },
              ]}
            />
          </div>
        ))}
      </Space>
    </Modal>
  );
}
