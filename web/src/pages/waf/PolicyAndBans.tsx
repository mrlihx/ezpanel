import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  App,
  Button,
  Card,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Skeleton,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import dayjs from 'dayjs';
import {
  createWAFBan,
  deleteWAFBan,
  getWAFConfig,
  listWAFBans,
  saveWAFConfig,
  syncWAFBanFirewall,
  type WAFGlobalConfig,
} from '../../api/waf';
import { LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';

function splitLines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export default function PolicyAndBans() {
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [form] = Form.useForm();
  const [banForm] = Form.useForm();
  const [banModal, setBanModal] = useState(false);
  const [banSearch, setBanSearch] = useState('');

  const configQuery = useQuery({
    queryKey: ['waf-config'],
    queryFn: getWAFConfig,
  });

  const bansQuery = useQuery({
    queryKey: ['waf-bans', banSearch],
    queryFn: () => listWAFBans(banSearch),
  });

  useEffect(() => {
    const cfg = configQuery.data?.config;
    if (!cfg) return;
    form.setFieldsValue({
      ...cfg,
      allowIPsText: (cfg.allowIPs ?? []).join('\n'),
      denyIPsText: (cfg.denyIPs ?? []).join('\n'),
      allowPathsText: (cfg.allowPaths ?? []).join('\n'),
    });
  }, [configQuery.data, form]);

  const saveConfigMutation = useMutation({
    mutationFn: (cfg: WAFGlobalConfig) => saveWAFConfig(cfg),
    onSuccess: () => {
      message.success('全局 WAF 策略已保存并热更新');
      void queryClient.invalidateQueries({ queryKey: ['waf-config'] });
      void queryClient.invalidateQueries({ queryKey: ['waf-overview'] });
    },
  });

  const createBanMutation = useMutation({
    mutationFn: createWAFBan,
    onSuccess: () => {
      message.success('IP 已加入封禁名单');
      setBanModal(false);
      banForm.resetFields();
      void queryClient.invalidateQueries({ queryKey: ['waf-bans'] });
      void queryClient.invalidateQueries({ queryKey: ['waf-overview'] });
    },
  });

  const deleteBanMutation = useMutation({
    mutationFn: deleteWAFBan,
    onSuccess: () => {
      message.success('已解封该 IP');
      void queryClient.invalidateQueries({ queryKey: ['waf-bans'] });
      void queryClient.invalidateQueries({ queryKey: ['waf-overview'] });
    },
  });

  const syncFirewallMutation = useMutation({
    mutationFn: syncWAFBanFirewall,
    onSuccess: () => {
      message.success('已下沉至系统防火墙（nftables）');
      void queryClient.invalidateQueries({ queryKey: ['waf-bans'] });
    },
  });

  if (configQuery.isLoading) {
    return <Skeleton active />;
  }

  return (
    <Flex vertical gap={SPACING.lg}>
      <Card
        title="全局 WAF 策略"
        extra={
          <Button
            type="primary"
            loading={saveConfigMutation.isPending}
            onClick={() => void form.submit()}
          >
            保存全局策略
          </Button>
        }
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={(values) => {
            saveConfigMutation.mutate({
              enabled: !!values.enabled,
              mode: values.mode,
              ccEnabled: !!values.ccEnabled,
              ccWindowSeconds: values.ccWindowSeconds,
              ccMaxRequests: values.ccMaxRequests,
              ccAction: values.ccAction,
              autoBanEnabled: !!values.autoBanEnabled,
              autoBanWindow: values.autoBanWindow,
              autoBanThreshold: values.autoBanThreshold,
              autoBanDuration: values.autoBanDuration,
              syncFirewall: !!values.syncFirewall,
              retentionDays: values.retentionDays,
              allowIPs: splitLines(values.allowIPsText || ''),
              denyIPs: splitLines(values.denyIPsText || ''),
              allowPaths: splitLines(values.allowPathsText || ''),
              blockHtml: values.blockHtml || '',
              disabledRuleGroups:
                configQuery.data?.config.disabledRuleGroups ?? [],
            });
          }}
        >
          <div className="waf-policy-grid">
            <Card size="small" title="基础与 CC 防护">
              <Space size={SPACING.xl} wrap>
                <Form.Item
                  name="enabled"
                  label="全局 WAF 总开关"
                  valuePropName="checked"
                >
                  <Switch />
                </Form.Item>
                <Form.Item name="mode" label="默认防护模式">
                  <Select
                    style={{ width: 150 }}
                    options={[
                      { label: '拦截模式 (block)', value: 'block' },
                      { label: '仅观察 (observe)', value: 'observe' },
                    ]}
                  />
                </Form.Item>
                <Form.Item name="retentionDays" label="日志保留（天）">
                  <InputNumber min={1} max={365} />
                </Form.Item>
              </Space>

              <Space size={SPACING.md} wrap>
                <Form.Item
                  name="ccEnabled"
                  label="CC 频率限制"
                  valuePropName="checked"
                >
                  <Switch />
                </Form.Item>
                <Form.Item name="ccWindowSeconds" label="窗口（秒）">
                  <InputNumber min={1} max={3600} />
                </Form.Item>
                <Form.Item name="ccMaxRequests" label="单 IP 上限">
                  <InputNumber min={1} max={100000} />
                </Form.Item>
                <Form.Item name="ccAction" label="触发动作">
                  <Select
                    style={{ width: 130 }}
                    options={[
                      { label: '拦截 (403)', value: 'block' },
                      { label: 'JS 质询', value: 'challenge' },
                      { label: '仅观察', value: 'observe' },
                    ]}
                  />
                </Form.Item>
              </Space>
            </Card>

            <Card size="small" title="高频自动封禁与防火墙联动">
              <Space size={SPACING.xl} wrap>
                <Form.Item
                  name="autoBanEnabled"
                  label="启用自动封禁"
                  valuePropName="checked"
                >
                  <Switch />
                </Form.Item>
                <Form.Item
                  name="syncFirewall"
                  label="联动系统防火墙 (nftables)"
                  valuePropName="checked"
                >
                  <Switch />
                </Form.Item>
              </Space>
              <Space size={SPACING.md} wrap>
                <Form.Item name="autoBanWindow" label="检测窗口（秒）">
                  <InputNumber min={10} max={86400} />
                </Form.Item>
                <Form.Item name="autoBanThreshold" label="触发次数">
                  <InputNumber min={1} max={10000} />
                </Form.Item>
                <Form.Item
                  name="autoBanDuration"
                  label="封禁时长（秒，0=永久）"
                >
                  <InputNumber min={0} max={86400 * 365} />
                </Form.Item>
              </Space>
            </Card>
          </div>

          <div className="waf-policy-grid" style={{ marginTop: SPACING.lg }}>
            <Form.Item
              name="allowIPsText"
              label="全局 IP / CIDR 白名单（每行一条）"
            >
              <Input.TextArea
                rows={4}
                placeholder="127.0.0.1&#10;192.168.1.0/24"
              />
            </Form.Item>
            <Form.Item
              name="denyIPsText"
              label="全局 IP / CIDR 黑名单（每行一条）"
            >
              <Input.TextArea rows={4} placeholder="203.0.113.10" />
            </Form.Item>
          </div>

          <Form.Item
            name="allowPathsText"
            label="全局 URL 前缀白名单（每行一条，以 / 开头）"
          >
            <Input.TextArea
              rows={2}
              placeholder="/.well-known/acme-challenge"
            />
          </Form.Item>

          <Form.Item
            name="blockHtml"
            label="自定义 403 拦截页 HTML（留空使用内置默认页面）"
          >
            <Input.TextArea
              rows={3}
              placeholder="留空则使用 ezpanel 默认拦截页面"
            />
          </Form.Item>
        </Form>
      </Card>

      <Card
        title="实时封禁 IP 名单"
        extra={
          <Space wrap>
            <Input
              allowClear
              placeholder="搜索 IP"
              style={{ width: LAYOUT.dashboardNetworkSelectWidth }}
              value={banSearch}
              onChange={(e) => setBanSearch(e.target.value)}
            />
            <Button
              type="primary"
              danger
              onClick={() => {
                banForm.setFieldsValue({
                  ip: '',
                  duration: 86400,
                  reason: '管理员手动封禁',
                  syncFirewall: false,
                });
                setBanModal(true);
              }}
            >
              手动封禁 IP
            </Button>
          </Space>
        }
      >
        <Table
          rowKey="id"
          scroll={{ x: 'max-content' }}
          loading={bansQuery.isLoading}
          dataSource={bansQuery.data?.list ?? []}
          pagination={false}
          locale={{ emptyText: '当前没有生效中的封禁记录' }}
          columns={[
            {
              title: '封禁 IP',
              dataIndex: 'ip',
              width: 190,
              render: (ip: string, row) => (
                <Space direction="vertical" size={0}>
                  <Typography.Text style={{ fontFamily: MONO_FONT }}>
                    {ip}
                  </Typography.Text>
                  {row.location ? (
                    <Typography.Text type="secondary">
                      {row.location}
                    </Typography.Text>
                  ) : null}
                </Space>
              ),
            },
            {
              title: '来源',
              dataIndex: 'source',
              width: 90,
              render: (s: string) => (
                <Tag color={s === 'auto' ? 'orange' : 'blue'}>
                  {s === 'auto' ? '自动' : '手动'}
                </Tag>
              ),
            },
            { title: '封禁原因', dataIndex: 'reason' },
            {
              title: '系统防火墙',
              dataIndex: 'nftSynced',
              width: 110,
              render: (synced: boolean) => (
                <Tag color={synced ? 'green' : 'default'}>
                  {synced ? '已下沉' : '仅 WAF'}
                </Tag>
              ),
            },
            {
              title: '解封时间',
              dataIndex: 'expiresAt',
              width: 170,
              render: (ts: number) =>
                ts === 0
                  ? '永久封禁'
                  : dayjs.unix(ts).format('YYYY-MM-DD HH:mm:ss'),
            },
            {
              title: '操作',
              width: 170,
              render: (_, row) => (
                <Space size={4}>
                  {!row.nftSynced ? (
                    <Button
                      type="link"
                      size="small"
                      loading={syncFirewallMutation.isPending}
                      onClick={() => syncFirewallMutation.mutate(row.id)}
                    >
                      同步防火墙
                    </Button>
                  ) : null}
                  <Popconfirm
                    title={`确认解封 ${row.ip}？`}
                    onConfirm={() => deleteBanMutation.mutate(row.id)}
                  >
                    <Button type="link" size="small" danger>
                      解封
                    </Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Card>

      <Modal
        title="手动封禁 IP"
        width={LAYOUT.modalWidth}
        open={banModal}
        confirmLoading={createBanMutation.isPending}
        onCancel={() => setBanModal(false)}
        onOk={() => void banForm.submit()}
      >
        <Form
          form={banForm}
          layout="vertical"
          onFinish={(values) => createBanMutation.mutate(values)}
        >
          <Form.Item
            name="ip"
            label="目标 IP"
            rules={[{ required: true, message: '请输入 IPv4 或 IPv6 地址' }]}
          >
            <Input placeholder="例如：203.0.113.10" />
          </Form.Item>
          <Form.Item name="duration" label="封禁时长">
            <Select
              options={[
                { label: '30 分钟', value: 1800 },
                { label: '6 小时', value: 21600 },
                { label: '24 小时', value: 86400 },
                { label: '7 天', value: 604800 },
                { label: '永久封禁', value: 0 },
              ]}
            />
          </Form.Item>
          <Form.Item name="reason" label="封禁原因">
            <Input maxLength={128} />
          </Form.Item>
          <Form.Item
            name="syncFirewall"
            label="同步写入系统防火墙（影响整台服务器）"
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </Flex>
  );
}
