import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  App,
  Button,
  Card,
  Checkbox,
  Drawer,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import {
  listWAFSites,
  updateWAFSitePolicy,
  type WAFSitePolicy,
  type WAFSiteRow,
} from '../../api/waf';
import { LAYOUT, SPACING } from '../../styles/tokens';

const RULE_GROUP_OPTIONS = [
  { label: '敏感路径与泄露', value: 'sensitive' },
  { label: 'SQL 注入', value: 'sqli' },
  { label: 'XSS 跨站脚本', value: 'xss' },
  { label: '命令注入与 RCE', value: 'rce' },
  { label: '目录穿越', value: 'traversal' },
  { label: '恶意扫描器', value: 'scanner' },
  { label: '高危脚本与上传', value: 'upload' },
];

function splitLines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function SitePolicyForm({
  policy,
  disabled,
  onSaved,
}: {
  policy: WAFSitePolicy;
  disabled?: boolean;
  onSaved?: () => void;
}) {
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [form] = Form.useForm();
  const inheritGlobal =
    Form.useWatch('inheritGlobal', form) ?? policy.inheritGlobal;
  const save = useMutation({
    mutationFn: (value: WAFSitePolicy) =>
      updateWAFSitePolicy(policy.siteId, value),
    onSuccess: (value) => {
      queryClient.setQueryData(['waf-site', policy.siteId], value);
      void queryClient.invalidateQueries({ queryKey: ['waf-sites'] });
      void queryClient.invalidateQueries({ queryKey: ['waf-overview'] });
      void message.success('站点防护策略已保存');
      onSaved?.();
    },
  });
  return (
    <Form
      form={form}
      initialValues={{
        inheritGlobal: policy.inheritGlobal,
        mode: policy.mode,
        ccEnabled: policy.ccEnabled,
        ccWindowSeconds: policy.ccWindowSeconds,
        ccMaxRequests: policy.ccMaxRequests,
        ccAction: policy.ccAction,
        allowIPsText: (policy.allowIPs ?? []).join('\n'),
        denyIPsText: (policy.denyIPs ?? []).join('\n'),
        allowPathsText: (policy.allowPaths ?? []).join('\n'),
        denyPathsText: (policy.denyPaths ?? []).join('\n'),
        disabledGroups: policy.disabledGroups ?? [],
      }}
      labelCol={{ flex: LAYOUT.formLabelWidth }}
      wrapperCol={{ flex: 1 }}
      colon={false}
      disabled={disabled}
      onFinish={(values) => {
        save.mutate({
          siteId: policy.siteId,
          inheritGlobal: values.inheritGlobal,
          mode: values.mode ?? policy.mode,
          ccEnabled: values.ccEnabled ?? policy.ccEnabled,
          ccWindowSeconds: values.ccWindowSeconds ?? policy.ccWindowSeconds,
          ccMaxRequests: values.ccMaxRequests ?? policy.ccMaxRequests,
          ccAction: values.ccAction ?? policy.ccAction,
          allowIPs: splitLines(values.allowIPsText || ''),
          denyIPs: splitLines(values.denyIPsText || ''),
          allowPaths: splitLines(values.allowPathsText || ''),
          denyPaths: splitLines(values.denyPathsText || ''),
          disabledGroups: values.disabledGroups || [],
        });
      }}
    >
      <Form.Item
        name="inheritGlobal"
        label="继承全局 WAF 策略"
        valuePropName="checked"
      >
        <Switch />
      </Form.Item>

      {!inheritGlobal ? (
        <>
          <Form.Item name="mode" label="站点防护模式">
            <Select
              options={[
                { label: '拦截模式 (block)', value: 'block' },
                { label: '仅观察记录 (observe)', value: 'observe' },
                { label: '关闭防护 (off)', value: 'off' },
              ]}
            />
          </Form.Item>
          <Space style={{ width: '100%' }} size={SPACING.md} wrap>
            <Form.Item
              name="ccEnabled"
              label="独立 CC 防护"
              valuePropName="checked"
            >
              <Switch />
            </Form.Item>
            <Form.Item name="ccWindowSeconds" label="窗口（秒）">
              <InputNumber min={1} max={3600} />
            </Form.Item>
            <Form.Item name="ccMaxRequests" label="最大请求数">
              <InputNumber min={1} max={100000} />
            </Form.Item>
            <Form.Item name="ccAction" label="CC 动作">
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
        </>
      ) : null}

      <Form.Item name="disabledGroups" label="该站点停用的内置规则分组">
        <Checkbox.Group options={RULE_GROUP_OPTIONS} />
      </Form.Item>

      <Form.Item name="allowIPsText" label="站点 IP / CIDR 白名单（每行一条）">
        <Input.TextArea
          rows={3}
          placeholder="127.0.0.1&#10;10.0.0.0/8"
        />
      </Form.Item>
      <Form.Item name="denyIPsText" label="站点 IP / CIDR 黑名单（每行一条）">
        <Input.TextArea rows={3} placeholder="198.51.100.12" />
      </Form.Item>
      <Form.Item
        name="allowPathsText"
        label="站点放行 URL 前缀（每行一条，以 / 开头）"
      >
        <Input.TextArea rows={3} placeholder="/api/webhook" />
      </Form.Item>
      <Form.Item
        name="denyPathsText"
        label="站点拦截 URL 前缀（每行一条，以 / 开头）"
      >
        <Input.TextArea rows={3} placeholder="/admin-backup" />
      </Form.Item>
      <Form.Item>
        <Button type="primary" htmlType="submit" loading={save.isPending}>
          保存配置
        </Button>
      </Form.Item>
    </Form>
  );
}

export default function Sites() {
  const [editing, setEditing] = useState<WAFSiteRow | null>(null);
  const sitesQuery = useQuery({
    queryKey: ['waf-sites'],
    queryFn: listWAFSites,
  });

  return (
    <Card title="站点独立防护策略">
      <Table
        rowKey="siteId"
        scroll={{ x: 'max-content' }}
        loading={sitesQuery.isLoading}
        dataSource={sitesQuery.data?.list ?? []}
        pagination={false}
        locale={{ emptyText: '暂无托管站点' }}
        columns={[
          {
            title: '站点名称',
            render: (_, row) => (
              <Space direction="vertical" size={0}>
                <Typography.Text strong>{row.name}</Typography.Text>
                <Typography.Text type="secondary">
                  {row.domains.slice(0, 3).join('、') || '-'}
                </Typography.Text>
              </Space>
            ),
          },
          {
            title: '策略来源',
            width: 120,
            render: (_, row) => (
              <Tag color={row.policy.inheritGlobal ? 'blue' : 'purple'}>
                {row.policy.inheritGlobal ? '继承全局' : '独立策略'}
              </Tag>
            ),
          },
          {
            title: '当前模式',
            width: 120,
            render: (_, row) => {
              if (row.policy.inheritGlobal) {
                return <Tag>跟随全局</Tag>;
              }
              return (
                <Tag
                  color={
                    row.policy.mode === 'block'
                      ? 'red'
                      : row.policy.mode === 'observe'
                        ? 'orange'
                        : 'default'
                  }
                >
                  {row.policy.mode === 'block'
                    ? '拦截'
                    : row.policy.mode === 'observe'
                      ? '仅观察'
                      : '已关闭'}
                </Tag>
              );
            },
          },
          {
            title: '今日拦截',
            dataIndex: 'todayBlocked',
            width: 110,
            render: (v: number) => (
              <Tag color={v > 0 ? 'red' : 'default'}>{v}</Tag>
            ),
          },
          {
            title: '操作',
            width: 110,
            render: (_, row) => (
              <Button type="link" size="small" onClick={() => setEditing(row)}>
                配置策略
              </Button>
            ),
          },
        ]}
      />

      <Drawer
        title={editing ? `站点防护配置 · ${editing.name}` : '站点防护配置'}
        width={LAYOUT.drawerWidth}
        open={editing != null}
        onClose={() => setEditing(null)}
      >
        {editing && (
          <SitePolicyForm
            key={editing.siteId}
            policy={editing.policy}
            onSaved={() => setEditing(null)}
          />
        )}
      </Drawer>
    </Card>
  );
}
