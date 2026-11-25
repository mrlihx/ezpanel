import { Alert, Button, Input, Select, Form, Switch } from 'antd';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { saveRedirect } from '../../api/website';

import type { SiteSettingsState } from './SiteSettingsContent';

export default function SiteRedirectPanel({
  apply,
  id,
  manageable,
  pending,
  site,
}: Pick<SiteSettingsState, 'apply' | 'id' | 'manageable' | 'pending'> & {
  site: NonNullable<SiteSettingsState['site']['data']>;
}) {
  return (
    <Form
      key={site.updatedAt}
      layout="horizontal"
      colon={false}
      labelCol={{ flex: LAYOUT.formLabelWidth }}
      wrapperCol={{ flex: 1 }}
      initialValues={{
        enabled: site.redirectEnabled,
        target: site.redirectTarget,
        code: site.redirectCode,
        keepUri: site.redirectKeepUri,
      }}
      onFinish={(values) => void apply(() => saveRedirect(id, values))}
    >
      <Alert
        type="info"
        showIcon
        message="对本站所有域名生效；保留请求路径时会把原路径和查询参数追加到目标地址。"
        style={{ marginBottom: SPACING.lg }}
      />
      <Form.Item name="enabled" label="启用重定向" valuePropName="checked">
        <Switch />
      </Form.Item>
      <Form.Item name="code" label="重定向类型">
        <Select
          options={[
            { value: 301, label: '301 永久重定向' },
            { value: 302, label: '302 临时重定向' },
          ]}
        />
      </Form.Item>
      <Form.Item
        name="target"
        label="目标地址"
        rules={[
          ({ getFieldValue }) => ({
            validator: (_, value) =>
              !getFieldValue('enabled') || value?.trim()
                ? Promise.resolve()
                : Promise.reject(new Error('请输入目标地址')),
          }),
        ]}
      >
        <Input placeholder="https://example.com" />
      </Form.Item>
      <Form.Item name="keepUri" label="保留请求路径" valuePropName="checked">
        <Switch />
      </Form.Item>
      <Form.Item label=" ">
        <Button
          type="primary"
          htmlType="submit"
          loading={pending}
          disabled={!manageable}
        >
          保存
        </Button>
      </Form.Item>
    </Form>
  );
}
