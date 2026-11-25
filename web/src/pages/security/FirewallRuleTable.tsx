import { firewallRuleNames, firewallRuleOriginLabel } from './firewallLabels';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  saveFirewallRule,
  deleteFirewallRule,
  batchFirewallRules,
  type FirewallRule,
} from '../../api/security';
import { Button, Select, Space, Tag, Tooltip, Typography } from 'antd';
import { formatTime } from '../../utils/format';
import { countryLabel } from './countries';
import type { FirewallPageState } from './Firewall';
export default function FirewallRuleTable({
  batchAction,
  expired,
  fixedActions,
  kind,
  listening,
  message,
  modal,
  open,
  query,
  rows,
  selected,
  setBatchAction,
  setDetail,
  setSelected,
  setWorking,
  working,
}: Pick<
  FirewallPageState,
  | 'batchAction'
  | 'expired'
  | 'fixedActions'
  | 'kind'
  | 'listening'
  | 'message'
  | 'modal'
  | 'open'
  | 'query'
  | 'rows'
  | 'selected'
  | 'setBatchAction'
  | 'setDetail'
  | 'setSelected'
  | 'setWorking'
  | 'working'
>) {
  const common = [
    {
      title: '策略',
      key: 'policy',
      width: 64,
      render: (_: unknown, row: FirewallRule) =>
        row.readOnly ? (
          <Typography.Text type={row.policy === 'deny' ? 'danger' : 'success'}>
            {row.policy === 'allow' ? '放行' : '拒绝'}
          </Typography.Text>
        ) : (
          <Tooltip
            title={`点击切换为${row.policy === 'allow' ? '拒绝' : '放行'}`}
          >
            <Button
              type="link"
              size="small"
              danger={row.policy === 'deny'}
              disabled={working}
              style={{ padding: 0 }}
              onClick={async () => {
                const policy = row.policy === 'allow' ? 'deny' : 'allow';
                setWorking(true);
                try {
                  await saveFirewallRule({ ...row, policy }, row.id);
                  await query.refetch();
                  message.success(
                    `策略已切换为${policy === 'allow' ? '放行' : '拒绝'}`,
                  );
                } finally {
                  setWorking(false);
                }
              }}
            >
              {row.policy === 'allow' ? '放行' : '拒绝'}
            </Button>
          </Tooltip>
        ),
    },
    {
      title: '方向',
      dataIndex: 'direction',
      width: 64,
      render: (value: string) =>
        value === 'in' ? '入站' : value === 'out' ? '出站' : '双向',
    },
    {
      title: '状态',
      key: 'enabled',
      width: 72,
      render: (_: unknown, row: FirewallRule) => (
        <Typography.Text
          type={row.enabled && !expired(row) ? 'success' : 'secondary'}
        >
          {expired(row) ? '已过期' : row.enabled ? '启用' : '停用'}
        </Typography.Text>
      ),
    },
  ];
  const columns =
    kind === 'port'
      ? [
          { title: '协议', dataIndex: 'protocol', width: 64 },
          {
            title: '端口',
            dataIndex: 'ports',
            width: 136,
            render: (value: string) => (value === 'all' ? '全部端口' : value),
          },
          {
            title: '端口监听',
            key: 'listen',
            width: 112,
            render: (_: unknown, row: FirewallRule) =>
              query.data?.listenersKnown ? (
                <Space size={4}>
                  {listening(row)?.length ? '正常' : '未使用'}
                  {!!listening(row)?.length && (
                    <Button
                      type="link"
                      size="small"
                      onClick={() => setDetail(row)}
                    >
                      详情
                    </Button>
                  )}
                </Space>
              ) : (
                '未知'
              ),
          },
          ...common.filter((col) => col.key !== 'enabled'),
          {
            title: '来源 IP',
            dataIndex: 'peer',
            width: 224,
            ellipsis: true,
            render: (value: string) =>
              value === 'any' ? (
                '所有IP'
              ) : (
                <Tooltip
                  title={
                    <span style={{ whiteSpace: 'pre-line' }}>{value}</span>
                  }
                >
                  {value.split('\n').join('、')}
                </Tooltip>
              ),
          },
        ]
      : kind === 'ip'
        ? [
            {
              title: 'IP地址',
              dataIndex: 'peer',
              width: 180,
              ellipsis: true,
              render: (value: string) => (
                <Tooltip
                  title={
                    <span style={{ whiteSpace: 'pre-line' }}>{value}</span>
                  }
                >
                  {value.split('\n').join('、')}
                </Tooltip>
              ),
            },
            {
              title: 'IP归属地',
              dataIndex: 'location',
              width: 160,
              render: (value?: string) => value || '—',
            },
            ...common,
          ]
        : kind === 'forward'
          ? [
              { title: '协议', dataIndex: 'protocol', width: 80 },
              { title: '源端口', dataIndex: 'ports', width: 100 },
              {
                title: '目标 IP',
                dataIndex: 'targetIP',
                width: 180,
                ellipsis: true,
              },
              { title: '目标端口', dataIndex: 'targetPort', width: 110 },
            ]
          : [
              {
                title: '地区',
                dataIndex: 'country',
                width: 224,
                render: (value: string) => countryLabel(value),
              },
              {
                title: '端口',
                dataIndex: 'ports',
                width: 130,
                render: (value: string) =>
                  !value || value === 'all' ? '全部端口' : value,
              },
              ...common.filter((col) => col.dataIndex !== 'direction'),
            ];
  return (
    <>
      <ContentSizedTable<FirewallRule>
        className="security-data-table"
        fillColumn={
          kind === 'port'
            ? rows.some((row) => row.peer !== 'any')
              ? 'peer'
              : 'ports'
            : ['peer', 'targetIP', 'country']
        }
        growColumns={['remark']}
        rowKey="id"
        rowSelection={{
          selectedRowKeys: selected,
          onChange: setSelected,
          getCheckboxProps: (row) => ({ disabled: row.readOnly }),
        }}
        loading={query.isFetching}
        dataSource={rows}
        size="middle"
        pagination={{
          defaultPageSize: 20,
          showSizeChanger: true,
          showTotal: (total) => `共 ${total} 条`,
        }}
        columns={[
          ...columns,
          {
            title: '规则来源',
            dataIndex: 'origin',
            render: (_, row) =>
              row.origin && (
                <Tooltip title={row.detail}>
                  <Tag
                    className="firewall-origin"
                    color={row.readOnly ? 'default' : 'processing'}
                  >
                    {firewallRuleOriginLabel(row.origin)}
                  </Tag>
                </Tooltip>
              ),
          },
          { title: '备注', dataIndex: 'remark', width: 260 },
          {
            title: '时间',
            dataIndex: 'createdAt',
            width: 170,
            render: (value: string) =>
              value && !value.startsWith('0001-') ? formatTime(value) : '—',
          },
          ...(kind === 'ip'
            ? [
                {
                  title: '过期时间',
                  dataIndex: 'expiresAt',
                  width: 170,
                  render: (value: string | null) =>
                    value ? formatTime(value) : '永久',
                },
              ]
            : []),
          {
            title: '操作',
            key: 'actions',
            fixed: fixedActions,
            render: (_, row) =>
              row.readOnly ? (
                <Typography.Text type="secondary">只读</Typography.Text>
              ) : (
                <Space size={12}>
                  <Button type="link" size="small" onClick={() => open(row)}>
                    修改
                  </Button>
                  <Button
                    type="link"
                    size="small"
                    danger
                    disabled={working}
                    onClick={() =>
                      modal.confirm({
                        title: `删除${firewallRuleNames[row.kind]}？`,
                        content: row.ports || row.peer || row.country,
                        onOk: async () => {
                          await deleteFirewallRule(row.id);
                          await query.refetch();
                        },
                      })
                    }
                  >
                    删除
                  </Button>
                </Space>
              ),
          },
        ]}
      />
      <Space wrap style={{ marginTop: 16 }}>
        <Select
          value={batchAction}
          onChange={setBatchAction}
          style={{ width: 180, maxWidth: '100%' }}
          options={[
            { value: 'delete', label: '删除规则' },
            { value: 'enable', label: '启用规则' },
            { value: 'disable', label: '停用规则' },
          ]}
        />
        <Button
          disabled={!selected.length || working}
          onClick={() =>
            modal.confirm({
              title: `对选中${selected.length}条规则执行${batchAction === 'delete' ? '删除' : batchAction === 'enable' ? '启用' : '停用'}？`,
              onOk: async () => {
                setWorking(true);
                try {
                  await batchFirewallRules(selected.map(String), batchAction);
                  setSelected([]);
                  await query.refetch();
                  message.success('批量操作已完成');
                } finally {
                  setWorking(false);
                }
              },
            })
          }
        >
          批量操作
        </Button>
      </Space>
    </>
  );
}
