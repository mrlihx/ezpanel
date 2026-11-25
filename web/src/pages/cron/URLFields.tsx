import { useState } from 'react';
import {
  Alert,
  Button,
  Descriptions,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Typography,
} from 'antd';
import { MinusCircleOutlined, PlusOutlined } from '@ant-design/icons';
import { testCronURL, type CronInput } from '../../api/cron';
import { MONO_FONT, SPACING, TERMINAL_COLORS } from '../../styles/tokens';

export default function URLFields() {
  const form = Form.useFormInstance<CronInput>();
  const method = Form.useWatch('urlMethod', form) ?? 'GET';
  const [testing, setTesting] = useState(false);
  const [result, setResult] =
    useState<Awaited<ReturnType<typeof testCronURL>>>();
  async function test() {
    const values = await form.validateFields(
      method === 'POST' ? ['url', 'urlParams'] : ['url'],
      { recursive: true },
    );
    setTesting(true);
    try {
      setResult(
        await testCronURL({
          url: values.url,
          urlMethod: method,
          userAgent: form.getFieldValue('userAgent'),
          urlParams: values.urlParams,
        }),
      );
    } finally {
      setTesting(false);
    }
  }
  return (
    <>
      <Form.Item name="urlMethod" label="请求方式">
        <Select
          options={[{ value: 'GET' }, { value: 'POST' }]}
          style={{ maxWidth: 160 }}
        />
      </Form.Item>
      <Form.Item label="目标 URL" required htmlFor="url">
        <Space.Compact block>
          <Form.Item
            name="url"
            noStyle
            rules={[{ required: true, message: '请输入目标 URL' }]}
          >
            <Input placeholder="https://example.com/cron" />
          </Form.Item>
          <Button loading={testing} onClick={() => void test().catch(() => {})}>
            测试访问
          </Button>
        </Space.Compact>
      </Form.Item>
      <Form.Item name="userAgent" label="User-Agent">
        <Input placeholder="使用默认 User-Agent" maxLength={1024} />
      </Form.Item>
      {method === 'POST' && (
        <Form.Item label="请求参数">
          <Form.List name="urlParams">
            {(fields, { add, remove }) => (
              <>
                <div className="cron-url-parameters" aria-hidden="true">
                  <Typography.Text strong>参数名</Typography.Text>
                  <Typography.Text strong>参数值</Typography.Text>
                  <Typography.Text strong>操作</Typography.Text>
                </div>
                {fields.map((field) => (
                  <div className="cron-url-parameters" key={field.key}>
                    <Form.Item
                      name={[field.name, 'name']}
                      rules={[
                        {
                          required: true,
                          whitespace: true,
                          message: '请输入参数名',
                        },
                      ]}
                    >
                      <Input aria-label="参数名" />
                    </Form.Item>
                    <Form.Item name={[field.name, 'value']}>
                      <Input aria-label="参数值" />
                    </Form.Item>
                    <Button
                      aria-label="删除参数"
                      icon={<MinusCircleOutlined />}
                      type="text"
                      danger
                      onClick={() => remove(field.name)}
                    />
                  </div>
                ))}
                <Button
                  icon={<PlusOutlined />}
                  onClick={() => add({ name: '', value: '' })}
                  disabled={fields.length >= 100}
                >
                  添加参数
                </Button>
              </>
            )}
          </Form.List>
        </Form.Item>
      )}
      {result && (
        <Modal
          open
          title="URL 测试结果"
          width={720}
          footer={null}
          onCancel={() => setResult(undefined)}
        >
          <Descriptions
            size="small"
            column={2}
            items={[
              {
                key: 'time',
                label: '响应时间',
                children: result.durationMs + ' 毫秒',
              },
              {
                key: 'status',
                label: '响应状态',
                children: result.statusCode
                  ? 'HTTP ' + result.statusCode
                  : '请求失败',
              },
            ]}
          />
          {result.error && (
            <Alert type="error" message={result.error} showIcon />
          )}
          <Typography.Text strong>响应内容</Typography.Text>
          <pre
            style={{
              maxHeight: '45vh',
              overflow: 'auto',
              whiteSpace: 'pre-wrap',
              overflowWrap: 'anywhere',
              fontFamily: MONO_FONT,
              padding: SPACING.md,
              borderRadius: 6,
              background: TERMINAL_COLORS.background,
              color: TERMINAL_COLORS.foreground,
            }}
          >
            {result.body || '暂无响应内容'}
          </pre>
          {result.truncated && (
            <Typography.Text type="secondary">
              已显示响应内容的前 64 KiB。
            </Typography.Text>
          )}
        </Modal>
      )}
    </>
  );
}
