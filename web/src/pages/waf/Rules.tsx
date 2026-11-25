import { useState } from 'react';
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
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import dayjs from 'dayjs';
import {
  createWAFCustomRule,
  deleteWAFCustomRule,
  deleteWAFExemption,
  listWAFExemptions,
  listWAFRules,
  listWAFSites,
  updateWAFBuiltinRule,
  updateWAFCustomRule,
  type WAFCustomRule,
} from '../../api/waf';
import { LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';

export default function Rules() {
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<WAFCustomRule | null>(null);
  const [form] = Form.useForm();
  const matchField = Form.useWatch('matchField', form);

  const rulesQuery = useQuery({
    queryKey: ['waf-rules'],
    queryFn: listWAFRules,
  });

  const sitesQuery = useQuery({
    queryKey: ['waf-sites'],
    queryFn: listWAFSites,
  });

  const exemptionsQuery = useQuery({
    queryKey: ['waf-exemptions'],
    queryFn: listWAFExemptions,
  });

  const builtinMutation = useMutation({
    mutationFn: ({
      id,
      enabled,
      action,
    }: {
      id: string;
      enabled: boolean;
      action: 'block' | 'observe';
    }) => updateWAFBuiltinRule(id, { enabled, action }),
    onSuccess: () => {
      message.success('内置规则已热更新');
      void queryClient.invalidateQueries({ queryKey: ['waf-rules'] });
    },
  });

  const saveCustomMutation = useMutation({
    mutationFn: (values: Omit<WAFCustomRule, 'id'>) =>
      editing
        ? updateWAFCustomRule(editing.id, values)
        : createWAFCustomRule(values),
    onSuccess: () => {
      message.success(editing ? '自定义规则已更新' : '自定义规则已创建');
      setModalOpen(false);
      setEditing(null);
      void queryClient.invalidateQueries({ queryKey: ['waf-rules'] });
    },
  });

  const deleteCustomMutation = useMutation({
    mutationFn: deleteWAFCustomRule,
    onSuccess: () => {
      message.success('自定义规则已删除');
      void queryClient.invalidateQueries({ queryKey: ['waf-rules'] });
    },
  });

  const deleteExemptionMutation = useMutation({
    mutationFn: deleteWAFExemption,
    onSuccess: () => {
      message.success('已移除误报豁免');
      void queryClient.invalidateQueries({ queryKey: ['waf-exemptions'] });
    },
  });

  const siteOptions = [
    { label: '全局生效', value: '' },
    ...(sitesQuery.data?.list ?? []).map((s) => ({
      label: s.name,
      value: s.siteId,
    })),
  ];

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({
      name: '',
      siteId: '',
      enabled: true,
      priority: 100,
      action: 'block',
      matchField: 'uri',
      matchOperator: 'contains',
      matchValue: '',
      headerName: '',
      remark: '',
    });
    setModalOpen(true);
  };

  const openEdit = (row: WAFCustomRule) => {
    setEditing(row);
    form.resetFields();
    form.setFieldsValue(row);
    setModalOpen(true);
  };

  return (
    <Flex vertical gap={SPACING.lg}>
      <Card
        title="自定义规则"
        extra={
          <Button type="primary" onClick={openCreate}>
            新增自定义规则
          </Button>
        }
      >
        <Table
          rowKey="id"
          scroll={{ x: 'max-content' }}
          loading={rulesQuery.isLoading}
          dataSource={rulesQuery.data?.custom ?? []}
          pagination={false}
          locale={{ emptyText: '暂无自定义规则' }}
          columns={[
            { title: '优先级', dataIndex: 'priority', width: 80 },
            { title: '名称', dataIndex: 'name', width: 180 },
            {
              title: '匹配条件',
              render: (_, row) => (
                <Typography.Text style={{ fontFamily: MONO_FONT }}>
                  {row.matchField}
                  {row.matchField === 'header'
                    ? `[${row.headerName}]`
                    : ''}{' '}
                  {row.matchOperator} {row.matchValue}
                </Typography.Text>
              ),
            },
            {
              title: '动作',
              dataIndex: 'action',
              width: 90,
              render: (v: string) => (
                <Tag
                  color={
                    v === 'block' ? 'red' : v === 'allow' ? 'green' : 'orange'
                  }
                >
                  {v === 'block' ? '拦截' : v === 'allow' ? '放行' : '观察'}
                </Tag>
              ),
            },
            {
              title: '状态',
              dataIndex: 'enabled',
              width: 80,
              render: (v: boolean) => (
                <Tag color={v ? 'blue' : 'default'}>{v ? '启用' : '停用'}</Tag>
              ),
            },
            {
              title: '操作',
              width: 140,
              render: (_, row) => (
                <Space size={4}>
                  <Button
                    type="link"
                    size="small"
                    onClick={() => openEdit(row)}
                  >
                    编辑
                  </Button>
                  <Popconfirm
                    title="确认删除此规则？"
                    onConfirm={() => deleteCustomMutation.mutate(row.id)}
                  >
                    <Button type="link" size="small" danger>
                      删除
                    </Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Card>

      <Card title="内置安全规则库">
        <Table
          rowKey="id"
          scroll={{ x: 'max-content' }}
          loading={rulesQuery.isLoading}
          dataSource={rulesQuery.data?.builtin ?? []}
          pagination={false}
          columns={[
            {
              title: '分类',
              dataIndex: 'groupLabel',
              width: 140,
              render: (v: string) => <Tag>{v}</Tag>,
            },
            {
              title: '规则名称',
              render: (_, row) => (
                <Space direction="vertical" size={0}>
                  <Typography.Text strong>{row.name}</Typography.Text>
                  <Typography.Text type="secondary">
                    {row.description}
                  </Typography.Text>
                </Space>
              ),
            },
            {
              title: '处置动作',
              dataIndex: 'action',
              width: 130,
              render: (act: 'block' | 'observe', row) => (
                <Select
                  size="small"
                  value={act}
                  style={{ width: 100 }}
                  onChange={(next) =>
                    builtinMutation.mutate({
                      id: row.id,
                      enabled: row.enabled,
                      action: next,
                    })
                  }
                  options={[
                    { label: '拦截', value: 'block' },
                    { label: '仅观察', value: 'observe' },
                  ]}
                />
              ),
            },
            {
              title: '开关',
              dataIndex: 'enabled',
              width: 80,
              render: (enabled: boolean, row) => (
                <Switch
                  checked={enabled}
                  onChange={(checked) =>
                    builtinMutation.mutate({
                      id: row.id,
                      enabled: checked,
                      action: row.action,
                    })
                  }
                />
              ),
            },
          ]}
        />
      </Card>

      <Card title="误报放行（豁免列表）">
        <Table
          rowKey="id"
          scroll={{ x: 'max-content' }}
          loading={exemptionsQuery.isLoading}
          dataSource={exemptionsQuery.data?.list ?? []}
          pagination={false}
          locale={{ emptyText: '暂无误报豁免记录，可在攻击日志中一键添加' }}
          columns={[
            {
              title: '规则 ID',
              dataIndex: 'ruleId',
              width: 180,
              render: (v: string) => (
                <Typography.Text style={{ fontFamily: MONO_FONT }}>
                  {v}
                </Typography.Text>
              ),
            },
            {
              title: '放行 URI',
              dataIndex: 'uri',
              render: (v: string) => (
                <Typography.Text style={{ fontFamily: MONO_FONT }}>
                  {v}
                </Typography.Text>
              ),
            },
            { title: '备注', dataIndex: 'remark', width: 220 },
            {
              title: '添加时间',
              dataIndex: 'createdAt',
              width: 170,
              render: (v: string) => dayjs(v).format('YYYY-MM-DD HH:mm:ss'),
            },
            {
              title: '操作',
              width: 90,
              render: (_, row) => (
                <Popconfirm
                  title="移除后该规则将重新生效，确认移除？"
                  onConfirm={() => deleteExemptionMutation.mutate(row.id)}
                >
                  <Button type="link" size="small" danger>
                    移除
                  </Button>
                </Popconfirm>
              ),
            },
          ]}
        />
      </Card>

      <Modal
        title={editing ? '编辑自定义规则' : '新增自定义规则'}
        width={LAYOUT.modalWidth}
        open={modalOpen}
        confirmLoading={saveCustomMutation.isPending}
        onCancel={() => setModalOpen(false)}
        onOk={() => void form.submit()}
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={(values) => saveCustomMutation.mutate(values)}
        >
          <Form.Item
            name="name"
            label="规则名称"
            rules={[{ required: true, message: '请输入规则名称' }]}
          >
            <Input placeholder="例如：禁止访问后台测试接口" maxLength={64} />
          </Form.Item>
          <Flex gap={SPACING.md} wrap>
            <Form.Item
              name="siteId"
              label="生效站点"
              style={{ flex: '1 1 200px', minWidth: 0 }}
            >
              <Select options={siteOptions} />
            </Form.Item>
            <Form.Item name="priority" label="优先级（越小越先）">
              <InputNumber min={1} max={9999} />
            </Form.Item>
          </Flex>
          <Flex gap={SPACING.md} wrap>
            <Form.Item
              name="matchField"
              label="匹配字段"
              style={{ flex: '1 1 180px', minWidth: 0 }}
            >
              <Select
                options={[
                  { label: 'URI 路径', value: 'uri' },
                  { label: '查询参数 (Query)', value: 'query' },
                  { label: 'URI + 参数', value: 'args' },
                  { label: 'User-Agent', value: 'user_agent' },
                  { label: '请求方法 (Method)', value: 'method' },
                  { label: '客户端 IP', value: 'ip' },
                  { label: '请求头 (Header)', value: 'header' },
                ]}
              />
            </Form.Item>
            <Form.Item
              name="matchOperator"
              label="匹配方式"
              style={{ flex: '1 1 180px', minWidth: 0 }}
            >
              <Select
                options={[
                  { label: '包含 (contains)', value: 'contains' },
                  { label: '前缀 (prefix)', value: 'prefix' },
                  { label: '精确匹配 (exact)', value: 'exact' },
                  { label: '正则 (regex)', value: 'regex' },
                ]}
              />
            </Form.Item>
          </Flex>
          {matchField === 'header' ? (
            <Form.Item
              name="headerName"
              label="请求头名称"
              rules={[{ required: true, message: '请输入请求头名称' }]}
            >
              <Input placeholder="例如：X-Custom-Token" />
            </Form.Item>
          ) : null}
          <Form.Item
            name="matchValue"
            label="匹配内容"
            rules={[{ required: true, message: '请输入匹配值' }]}
          >
            <Input placeholder="例如：/admin/debug 或正则模式" />
          </Form.Item>
          <Flex gap={SPACING.md} wrap align="center">
            <Form.Item
              name="action"
              label="处置动作"
              style={{ flex: '1 1 200px', minWidth: 0 }}
            >
              <Select
                options={[
                  { label: '拦截 (403)', value: 'block' },
                  { label: '仅观察记录', value: 'observe' },
                  { label: '直接放行', value: 'allow' },
                ]}
              />
            </Form.Item>
            <Form.Item name="enabled" label="启用" valuePropName="checked">
              <Switch />
            </Form.Item>
          </Flex>
          <Form.Item name="remark" label="备注">
            <Input placeholder="可选说明" maxLength={128} />
          </Form.Item>
        </Form>
      </Modal>
    </Flex>
  );
}
