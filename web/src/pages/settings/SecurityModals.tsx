import { useState } from 'react';
import {
  Alert,
  Button,
  Empty,
  Flex,
  Form,
  Input,
  Modal,
  Select,
  Typography,
} from 'antd';
import { MONO_FONT, SPACING } from '../../styles/tokens';

export const UNAUTH_STATUS_OPTIONS = [
  { value: 404, label: '404 Not Found（默认）' },
  { value: 403, label: '403 Forbidden' },
  { value: 401, label: '401 Unauthorized' },
  { value: 444, label: '444 关闭连接（无响应）' },
  { value: 500, label: '500 Internal Server Error' },
  { value: 502, label: '502 Bad Gateway' },
  { value: 503, label: '503 Service Unavailable' },
  { value: 200, label: '200 OK' },
];

export function formatUnauthSummary(status: number, html: string) {
  const option = UNAUTH_STATUS_OPTIONS.find((item) => item.value === status);
  const label = option ? option.label : String(status || 404);
  if (status !== 444 && html.trim()) {
    return `${label}（已配置自定义 HTML）`;
  }
  return label;
}

export function parseUserAgentList(raw: string) {
  return raw
    .split(/[\r\n]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

export function UnauthResponseModal({
  open,
  unauthStatus,
  unauthHtml,
  saving,
  onSave,
  onClose,
}: {
  open: boolean;
  unauthStatus: number;
  unauthHtml: string;
  saving: boolean;
  onSave: (values: {
    unauthStatus: number;
    unauthHtml: string;
  }) => Promise<void>;
  onClose: () => void;
}) {
  const [form] = Form.useForm<{ unauthStatus: number; unauthHtml: string }>();
  const status = Form.useWatch('unauthStatus', form) ?? unauthStatus;
  return (
    <Modal
      open={open}
      title="未认证响应状态"
      width={600}
      okText="保存"
      confirmLoading={saving}
      destroyOnHidden
      onCancel={onClose}
      onOk={() => form.submit()}
    >
      <Form
        form={form}
        layout="horizontal"
        labelAlign="left"
        labelCol={{ flex: '108px' }}
        wrapperCol={{ flex: 1 }}
        colon={false}
        initialValues={{ unauthStatus: unauthStatus || 404, unauthHtml }}
        onFinish={(values) =>
          void onSave({
            unauthStatus: values.unauthStatus ?? 404,
            unauthHtml: values.unauthHtml ?? '',
          })
        }
      >
        <Form.Item
          label="响应状态"
          name="unauthStatus"
          extra="未通过安全入口或 User-Agent 校验时返回的 HTTP 状态码。"
          style={status === 444 ? { marginBottom: 0 } : undefined}
        >
          <Select style={{ width: '100%' }} options={UNAUTH_STATUS_OPTIONS} />
        </Form.Item>
        {status !== 444 && (
          <Form.Item
            label="自定义 HTML"
            name="unauthHtml"
            extra="可选。填写后未认证请求将返回该 HTML 页面，留空则返回空响应体。"
            style={{ marginBottom: 0 }}
          >
            <Input.TextArea
              rows={7}
              style={{ width: '100%', fontFamily: MONO_FONT }}
              placeholder="<!doctype html><html><body><h1>404 Not Found</h1></body></html>"
            />
          </Form.Item>
        )}
      </Form>
    </Modal>
  );
}

export function UserAgentModal({
  open,
  userAgentEnabled,
  userAgentAllow,
  saving,
  onSave,
  onClose,
}: {
  open: boolean;
  userAgentEnabled: boolean;
  userAgentAllow: string;
  saving: boolean;
  onSave: (values: {
    userAgentEnabled: boolean;
    userAgentAllow: string;
  }) => Promise<void>;
  onClose: () => void;
}) {
  const [items, setItems] = useState<string[]>(() =>
    parseUserAgentList(userAgentAllow),
  );
  const [draft, setDraft] = useState('');

  const addToken = (raw: string) => {
    const next = parseUserAgentList(raw);
    if (!next.length) return;
    setItems((prev) => [...new Set([...prev, ...next])]);
  };

  return (
    <Modal
      open={open}
      title="User-Agent 限制"
      width={560}
      okText="保存"
      confirmLoading={saving}
      destroyOnHidden
      onCancel={onClose}
      onOk={() =>
        void onSave({
          userAgentEnabled: items.length > 0 ? true : userAgentEnabled,
          userAgentAllow: items.join('\n'),
        })
      }
    >
      <Flex vertical gap={SPACING.md}>
        <Alert
          type="info"
          showIcon
          message="启用后，仅 User-Agent 包含下方任一关键字的客户端可访问面板；保存时会自动校验当前浏览器是否在允许列表中。"
        />
        <Flex gap={SPACING.xs}>
          <Input
            value={draft}
            placeholder="输入允许的 User-Agent 关键字"
            onChange={(e) => setDraft(e.target.value)}
            onPressEnter={() => {
              addToken(draft);
              setDraft('');
            }}
          />
          <Button
            onClick={() => {
              addToken(draft);
              setDraft('');
            }}
          >
            添加
          </Button>
          <Button
            onClick={() => addToken(navigator.userAgent)}
            title={navigator.userAgent}
          >
            填入当前浏览器
          </Button>
        </Flex>
        <div
          style={{
            border: '1px solid var(--ant-color-border-secondary)',
            borderRadius: 8,
            overflow: 'hidden',
          }}
        >
          <Flex
            justify="space-between"
            align="center"
            style={{
              padding: '8px 12px',
              borderBottom: '1px solid var(--ant-color-border-secondary)',
              background: 'var(--ant-color-fill-quaternary)',
            }}
          >
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              允许列表（共 {items.length} 项）
            </Typography.Text>
            {items.length > 0 && (
              <Button
                type="link"
                size="small"
                danger
                style={{ padding: 0, height: 'auto' }}
                onClick={() => setItems([])}
              >
                清空
              </Button>
            )}
          </Flex>
          <div style={{ maxHeight: 260, overflowY: 'auto' }}>
            {items.length === 0 ? (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="暂无 User-Agent 规则，请先添加"
                style={{ marginBlock: SPACING.md }}
              />
            ) : (
              items.map((item, idx) => (
                <Flex
                  key={item}
                  justify="space-between"
                  align="center"
                  gap={SPACING.sm}
                  style={{
                    padding: '8px 12px',
                    borderBottom:
                      idx === items.length - 1
                        ? 'none'
                        : '1px solid var(--ant-color-border-secondary)',
                  }}
                >
                  <Typography.Text
                    style={{
                      flex: 1,
                      minWidth: 0,
                      overflowWrap: 'anywhere',
                      fontFamily: MONO_FONT,
                      fontSize: 12,
                    }}
                  >
                    {item}
                  </Typography.Text>
                  <Button
                    type="link"
                    size="small"
                    danger
                    style={{ flexShrink: 0, paddingInline: 4 }}
                    onClick={() =>
                      setItems((prev) => prev.filter((x) => x !== item))
                    }
                  >
                    删除
                  </Button>
                </Flex>
              ))
            )}
          </div>
        </div>
      </Flex>
    </Modal>
  );
}
