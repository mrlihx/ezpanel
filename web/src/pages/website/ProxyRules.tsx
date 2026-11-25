import {
  Alert,
  App,
  Badge,
  Button,
  Collapse,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Switch,
} from 'antd';
import { useState } from 'react';
import { MinusCircleOutlined, PlusOutlined } from '@ant-design/icons';
import type { ProxyRoute } from '../../api/website';
import ContentSizedTable from '../../components/ContentSizedTable';
import { LAYOUT, SPACING } from '../../styles/tokens';

type ProxyForm = Omit<ProxyRoute, 'rateLimit' | 'cacheTTL'> & {
  cacheTTL?: number | null;
  requestsPerSecond?: number | null;
  burst?: number | null;
};

export default function ProxyRules({
  routes,
  onSave,
  pending,
  disabled,
  error,
}: {
  routes: ProxyRoute[];
  onSave: (routes: ProxyRoute[]) => Promise<boolean>;
  pending: boolean;
  disabled: boolean;
  error: string;
}) {
  const { modal } = App.useApp();
  const [form] = Form.useForm<ProxyForm>();
  const [editing, setEditing] = useState<number>();
  const requestsPerSecond = Form.useWatch('requestsPerSecond', form);
  function open(index: number) {
    const route = routes[index];
    form.resetFields();
    form.setFieldsValue({
      ...(route ?? {
        name: '',
        path: routes.length ? '' : '/',
        target: '',
        host: '',
        enabled: true,
      }),
      webSocket: route?.webSocket ?? true,
      cacheTTL: route?.cacheTTL ?? 0,
      requestsPerSecond: route?.rateLimit?.requestsPerSecond ?? 0,
      burst: route?.rateLimit?.burst ?? 0,
    });
    setEditing(index);
  }
  return (
    <Space direction="vertical" style={{ width: '100%' }}>
      <Button
        type="primary"
        icon={<PlusOutlined />}
        disabled={disabled || pending || routes.length >= 50}
        onClick={() => open(routes.length)}
      >
        添加反向代理
      </Button>
      <ContentSizedTable<ProxyRoute>
        fillColumn="target"
        rowKey="path"
        dataSource={routes}
        pagination={false}
        locale={{ emptyText: '暂无反向代理规则' }}
        columns={[
          { title: '名称', dataIndex: 'name', width: 156 },
          { title: '代理目录', dataIndex: 'path', width: 150 },
          { title: '目标 URL', dataIndex: 'target' },
          {
            title: '状态',
            key: 'status',
            render: (_, route) => (
              <Badge
                status={route.enabled ? 'success' : 'default'}
                text={route.enabled ? '已启用' : '已停用'}
              />
            ),
          },
          {
            title: '操作',
            render: (_, route, index) => (
              <Space size={12}>
                <Button
                  type="link"
                  disabled={disabled || pending}
                  onClick={() => open(index)}
                >
                  修改
                </Button>
                <Button
                  type="link"
                  danger
                  disabled={disabled || pending}
                  onClick={() =>
                    modal.confirm({
                      title: `删除反向代理「${route.name}」？`,
                      okButtonProps: { danger: true },
                      onOk: async () => {
                        await onSave(
                          routes.filter((item) => item.path !== route.path),
                        );
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
      <Modal
        open={editing !== undefined}
        forceRender
        centered
        width={LAYOUT.databaseModalWidth}
        title={editing === routes.length ? '添加反向代理' : '修改反向代理'}
        confirmLoading={pending}
        maskClosable={false}
        styles={{ body: { maxHeight: '70dvh', overflowY: 'auto' } }}
        onCancel={() => setEditing(undefined)}
        onOk={() => form.submit()}
        okText="保存"
        cancelText="取消"
      >
        {error && editing !== undefined && (
          <Alert
            showIcon
            type="error"
            message={error}
            style={{ marginBottom: SPACING.lg }}
          />
        )}
        <Form
          form={form}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1, style: { minWidth: 0 } }}
          colon={false}
          onFinish={async ({
            cacheTTL,
            requestsPerSecond,
            burst,
            ...values
          }) => {
            if (editing === undefined) return;
            const next = [...routes];
            next[editing] = {
              ...values,
              webSocket: values.webSocket ?? true,
              cacheTTL: cacheTTL || undefined,
              rateLimit: requestsPerSecond
                ? { requestsPerSecond, burst: burst ?? 0 }
                : undefined,
            };
            if (await onSave(next)) setEditing(undefined);
          }}
        >
          <Form.Item name="enabled" label="开启代理" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item
            name="name"
            label="代理名称"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input maxLength={150} />
          </Form.Item>
          <Form.Item
            name="path"
            label="代理目录"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input placeholder="/api/" maxLength={255} />
          </Form.Item>
          <Form.Item
            name="target"
            label="目标 URL"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input placeholder="http://127.0.0.1:8080/" />
          </Form.Item>
          <Form.Item name="host" label="发送域名">
            <Input placeholder="默认使用目标主机，可填写域名或 $host" />
          </Form.Item>
          <Collapse
            size="small"
            items={[
              {
                key: 'advanced',
                label: '高级设置',
                forceRender: true,
                children: (
                  <>
                    <Form.Item
                      name="webSocket"
                      label="WebSocket"
                      valuePropName="checked"
                    >
                      <Switch />
                    </Form.Item>
                    <Form.Item
                      name="cacheTTL"
                      label="缓存时长"
                      extra="填 0 关闭。带 Cookie、Authorization 或 Upgrade 的请求会绕过缓存，上游缓存响应头优先。"
                    >
                      <InputNumber
                        min={0}
                        max={86400}
                        precision={0}
                        suffix="秒"
                      />
                    </Form.Item>
                    <Form.Item
                      name="requestsPerSecond"
                      label="请求限速"
                      extra="按客户端 IP 统计，填 0 关闭。超限返回 HTTP 429。"
                    >
                      <InputNumber
                        min={0}
                        max={10000}
                        precision={0}
                        suffix="次/秒"
                      />
                    </Form.Item>
                    <Form.Item name="burst" label="允许突发">
                      <InputNumber
                        min={0}
                        max={10000}
                        precision={0}
                        suffix="次"
                        disabled={!requestsPerSecond}
                      />
                    </Form.Item>
                    <Form.Item
                      label="自定义 Header"
                      extra="填写固定值，留空可移除同名 Header。"
                    >
                      <Form.List name="headers">
                        {(fields, { add, remove }) => (
                          <>
                            {fields.map(({ key, name }) => (
                              <Flex
                                key={key}
                                gap={SPACING.sm}
                                align="start"
                                style={{ marginBottom: SPACING.sm }}
                              >
                                <Form.Item name={[name, 'direction']} noStyle>
                                  <Select
                                    aria-label="Header 方向"
                                    popupMatchSelectWidth={false}
                                    options={[
                                      { value: 'request', label: '请求' },
                                      { value: 'response', label: '响应' },
                                    ]}
                                  />
                                </Form.Item>
                                <Form.Item
                                  name={[name, 'name']}
                                  style={{ marginBottom: 0, flex: 1 }}
                                  rules={[
                                    {
                                      required: true,
                                      pattern:
                                        /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/,
                                      message: 'Header 名称无效',
                                    },
                                  ]}
                                >
                                  <Input
                                    aria-label="Header 名称"
                                    placeholder="名称"
                                    maxLength={64}
                                  />
                                </Form.Item>
                                <Form.Item name={[name, 'value']} noStyle>
                                  <Input
                                    aria-label="Header 值"
                                    placeholder="值"
                                    maxLength={2048}
                                  />
                                </Form.Item>
                                <Button
                                  type="text"
                                  icon={<MinusCircleOutlined />}
                                  aria-label="移除 Header"
                                  onClick={() => remove(name)}
                                />
                              </Flex>
                            ))}
                            <Button
                              icon={<PlusOutlined />}
                              disabled={fields.length >= 20}
                              onClick={() =>
                                add({
                                  direction: 'request',
                                  name: '',
                                  value: '',
                                })
                              }
                            >
                              添加 Header
                            </Button>
                          </>
                        )}
                      </Form.List>
                    </Form.Item>
                  </>
                ),
              },
            ]}
          />
        </Form>
      </Modal>
    </Space>
  );
}
