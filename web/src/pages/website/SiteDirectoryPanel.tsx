import { useState } from 'react';
import SiteDirectoryFields from './SiteDirectoryFields';
import { App, Button, Form, Space } from 'antd';
import { fixWebsitePermissions } from '../../api/website';
import { LAYOUT } from '../../styles/tokens';

import type { SiteSettingsState } from './SiteSettingsContent';

export default function SiteDirectoryPanel({
  manageable,
  pending,
  runtime,
  save,
  site,
}: Pick<SiteSettingsState, 'manageable' | 'pending' | 'runtime' | 'save'> & {
  site: NonNullable<SiteSettingsState['site']['data']>;
}) {
  const { message } = App.useApp();
  const [fixing, setFixing] = useState(false);
  const showRoot = site.type === 'static' || !!runtime;
  return (
    <Form
      key={site.updatedAt}
      layout="horizontal"
      labelCol={{ flex: LAYOUT.formLabelWidth }}
      wrapperCol={{ flex: 1 }}
      colon={false}
      initialValues={{
        root: site.root,
        name: site.name,
        remark: site.remark,
      }}
      onFinish={(values) => void save(values)}
    >
      <SiteDirectoryFields showRoot={showRoot} />
      <Form.Item label=" ">
        <Space wrap>
          <Button
            type="primary"
            htmlType="submit"
            loading={pending}
            disabled={!manageable}
          >
            保存
          </Button>
          {showRoot && site.root && (
            <Button
              loading={fixing}
              disabled={!manageable || pending}
              onClick={async () => {
                setFixing(true);
                try {
                  const res = await fixWebsitePermissions(site.id);
                  void message.success(
                    `目录权限已修复为 ${res.owner} (${res.mode})`,
                  );
                } finally {
                  setFixing(false);
                }
              }}
            >
              修复目录权限
            </Button>
          )}
        </Space>
      </Form.Item>
    </Form>
  );
}
