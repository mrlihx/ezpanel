import { Button, Flex, Space } from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import { formatTime } from '../../utils/format';

import type { DockerPageState } from './useDockerPage';

export default function DockerNetworksPanel({
  destroy,
  filter,
  fixedActions,
  match,
  networkList,
  online,
  open,
  prune,
}: Pick<
  DockerPageState,
  | 'destroy'
  | 'filter'
  | 'fixedActions'
  | 'match'
  | 'networkList'
  | 'online'
  | 'open'
  | 'prune'
>) {
  return (
    <>
      <Flex justify="space-between" gap={12} wrap style={{ marginBottom: 16 }}>
        <Space wrap>
          <Button
            type="primary"
            disabled={!online}
            onClick={() => open({ kind: 'network' })}
          >
            添加网络
          </Button>
          <Button disabled={!online} onClick={() => prune('networks')}>
            清理未使用网络
          </Button>
        </Space>
        {filter}
      </Flex>
      <ContentSizedTable
        fillColumn="Name"
        rowKey="Id"
        dataSource={networkList.data?.filter((r) => match(r.Name + ' ' + r.Id))}
        loading={networkList.isFetching}
        columns={[
          {
            title: '网络名',
            dataIndex: 'Name',
            width: 180,
            ellipsis: true,
          },
          { title: '驱动', dataIndex: 'Driver', width: 110 },
          {
            title: 'IPv4 子网',
            width: 220,
            ellipsis: true,
            render: (_, row) =>
              row.IPAM.Config?.flatMap(({ Subnet }) =>
                Subnet && !Subnet.includes(':') ? [Subnet] : [],
              ).join(', ') || '—',
          },
          {
            title: 'IPv4 网关',
            width: 180,
            ellipsis: true,
            render: (_, row) =>
              row.IPAM.Config?.flatMap(({ Gateway }) =>
                Gateway && !Gateway.includes(':') ? [Gateway] : [],
              ).join(', ') || '—',
          },
          { title: '作用于', dataIndex: 'Scope', width: 110 },
          {
            title: '创建时间',
            dataIndex: 'Created',
            width: 185,
            render: (value?: string) =>
              value && !value.startsWith('0001-') ? formatTime(value) : '—',
          },
          {
            title: '操作',
            fixed: fixedActions,
            width: 210,
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  onClick={() => open({ kind: 'connect', id: row.Id })}
                >
                  连接/断开
                </Button>
                <Button
                  type="link"
                  danger
                  disabled={[
                    'bridge',
                    'host',
                    'none',
                    'ezpanel-network',
                  ].includes(row.Name)}
                  onClick={() => destroy('networks', row.Id, row.Name)}
                >
                  删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
    </>
  );
}
