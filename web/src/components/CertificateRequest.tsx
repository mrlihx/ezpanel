import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Descriptions,
  Flex,
  Form,
  Modal,
  Popconfirm,
  Select,
  Radio,
  Space,
  Tag,
  Typography,
} from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import ContentSizedTable from './ContentSizedTable';
import {
  cancelCertificateOrder,
  certificateOrder,
  certificateOrders,
  completeCertificateOrder,
  createCertificateOrder,
  retryCertificateOrderDeploy,
  type Certificate,
  type CertificateAlgorithm,
  type CertificateOrder,
} from '../api/certificate';
import { websites, type Website } from '../api/website';
import { usePluginList } from '../api/plugin';
import { LAYOUT, SPACING } from '../styles/tokens';
import { formatTime } from '../utils/format';
import TaskDrawer from './TaskDrawer';

const orderStates = {
  creating: { label: '准备验证', color: 'processing' },
  pending: { label: '等待验证', color: 'warning' },
  issuing: { label: '正在签发', color: 'processing' },
  valid: { label: '已签发', color: 'success' },
  failed: { label: '失败', color: 'error' },
  cancelled: { label: '已取消', color: 'default' },
};
type Values = {
  domains: string[];
  algorithm: CertificateAlgorithm;
  method: 'http-01' | 'dns-01';
  dnsProvider?: string;
  siteId?: string;
  acceptTerms: boolean;
};
const inheritDNSProvider = '__inherit__';

export default function CertificateRequest({
  defaultDomains,
  siteId,
  autoTempSite,
  renewal,
  onDeploy,
}: {
  defaultDomains?: string[];
  siteId?: string;
  autoTempSite?: boolean;
  renewal?: Certificate;
  onDeploy: (id: string) => Promise<void | false>;
}) {
  const { message } = App.useApp();
  const client = useQueryClient();
  const [form] = Form.useForm<Values>();
  const method = Form.useWatch('method', form) ?? 'http-01';
  const domains = Form.useWatch('domains', form) ?? [];
  const [requestOpen, setRequestOpen] = useState(false);
  const domainOptions = [
    ...new Set(
      [...(renewal?.domains ?? []), ...(defaultDomains ?? [])]
        .map((domain) => domain.replace(/:\d+$/, '').trim().toLowerCase())
        .filter(Boolean),
    ),
  ];
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [taskId, setTaskId] = useState<string | null>(null);
  const [taskOpen, setTaskOpen] = useState(false);
  const [orderId, setOrderId] = useState<string>();
  const orders = useQuery({
    queryKey: ['certificate-orders', siteId],
    queryFn: () => certificateOrders(siteId),
  });
  const plugins = usePluginList();
  const dnsProviders = (plugins.data ?? []).flatMap((plugin) =>
    plugin.runtimeStatus === 'running' && plugin.dnsProvider
      ? [{ value: plugin.id, label: plugin.dnsProvider.title }]
      : [],
  );
  const detail = useQuery({
    queryKey: ['certificate-order', orderId],
    queryFn: () => certificateOrder(orderId!),
    enabled: !!orderId,
  });
  const sites = useQuery({
    queryKey: ['certificate-http-sites'],
    queryFn: async () => {
      const list: Website[] = [];
      let page = 1;
      for (;;) {
        const result = await websites(page++, 100, '');
        list.push(...result.list);
        if ((page - 1) * 100 >= result.total) return list;
      }
    },
    enabled: !siteId && !autoTempSite && method === 'http-01',
  });
  const requestedDomains = domains
    .map((value: string) => value.trim().toLowerCase())
    .filter(Boolean);
  const eligibleSites = sites.data?.filter(
    (site) =>
      site.enabled &&
      requestedDomains.every((domain: string) =>
        site.domains.some(
          (binding) => binding === domain || binding === `${domain}:80`,
        ),
      ),
  );
  const refresh = async () => {
    await Promise.all([
      orders.refetch(),
      client.invalidateQueries({ queryKey: ['certificates'] }),
      ...(orderId ? [detail.refetch()] : []),
    ]);
  };
  const run = async (key: string, action: () => Promise<void>) => {
    setBusy(key);
    setError('');
    try {
      await action();
    } catch (e) {
      setError(e instanceof Error ? e.message : '操作失败');
    } finally {
      setBusy('');
    }
  };
  const showTask = (id: string) => {
    setTaskId(id);
    setTaskOpen(true);
  };
  const submit = (values: Values) => {
    if (busy) return;
    return run('submit', async () => {
      const result = await createCertificateOrder({
        domains: values.domains.map((value) => value.trim()).filter(Boolean),
        algorithm: values.algorithm,
        email: '',
        method: values.method,
        dnsProvider:
          values.method === 'dns-01' &&
          values.dnsProvider !== inheritDNSProvider
            ? values.dnsProvider
            : undefined,
        inheritDNSProvider:
          values.method === 'dns-01' &&
          values.dnsProvider === inheritDNSProvider,
        siteId:
          siteId ||
          (!autoTempSite && values.method === 'http-01' ? values.siteId : ''),
        renewalId: renewal?.id,
        acceptTerms: true,
      });
      form.setFieldValue('acceptTerms', false);
      setRequestOpen(false);
      setOrderId(result.orderId);
      showTask(result.taskId);
      await orders.refetch();
    });
  };
  const deployOrder = (order: CertificateOrder) =>
    run(`deploy-${order.id}`, async () => {
      if (order.autoDeploy && order.status === 'failed') {
        const result = await retryCertificateOrderDeploy(order.id);
        showTask(result.taskId);
        await refresh();
        return;
      }
      if ((await onDeploy(order.certificateId)) === false) return;
      void message.success('证书已部署');
    });
  const activeOrder = detail.data;
  const requestForm = (
    <Form
      form={form}
      layout="horizontal"
      labelCol={{ sm: { span: 5 } }}
      wrapperCol={{ sm: { span: 19 } }}
      labelAlign="left"
      initialValues={{
        domains: [
          ...new Set(
            (renewal?.domains ?? defaultDomains ?? [])
              .map((domain) => domain.replace(/:\d+$/, '').trim().toLowerCase())
              .filter(Boolean),
          ),
        ],
        algorithm: renewal?.algorithm ?? 'rsa2048',
        method: renewal?.validationMethod || 'http-01',
        dnsProvider: renewal ? inheritDNSProvider : undefined,
        acceptTerms: false,
      }}
      onFinish={(values) => void submit(values)}
    >
      <Form.Item label="证书品牌">
        <Radio checked>Let's Encrypt</Radio>
      </Form.Item>
      <Form.Item label="证书算法" name="algorithm">
        <Radio.Group
          disabled={!!renewal}
          options={[
            { value: 'rsa2048', label: 'RSA 2048' },
            { value: 'rsa4096', label: 'RSA 4096' },
            { value: 'ecdsa256', label: 'ECDSA 256' },
            { value: 'ecdsa384', label: 'ECDSA 384' },
          ]}
        />
      </Form.Item>
      <Form.Item label="验证方式" name="method">
        <Radio.Group
          options={[
            { value: 'http-01', label: 'HTTP 文件验证' },
            { value: 'dns-01', label: 'DNS 验证' },
          ]}
        />
      </Form.Item>
      {method === 'dns-01' && (renewal || dnsProviders.length > 0) && (
        <Form.Item label="DNS 渠道" name="dnsProvider">
          <Select
            allowClear
            options={
              renewal
                ? [
                    { value: inheritDNSProvider, label: '沿用原验证渠道' },
                    ...dnsProviders,
                  ]
                : dnsProviders
            }
            placeholder="留空时手动添加 TXT 记录"
          />
        </Form.Item>
      )}
      <Form.Item label="申请域名" required>
        {domainOptions.length > 0 && (
          <Checkbox
            checked={domains.length === domainOptions.length}
            indeterminate={
              domains.length > 0 && domains.length < domainOptions.length
            }
            onChange={(event) =>
              form.setFieldValue(
                'domains',
                event.target.checked ? domainOptions : [],
              )
            }
            style={{ marginBottom: SPACING.sm }}
          >
            全选
          </Checkbox>
        )}
        <Form.Item
          name="domains"
          noStyle
          rules={[
            {
              required: true,
              type: 'array',
              min: 1,
              message: '请选择申请域名',
            },
          ]}
        >
          {domainOptions.length > 0 ? (
            <Checkbox.Group
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: SPACING.sm,
              }}
              options={domainOptions}
            />
          ) : (
            <Select
              mode="tags"
              tokenSeparators={[',', ' ', '\n']}
              placeholder="输入域名后按回车"
            />
          )}
        </Form.Item>
      </Form.Item>
      {!siteId && !autoTempSite && method === 'http-01' && (
        <Form.Item
          label="验证网站"
          name="siteId"
          rules={[
            { required: true, message: '请选择绑定这些域名的 80 端口网站' },
            {
              validator: (_, value) =>
                !value || eligibleSites?.some((site) => site.id === value)
                  ? Promise.resolve()
                  : Promise.reject(
                      new Error('所选网站未启用或未在 80 端口绑定全部域名'),
                    ),
            },
          ]}
          extra="仅显示已启用且在 80 端口绑定全部申请域名的网站。"
        >
          <Select
            showSearch
            optionFilterProp="label"
            loading={sites.isFetching}
            placeholder="选择验证网站"
            options={eligibleSites?.map((site) => ({
              value: site.id,
              label: `${site.name} · ${site.domains.join('、')}`,
            }))}
          />
        </Form.Item>
      )}
      {sites.isError && !siteId && !autoTempSite && method === 'http-01' && (
        <Alert
          type="error"
          message={sites.error.message}
          action={<Button onClick={() => void sites.refetch()}>重试</Button>}
        />
      )}
      <Form.Item
        name="acceptTerms"
        wrapperCol={{ sm: { offset: 5, span: 19 } }}
        valuePropName="checked"
        rules={[
          {
            validator: (_, value) =>
              value
                ? Promise.resolve()
                : Promise.reject(new Error('请先同意服务条款')),
          },
        ]}
      >
        <Checkbox>
          同意{' '}
          <Typography.Link
            href="https://letsencrypt.org/repository/"
            target="_blank"
            rel="noopener noreferrer"
          >
            Let's Encrypt 服务条款
          </Typography.Link>
        </Checkbox>
      </Form.Item>
      {renewal && (
        <Form.Item wrapperCol={{ sm: { offset: 5, span: 19 } }}>
          <Button
            type="primary"
            htmlType="submit"
            loading={busy === 'submit'}
            disabled={!!busy}
          >
            申请续签
          </Button>
        </Form.Item>
      )}
    </Form>
  );
  return (
    <Flex vertical gap={SPACING.lg}>
      {error && !requestOpen && !orderId && (
        <Alert
          type="error"
          showIcon
          message={error}
          closable
          onClose={() => setError('')}
        />
      )}
      {renewal ? (
        <Flex vertical gap={SPACING.lg}>
          <Alert
            type="info"
            showIcon
            message="Let's Encrypt 免费证书"
            description="HTTP 验证需要域名指向本机且公网可访问 80 端口；手动 DNS 验证支持泛域名，每次申请或续签都需要添加新的 TXT 记录。"
          />
          {requestForm}
        </Flex>
      ) : (
        <Modal
          title="申请免费证书"
          open={requestOpen}
          destroyOnHidden
          width={LAYOUT.databaseModalWidth}
          okText="申请证书"
          confirmLoading={busy === 'submit'}
          okButtonProps={{ disabled: !!busy }}
          onOk={() => form.submit()}
          onCancel={() => {
            if (!busy) {
              setRequestOpen(false);
              setError('');
            }
          }}
        >
          <Flex vertical gap={SPACING.lg}>
            <Alert
              type="info"
              showIcon
              message="Let's Encrypt 免费证书"
              description="HTTP 验证需要域名指向本机且公网可访问 80 端口；手动 DNS 验证支持泛域名，每次申请或续签都需要添加新的 TXT 记录。"
            />
            {error && (
              <Alert
                type="error"
                showIcon
                message={error}
                closable
                onClose={() => setError('')}
              />
            )}
            {requestForm}
          </Flex>
        </Modal>
      )}
      <Flex justify="space-between" align="center">
        <Typography.Text strong>申请记录</Typography.Text>
        <Space>
          {!renewal && (
            <Button
              type="primary"
              onClick={() => {
                setError('');
                form.resetFields();
                setRequestOpen(true);
              }}
            >
              申请证书
            </Button>
          )}
          <Button
            loading={orders.isFetching}
            onClick={() => void orders.refetch()}
          >
            刷新
          </Button>
        </Space>
      </Flex>
      {orders.isError && <Alert type="error" message={orders.error.message} />}
      <ContentSizedTable<CertificateOrder>
        fillColumn="domains"
        size="small"
        rowKey="id"
        loading={orders.isPending}
        dataSource={orders.data?.list ?? []}
        pagination={{ pageSize: 5, hideOnSinglePage: true }}
        columns={[
          {
            title: '域名',
            dataIndex: 'domains',
            render: (items: string[]) => items.join('、'),
          },
          { title: '算法', dataIndex: 'algorithm' },
          {
            title: '验证方式',
            dataIndex: 'method',
            render: (value: string) =>
              value === 'dns-01' ? '手动 DNS' : 'HTTP 文件',
          },
          {
            title: '状态',
            dataIndex: 'status',
            render: (value: CertificateOrder['status'], row) => (
              <Tag color={orderStates[value].color}>
                {value === 'failed' && row.certificateId
                  ? '部署失败'
                  : orderStates[value].label}
              </Tag>
            ),
          },
          { title: '申请时间', dataIndex: 'createdAt', render: formatTime },
          {
            title: '操作',
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  size="small"
                  onClick={() => setOrderId(row.id)}
                >
                  {row.status === 'pending' ? '配置 DNS' : '详情'}
                </Button>
                {row.taskId && (
                  <Button
                    type="link"
                    size="small"
                    onClick={() => showTask(row.taskId)}
                  >
                    任务
                  </Button>
                )}
                {row.certificateId &&
                  (row.status === 'valid' || row.status === 'failed') && (
                    <Button
                      type="link"
                      size="small"
                      disabled={!!busy}
                      loading={busy === `deploy-${row.id}`}
                      onClick={() => void deployOrder(row)}
                    >
                      {row.status === 'failed' ? '重试部署' : '部署'}
                    </Button>
                  )}
              </Space>
            ),
          },
        ]}
      />
      <Modal
        open={!!orderId}
        title="证书申请详情"
        loading={detail.isPending && !!orderId}
        width={LAYOUT.databaseModalWidth}
        onCancel={() => setOrderId(undefined)}
        footer={
          <Button
            loading={detail.isFetching}
            onClick={() => void detail.refetch()}
          >
            刷新状态
          </Button>
        }
      >
        {error && (
          <Alert
            type="error"
            message={error}
            style={{ marginBottom: SPACING.lg }}
          />
        )}
        {detail.isError && (
          <Alert type="error" message={detail.error.message} />
        )}
        {activeOrder && (
          <Flex vertical gap={SPACING.lg}>
            <Descriptions
              column={1}
              size="small"
              items={[
                {
                  key: 'domains',
                  label: '域名',
                  children: activeOrder.domains.join('、'),
                },
                {
                  key: 'algorithm',
                  label: '证书算法',
                  children: activeOrder.algorithm,
                },
                {
                  key: 'deploy',
                  label: '部署方式',
                  children: activeOrder.autoDeploy
                    ? '签发后自动部署'
                    : '手动部署',
                },
                {
                  key: 'status',
                  label: '状态',
                  children: (
                    <Tag color={orderStates[activeOrder.status].color}>
                      {activeOrder.status === 'failed' &&
                      activeOrder.certificateId
                        ? '部署失败'
                        : orderStates[activeOrder.status].label}
                    </Tag>
                  ),
                },
              ]}
            />
            {activeOrder.error && (
              <Alert type="error" message={activeOrder.error} />
            )}
            {activeOrder.status === 'pending' &&
              activeOrder.method === 'dns-01' && (
                <>
                  <Alert
                    type="warning"
                    showIcon
                    message="请在域名服务商添加以下 TXT 记录"
                    description="同一个记录名可能有多个值，请全部保留。确认 DNS 已生效后再继续验证。"
                  />
                  {activeOrder.challenges.map((challenge, index) => (
                    <Descriptions
                      key={`${challenge.domain}-${index}`}
                      column={1}
                      size="small"
                      bordered
                      items={[
                        {
                          key: 'domain',
                          label: '域名',
                          children: challenge.domain,
                        },
                        { key: 'type', label: '类型', children: 'TXT' },
                        {
                          key: 'name',
                          label: '记录名称',
                          children: (
                            <Typography.Text
                              copyable
                              style={{ overflowWrap: 'anywhere' }}
                            >
                              {challenge.recordName}
                            </Typography.Text>
                          ),
                        },
                        {
                          key: 'value',
                          label: '记录值',
                          children: (
                            <Typography.Text
                              copyable
                              style={{ overflowWrap: 'anywhere' }}
                            >
                              {challenge.value}
                            </Typography.Text>
                          ),
                        },
                      ]}
                    />
                  ))}
                  <Button
                    type="primary"
                    loading={busy === 'complete'}
                    disabled={!!busy}
                    onClick={() =>
                      void run('complete', async () => {
                        const result = await completeCertificateOrder(
                          activeOrder.id,
                        );
                        showTask(result.taskId);
                        await refresh();
                      })
                    }
                  >
                    已添加 TXT，继续验证
                  </Button>
                  <Popconfirm
                    title="取消这条等待验证的申请？"
                    description="面板将保留取消记录，你可以重新发起申请。"
                    onConfirm={() =>
                      run('cancel', async () => {
                        await cancelCertificateOrder(activeOrder.id);
                        await refresh();
                      })
                    }
                  >
                    <Button
                      danger
                      loading={busy === 'cancel'}
                      disabled={!!busy}
                    >
                      取消申请
                    </Button>
                  </Popconfirm>
                </>
              )}
            {activeOrder.status === 'valid' && (
              <Alert
                type="success"
                showIcon
                message={
                  activeOrder.autoDeploy
                    ? '证书已签发并自动部署'
                    : '证书已保存到本地证书夹'
                }
                action={
                  activeOrder.certificateId && (
                    <Button
                      disabled={!!busy}
                      loading={busy === `deploy-${activeOrder.id}`}
                      onClick={() => void deployOrder(activeOrder)}
                    >
                      部署证书
                    </Button>
                  )
                }
              />
            )}
            {activeOrder.status === 'failed' && activeOrder.certificateId && (
              <Button
                type="primary"
                disabled={!!busy}
                loading={busy === `deploy-${activeOrder.id}`}
                onClick={() => void deployOrder(activeOrder)}
              >
                重试部署
              </Button>
            )}
            {activeOrder.taskId && (
              <Button onClick={() => showTask(activeOrder.taskId)}>
                查看任务日志
              </Button>
            )}
          </Flex>
        )}
      </Modal>
      <TaskDrawer
        taskId={taskId}
        title="申请证书"
        open={taskOpen}
        onClose={() => {
          setTaskOpen(false);
          void refresh();
        }}
        onSettled={() => void refresh()}
      />
    </Flex>
  );
}
