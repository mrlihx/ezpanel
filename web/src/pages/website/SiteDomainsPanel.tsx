import { Button, Input, Flex, Space, Typography } from 'antd';
import { SPACING } from '../../styles/tokens';
import ContentSizedTable from '../../components/ContentSizedTable';

import type { SiteSettingsState } from './SiteSettingsContent';

export default function SiteDomainsPanel({
  manageable,
  modal,
  newDomains,
  pending,
  runtime,
  save,
  setNewDomains,
  site,
}: Pick<
  SiteSettingsState,
  | 'manageable'
  | 'modal'
  | 'newDomains'
  | 'pending'
  | 'runtime'
  | 'save'
  | 'setNewDomains'
> & {
  site: NonNullable<SiteSettingsState['site']['data']>;
}) {
  return (
    <Space direction="vertical" size="large" style={{ width: '100%' }}>
      <Flex gap={SPACING.lg} align="center" wrap>
        <Input.TextArea
          aria-label="新增域名绑定"
          value={newDomains}
          onChange={(event) => setNewDomains(event.target.value)}
          autoSize={{ minRows: 5, maxRows: 8 }}
          style={{ flex: '1 1 320px' }}
          placeholder={
            '每行一个域名，默认80端口\n域名加端口：www.example.com:8080\nIP加端口：192.168.1.199:8080\nIPv6：[2001:db8::1]:8080'
          }
        />
        <Button
          type="primary"
          loading={pending}
          disabled={!manageable || !newDomains.trim()}
          onClick={async () => {
            const added = newDomains
              .split(/\r?\n/)
              .map((value) => value.trim())
              .filter(Boolean);
            if (
              await save({
                domains: [...site!.domains, ...added],
              })
            )
              setNewDomains('');
          }}
        >
          添加
        </Button>
      </Flex>
      <Typography.Text type="secondary">HTTPS端口：443</Typography.Text>
      <ContentSizedTable
        fillColumn="host"
        rowKey="binding"
        pagination={false}
        size="middle"
        dataSource={site.domains.map((binding) => {
          const end = binding.startsWith('[')
            ? binding.indexOf(']') + 1
            : binding.indexOf(':');
          return {
            binding,
            host: end > 0 ? binding.slice(0, end) : binding,
            port: end > 0 ? binding.slice(end + 1) || '80' : '80',
          };
        })}
        columns={[
          {
            title: '域名 / IP',
            dataIndex: 'host',
            render: (host: string, { binding, port }) => {
              if (host.startsWith('*.')) return host;
              const protocol =
                site?.sslEnabled && (port === '80' || port === '443')
                  ? 'https'
                  : 'http';
              return (
                <Typography.Link
                  href={`${protocol}://${binding}`}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {host}
                </Typography.Link>
              );
            },
          },
          { title: '端口', dataIndex: 'port', width: 90 },
          {
            title: '操作',
            width: 90,
            render: (_, row) => (
              <Button
                type="link"
                danger
                disabled={
                  !manageable ||
                  pending ||
                  (site!.domains.length === 1 && (!runtime || site!.sslEnabled))
                }
                onClick={() =>
                  modal.confirm({
                    title: `删除绑定 ${row.binding}？`,
                    onOk: async () => {
                      if (
                        !(await save({
                          domains: site!.domains.filter(
                            (value) => value !== row.binding,
                          ),
                        }))
                      )
                        throw new Error('绑定删除失败');
                    },
                  })
                }
              >
                删除
              </Button>
            ),
          },
        ]}
      />
    </Space>
  );
}
