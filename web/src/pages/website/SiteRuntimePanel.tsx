import SiteDirectoryFields from './SiteDirectoryFields';
import { Button, Form, Space } from 'antd';
import { type SiteInput } from '../../api/website';
import { LAYOUT } from '../../styles/tokens';
import RuntimeFields from './RuntimeFields';

import type { SiteSettingsState } from './SiteSettingsContent';

export default function SiteRuntimePanel({
  entryModule,
  id,
  manageable,
  pending,
  runtime,
  runtimeCallable,
  save,
  site,
}: Pick<
  SiteSettingsState,
  'entryModule' | 'id' | 'manageable' | 'pending' | 'runtimeCallable' | 'save'
> & {
  runtime: NonNullable<SiteSettingsState['runtime']>;
  site: NonNullable<SiteSettingsState['site']['data']>;
}) {
  return (
    <Form<
      Partial<SiteInput> & {
        runtimeEntryFile?: string;
        runtimeCallable?: string;
      }
    >
      key={site.updatedAt}
      layout="horizontal"
      labelCol={{ flex: LAYOUT.formLabelWidth }}
      wrapperCol={{ flex: 1 }}
      colon={false}
      initialValues={{
        ...site,
        runtimeLaunch:
          site.runtimeLaunch ||
          (runtime === 'nodejs' && !site.runtimeConfigured
            ? 'script'
            : 'command'),
        runtimeScript: site.runtimeScript || 'start',
        runtimePort: site.runtimePort || (runtime === 'python' ? 8000 : 3000),
        runtimeEntryFile: entryModule
          ? `${entryModule.replaceAll('.', '/')}.py`
          : undefined,
        runtimeCallable,
      }}
      onFinish={({ runtimeEntryFile, runtimeCallable, ...values }) =>
        void save({
          ...values,
          runtimeEntry: runtimeEntryFile
            ? `${runtimeEntryFile.slice(0, -3).replaceAll('/', '.')}:${runtimeCallable}`
            : '',
        })
      }
    >
      <SiteDirectoryFields showRoot project />
      <RuntimeFields runtime={runtime} projectRoot={site.root} siteId={id} />
      <Space wrap>
        <Button
          type="primary"
          htmlType="submit"
          loading={pending}
          disabled={!manageable}
        >
          保存
        </Button>
      </Space>
    </Form>
  );
}
