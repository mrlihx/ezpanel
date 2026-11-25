import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import LogText from '../../components/LogText';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Row,
  Select,
  Skeleton,
  Space,
  Statistic,
  Switch,
  Tag,
  Typography,
} from 'antd';
import { request } from '../../api/client';
import { databaseInstances } from '../../api/software';
import ContentSizedTable from '../../components/ContentSizedTable';

const root = '/api/software/protection';
const queryKey = ['fail2ban-protection'];
const ports = { sshd: 22, ftpd: 21, mysql: 3306 };
const names = { sshd: 'sshd', ftpd: 'ftpd（Pure-FTPd）', mysql: 'mysql' };
type Service = keyof typeof ports;
type Rule = {
  service: Service;
  enabled: boolean;
  maxRetry: number;
  findTime: number;
  banTime: number;
  port: number;
  logPath?: string;
};
type IPList = 'blacklist' | 'whitelist';
type Protection = {
  installed: boolean;
  running: boolean;
  rules: Partial<Record<Service, Rule>>;
  blacklist: string[];
  whitelist: string[];
};

function useProtection() {
  const client = useQueryClient();
  const query = useQuery({
    queryKey,
    queryFn: () => request<Protection>(root, { silentError: true }),
  });
  const mutation = useMutation({
    mutationFn: (action: () => Promise<unknown>) => action(),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey });
      void client.invalidateQueries({ queryKey: ['software-catalog'] });
    },
  });
  return { query, mutation };
}

const saveRule = (rule: Rule) =>
  request(root + '/rules/' + rule.service, { method: 'PUT', data: rule });

function defaults(service: Service, port = ports[service]): Rule {
  return {
    service,
    enabled: true,
    maxRetry: 5,
    findTime: 300,
    banTime: 86400,
    port,
  };
}

export function Fail2banRules() {
  const { query, mutation } = useProtection();
  const { message } = App.useApp();
  const [editing, setEditing] = useState<Rule | 'new'>();
  const [logs, setLogs] = useState<Service>();
  if (query.isPending) return <Skeleton active />;
  if (query.error) return <Alert type="error" message={query.error.message} />;
  const { installed, running, rules } = query.data;
  const services = Object.keys(ports) as Service[];
  const rows = services.flatMap((service) => {
    const rule = rules[service];
    return rule?.maxRetry ? [rule] : [];
  });
  const available = services.filter((service) => !rules[service]?.maxRetry);
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {installed && !running && (
        <Alert type="info" showIcon message="Fail2ban 服务已停止" />
      )}
      <Button
        type="primary"
        disabled={!installed || !available.length}
        onClick={() => setEditing('new')}
      >
        创建规则
      </Button>
      <ContentSizedTable<Rule>
        rowKey="service"
        fillColumn="service"
        dataSource={rows}
        size="small"
        pagination={false}
        columns={[
          {
            title: '保护服务',
            dataIndex: 'service',
            render: (service: Service) => names[service],
          },
          {
            title: '状态',
            render: (_, rule) => (
              <Tag color={rule.enabled && running ? 'success' : 'default'}>
                {rule.enabled ? (running ? '已启用' : '服务已停止') : '已停用'}
              </Tag>
            ),
          },
          { title: '最大重试次数', dataIndex: 'maxRetry' },
          { title: '端口', dataIndex: 'port' },
          { title: '周期（秒）', dataIndex: 'findTime' },
          { title: '禁止时间（秒）', dataIndex: 'banTime' },
          {
            title: '操作',
            key: 'actions',
            render: (_, rule) => (
              <Space wrap>
                <Button
                  type="link"
                  size="small"
                  onClick={() => setEditing(rule)}
                >
                  修改
                </Button>
                <Button
                  type="link"
                  size="small"
                  onClick={() => setLogs(rule.service)}
                >
                  日志
                </Button>
                <Popconfirm
                  title={'删除 ' + names[rule.service] + ' 保护规则？'}
                  onConfirm={async () => {
                    await mutation.mutateAsync(() =>
                      request(root + '/rules/' + rule.service, {
                        method: 'DELETE',
                      }),
                    );
                    message.success('规则已删除');
                  }}
                >
                  <Button
                    type="link"
                    size="small"
                    danger
                    loading={mutation.isPending}
                  >
                    删除
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      {editing && (
        <RuleModal
          rule={editing === 'new' ? defaults(available[0]) : editing}
          options={editing === 'new' ? available : undefined}
          onClose={() => setEditing(undefined)}
          onSave={async (rule) => {
            await mutation.mutateAsync(() => saveRule(rule));
            setEditing(undefined);
            message.success('规则已保存');
          }}
          saving={mutation.isPending}
        />
      )}
      {logs && <RuleLogs service={logs} onClose={() => setLogs(undefined)} />}
    </Space>
  );
}

export function Fail2banIPList({ list }: { list: IPList }) {
  const { query, mutation } = useProtection();
  const { message } = App.useApp();
  const [draft, setDraft] = useState<string>();
  if (query.isPending) return <Skeleton active />;
  if (query.error) return <Alert type="error" message={query.error.message} />;
  const saved = query.data[list].join('\n');
  return (
    <Space
      direction="vertical"
      size="middle"
      style={{ width: '100%', maxWidth: 550 }}
    >
      <Input.TextArea
        aria-label={list === 'blacklist' ? 'IP 黑名单' : 'IP 白名单'}
        rows={10}
        value={draft ?? saved}
        placeholder={'192.0.2.1\n192.0.2.0/24\n2001:db8::/32'}
        onChange={(event) => setDraft(event.target.value)}
      />
      <Typography.Text type="secondary">
        {list === 'blacklist'
          ? '每行填写一个 IPv4、IPv6 或网段。名单生效后会阻断这些来源的入站流量。'
          : '每行填写一个 IPv4、IPv6 或网段。白名单地址会跳过自动封禁和此处的黑名单。'}
      </Typography.Text>
      <Button
        type="primary"
        disabled={
          !query.data.installed || draft === undefined || draft === saved
        }
        loading={mutation.isPending}
        onClick={async () => {
          await mutation.mutateAsync(() =>
            request(root + '/ips/' + list, {
              method: 'PUT',
              data: { addresses: draft },
            }),
          );
          setDraft(undefined);
          message.success('名单已保存');
        }}
      >
        保存
      </Button>
    </Space>
  );
}

export function SSHProtection({ port }: { port: number }) {
  const { query, mutation } = useProtection();
  const { message } = App.useApp();
  const [editing, setEditing] = useState(false);
  const [logs, setLogs] = useState(false);
  const configured = query.data?.rules.sshd;
  const rule = configured?.maxRetry
    ? { ...configured, port }
    : defaults('sshd', port);
  const enabled = Boolean(query.data?.running && configured?.enabled);
  async function save(values: Rule) {
    await mutation.mutateAsync(() => saveRule(values));
    setEditing(false);
    message.success('SSH安全防护已保存');
  }
  return (
    <Card title="SSH安全防护" size="small" loading={query.isPending}>
      {query.error ? (
        <Alert type="error" message={query.error.message} />
      ) : query.data?.installed ? (
        <Space wrap size="middle">
          <Switch
            aria-label="SSH安全防护"
            checked={enabled}
            loading={mutation.isPending}
            onChange={(checked) => void save({ ...rule, enabled: checked })}
          />
          <Button onClick={() => setEditing(true)}>配置</Button>
          <Button onClick={() => setLogs(true)}>日志</Button>
        </Space>
      ) : (
        <Link to="/apps?install=fail2ban">安装 Fail2ban</Link>
      )}
      {editing && (
        <RuleModal
          rule={{ ...rule, enabled: configured?.enabled ?? false }}
          ssh
          onClose={() => setEditing(false)}
          onSave={save}
          saving={mutation.isPending}
        />
      )}
      {logs && <RuleLogs service="sshd" onClose={() => setLogs(false)} />}
    </Card>
  );
}

function RuleModal({
  rule,
  options,
  ssh,
  onClose,
  onSave,
  saving,
}: {
  rule: Rule;
  options?: Service[];
  ssh?: boolean;
  onClose: () => void;
  onSave: (rule: Rule) => Promise<void>;
  saving: boolean;
}) {
  const [form] = Form.useForm<Rule>();
  const service = Form.useWatch('service', form) ?? rule.service;
  const mysqlInstances = useQuery({
    queryKey: ['database-instances', 'mysql'],
    queryFn: ({ signal }) => databaseInstances('mysql', signal),
    enabled: service === 'mysql',
  });
  return (
    <Modal
      open
      title={
        ssh ? 'SSH安全防护配置' : options ? '创建保护规则' : '修改保护规则'
      }
      onCancel={onClose}
      onOk={() => form.submit()}
      confirmLoading={saving}
      okText="保存"
      cancelText="取消"
    >
      <ApplicationSettingsForm<Rule>
        form={form}

        initialValues={rule}
        onFinish={(v) => void onSave({ ...rule, ...v })}
      >
        {!ssh && (
          <>
            <Form.Item name="service" label="保护服务">
              <Select<Service>
                disabled={!options}
                options={(options ?? [service]).map((value) => ({
                  value,
                  label: names[value],
                }))}
                onChange={(value) => form.setFieldValue('port', ports[value])}
              />
            </Form.Item>
            <Form.Item name="enabled" label="启用保护" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name="port" label="端口" rules={[{ required: true }]}>
              <InputNumber
                min={1}
                max={65535}
                precision={0}
                style={{ width: '100%' }}
              />
            </Form.Item>
          </>
        )}
        <Form.Item
          name="maxRetry"
          label="最大重试次数"
          rules={[{ required: true }]}
        >
          <InputNumber
            min={1}
            max={999}
            precision={0}
            style={{ width: '100%' }}
          />
        </Form.Item>
        <Form.Item name="findTime" label="周期" rules={[{ required: true }]}>
          <InputNumber
            min={1}
            precision={0}
            addonAfter="秒"
            style={{ width: '100%' }}
          />
        </Form.Item>
        <Form.Item name="banTime" label="禁止时间" rules={[{ required: true }]}>
          <InputNumber
            min={1}
            precision={0}
            addonAfter="秒"
            style={{ width: '100%' }}
          />
        </Form.Item>
        {service === 'mysql' && !!mysqlInstances.data?.list.length && (
          <Form.Item label="MySQL 实例">
            <Select
              placeholder="选择实例，带入端口和日志路径"
              options={mysqlInstances.data.list.map((item) => ({
                value: item.id,
                label: item.name + ' · ' + item.port,
              }))}
              onChange={(id) => {
                const instance = mysqlInstances.data!.list.find(
                  (item) => item.id === id,
                )!;
                form.setFieldsValue({
                  port: instance.port,
                  logPath:
                    instance.errorLogPath ??
                    `/opt/ezpanel/log/mysql/${instance.id}/error.log`,
                });
              }}
            />
          </Form.Item>
        )}
        {service === 'mysql' && (
          <Form.Item
            name="logPath"
            label="认证失败日志"
            extra="填写宿主机上可读取的 MySQL 错误日志路径，并将 log_error_verbosity 设为 3。"
          >
            <Input placeholder="/var/log/mysql/error.log" />
          </Form.Item>
        )}
      </ApplicationSettingsForm>
    </Modal>
  );
}

type RuleLogView = {
  log: string;
  ips: string[] | null;
  stats?: {
    totalBanned: number;
    totalFailed: number;
    currentlyBanned: number;
    currentlyFailed: number;
  };
};

function RuleLogs({
  service,
  onClose,
}: {
  service: Service;
  onClose: () => void;
}) {
  const { message } = App.useApp();
  const url = root + '/rules/' + service;
  const logs = useQuery({
    queryKey: ['fail2ban-logs', service],
    queryFn: () => request<RuleLogView>(url + '/logs', { silentError: true }),
  });
  const unban = useMutation({
    mutationFn: (ip: string) =>
      request(url + '/unban', { method: 'POST', data: { ip } }),
    onSuccess: async () => {
      await logs.refetch();
      message.success('已解除封禁');
    },
  });
  if (service === 'sshd') {
    const stats = logs.data?.stats;
    return (
      <Modal
        open
        title="防爆破日志"
        onCancel={onClose}
        width={760}
        footer={
          <Button loading={logs.isFetching} onClick={() => void logs.refetch()}>
            刷新
          </Button>
        }
      >
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          {logs.error && <Alert type="error" message={logs.error.message} />}
          {logs.isPending ? (
            <Skeleton active />
          ) : (
            logs.data && (
              <>
                <Card size="small">
                  <Row gutter={[16, 16]} justify="center">
                    {[
                      { title: '封锁总次数', value: stats?.totalBanned },
                      { title: '连接失败总数', value: stats?.totalFailed },
                      {
                        title: '当前封锁 IP 数',
                        value: stats?.currentlyBanned,
                      },
                      {
                        title: '当前失败 IP 数',
                        value: stats?.currentlyFailed,
                      },
                    ].map(({ title, value }) => (
                      <Col
                        key={title}
                        xs={12}
                        sm={6}
                        style={{ textAlign: 'center' }}
                      >
                        <Statistic title={title} value={value ?? '—'} />
                      </Col>
                    ))}
                  </Row>
                </Card>
                <ContentSizedTable<{ ip: string }>
                  rowKey="ip"
                  fillColumn="ip"
                  size="small"
                  dataSource={(logs.data.ips ?? []).map((ip) => ({ ip }))}
                  pagination={false}
                  scroll={{ y: 320 }}
                  locale={{
                    emptyText: stats
                      ? '当前没有封锁的 IP'
                      : '当前无可用的 SSH 防护统计',
                  }}
                  columns={[
                    { title: 'IP', dataIndex: 'ip' },
                    {
                      title: '操作',
                      key: 'action',
                      align: 'right',
                      render: (_, { ip }) => (
                        <Popconfirm
                          title={'解除 ' + ip + ' 的封锁？'}
                          onConfirm={() => unban.mutateAsync(ip)}
                        >
                          <Button
                            type="link"
                            size="small"
                            disabled={unban.isPending}
                          >
                            解除封锁
                          </Button>
                        </Popconfirm>
                      ),
                    },
                  ]}
                />
              </>
            )
          )}
        </Space>
      </Modal>
    );
  }

  return (
    <Modal
      open
      title={names[service] + ' 保护日志'}
      footer={null}
      onCancel={onClose}
      width={760}
    >
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Button loading={logs.isFetching} onClick={() => void logs.refetch()}>
          刷新
        </Button>
        {logs.error && <Alert type="error" message={logs.error.message} />}
        {!!logs.data?.ips?.length && (
          <Space wrap>
            <Typography.Text strong>当前封禁</Typography.Text>
            {logs.data.ips.map((ip) => (
              <Popconfirm
                key={ip}
                title={'解除 ' + ip + ' 的封禁？'}
                onConfirm={() => unban.mutateAsync(ip)}
              >
                <Button size="small" disabled={unban.isPending}>
                  {ip} · 解除
                </Button>
              </Popconfirm>
            ))}
          </Space>
        )}
        {logs.isPending ? (
          <Skeleton active />
        ) : logs.data?.log ? (
          <LogText
            style={{
              margin: 0,
              maxHeight: '50vh',
              overflow: 'auto',
              whiteSpace: 'pre-wrap',
              overflowWrap: 'anywhere',
            }}
          >
            {logs.data.log}
          </LogText>
        ) : (
          !logs.error && (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="暂无保护日志"
            />
          )
        )}
      </Space>
    </Modal>
  );
}
