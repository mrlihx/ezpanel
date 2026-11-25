import { Alert, Button, Select, Form } from 'antd';
import { LAYOUT } from '../../styles/tokens';

import type { SiteSettingsState } from './SiteSettingsContent';

export default function SitePhpPanel({
  manageable,
  pending,
  php,
  save,
  site,
}: Pick<SiteSettingsState, 'manageable' | 'pending' | 'php' | 'save'> & {
  site: NonNullable<SiteSettingsState['site']['data']>;
}) {
  return (
    <Form
      key={site.phpVersion}
      layout="horizontal"
      labelCol={{ flex: LAYOUT.formLabelWidth }}
      wrapperCol={{ flex: 1 }}
      colon={false}
      initialValues={{ phpVersion: site.phpVersion }}
      onFinish={(values) => void save(values)}
    >
      {php.error && (
        <Alert
          type="error"
          message={php.error.message}
          action={<Button onClick={() => void php.refetch()}>重试</Button>}
        />
      )}
      <Form.Item name="phpVersion" label="PHP版本">
        <Select
          loading={php.isFetching}
          options={[
            { value: '', label: '纯静态' },
            ...(site.phpVersion &&
            !php.data?.list.some(
              (item) => item.version === site!.phpVersion && item.installed,
            )
              ? [
                  {
                    value: site.phpVersion,
                    label: `PHP-${site.phpVersion}（当前配置）`,
                  },
                ]
              : []),
            ...(php.data?.list
              .filter((item) => item.installed)
              .map((item) => ({
                value: item.version,
                label: `PHP-${item.version}${item.running ? '' : '（已停止）'}`,
                disabled: !item.running && item.version !== site!.phpVersion,
              })) ?? []),
          ]}
        />
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
