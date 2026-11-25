import {
  Alert,
  Button,
  Form,
  Input,
  Card,
  Select,
  Skeleton,
  Space,
  Tag,
} from 'antd';
import IPSettings from './IPSettings';
import { testDNS, type DNSResult } from '../../api/toolbox';
import ContentSizedTable from '../../components/ContentSizedTable';
import type { ToolboxPageState } from './index';

export default function ToolIpPanel({
  busy,
  confirm,
  dns,
  dnsForm,
  dnsRows,
  formProps,
  ip,
  modal,
  save,
  setBusy,
  setDNSRows,
}: Pick<
  ToolboxPageState,
  | 'busy'
  | 'confirm'
  | 'dns'
  | 'dnsForm'
  | 'dnsRows'
  | 'formProps'
  | 'ip'
  | 'modal'
  | 'save'
  | 'setBusy'
  | 'setDNSRows'
>) {
  return (
    <>
      {ip.data && (
        <Card title="IP 设置">
          <IPSettings data={ip.data} onChanged={ip.refetch} />
        </Card>
      )}
      {
        <Card title="DNS 设置">
          {dns.error && (
            <Alert
              type="error"
              message={dns.error.message}
              action={<Button onClick={() => void dns.refetch()}>重试</Button>}
            />
          )}
          {dns.isPending && <Skeleton active />}
          {dns.data && (
            <>
              <Form
                {...formProps}
                form={dnsForm}
                initialValues={{
                  primary: dns.data.servers[0] ?? '',
                  secondary: dns.data.servers[1] ?? '',
                }}
                onFinish={(v) =>
                  confirm(
                    '保存 DNS 设置？',
                    '设置错误会影响服务器域名解析。',
                    () =>
                      save('dns', {
                        servers: [v.primary, v.secondary].filter(Boolean),
                      }),
                  )
                }
              >
                <Form.Item
                  label="主 DNS"
                  name="primary"
                  rules={[{ required: true }]}
                >
                  <Input />
                </Form.Item>
                <Form.Item label="备用 DNS" name="secondary">
                  <Input />
                </Form.Item>
                <Form.Item label="快捷选择">
                  <Select
                    placeholder="请选择公共 DNS"
                    options={dns.data.providers.map((p) => ({
                      value: p.id,
                      label: p.name,
                    }))}
                    onChange={(id) => {
                      const p = dns.data!.providers.find((p) => p.id === id)!;
                      dnsForm.setFieldsValue({
                        primary: p.primary,
                        secondary: p.secondary,
                      });
                    }}
                  />
                </Form.Item>
                <Form.Item label=" ">
                  <Space wrap>
                    <Button
                      loading={busy}
                      onClick={async () => {
                        setBusy(true);
                        try {
                          setDNSRows((await testDNS({ recommend: true })).list);
                        } finally {
                          setBusy(false);
                        }
                      }}
                    >
                      测速并推荐
                    </Button>
                    <Button
                      loading={busy}
                      onClick={async () => {
                        const v = await dnsForm.validateFields();
                        setBusy(true);
                        try {
                          setDNSRows(
                            (
                              await testDNS({
                                servers: [v.primary, v.secondary].filter(
                                  Boolean,
                                ),
                              })
                            ).list,
                          );
                        } finally {
                          setBusy(false);
                        }
                      }}
                    >
                      测试
                    </Button>
                    <Button htmlType="submit" type="primary" loading={busy}>
                      保存
                    </Button>
                  </Space>
                </Form.Item>
              </Form>
              {dnsRows && (
                <ContentSizedTable<DNSResult>
                  fillColumn="name"
                  size="small"
                  rowKey="id"
                  dataSource={dnsRows}
                  pagination={false}
                  columns={[
                    { title: 'DNS', dataIndex: 'name' },
                    { title: '主 DNS', dataIndex: 'primary' },
                    {
                      title: '备用 DNS',
                      render: (_, r) => r.secondary || '—',
                    },
                    {
                      title: 'TCP 延迟',
                      render: (_, r) =>
                        r.latency === null ? '—' : `${r.latency.toFixed(1)} ms`,
                    },
                    {
                      title: '解析耗时',
                      render: (_, r) =>
                        r.resolveMs === null
                          ? '—'
                          : `${r.resolveMs.toFixed(1)} ms`,
                    },
                    {
                      title: '可用性',
                      render: (_, r) => (
                        <Tag
                          color={r.success === r.total ? 'success' : 'warning'}
                        >
                          {r.success}/{r.total}
                        </Tag>
                      ),
                    },
                    {
                      title: '操作',
                      render: (_, r) => (
                        <Space>
                          <Button
                            type="link"
                            onClick={() =>
                              dnsForm.setFieldsValue({
                                primary: r.primary,
                                secondary: r.secondary,
                              })
                            }
                          >
                            选用
                          </Button>
                          <Button
                            type="link"
                            onClick={() =>
                              modal.info({
                                title: r.name,
                                content: r.errors.length
                                  ? r.errors.map((e) => <div key={e}>{e}</div>)
                                  : '本次解析全部成功',
                              })
                            }
                          >
                            详情
                          </Button>
                        </Space>
                      ),
                    },
                  ]}
                />
              )}
              <Alert
                type="info"
                message="测速由服务器发起，以实际解析成功率和耗时排序。系统使用 resolved 时保存独立配置，否则仅修改普通 resolv.conf 文件。"
              />
            </>
          )}
        </Card>
      }
    </>
  );
}
