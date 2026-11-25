import { Space, Spin, Typography } from 'antd';
import { LAYOUT } from '../../styles/tokens';
import { lazy, Suspense } from 'react';

import type { SiteSettingsState } from './SiteSettingsContent';
const CodeEditor = lazy(() => import('../../components/CodeEditor'));

export default function SiteWsgiPanel({
  site,
  tab,
}: Pick<SiteSettingsState, 'tab'> & {
  site: NonNullable<SiteSettingsState['site']['data']>;
}) {
  return tab === 'wsgi' && site.runtimeConfig ? (
    <Space direction="vertical" style={{ width: '100%' }}>
      <Typography.Text code>{site.runtimeConfig.path}</Typography.Text>
      <div style={{ height: LAYOUT.configEditorHeight }}>
        <Suspense fallback={<Spin />}>
          <CodeEditor
            path={site.runtimeConfig.path}
            value={site.runtimeConfig.content}
            readOnly
          />
        </Suspense>
      </div>
    </Space>
  ) : null;
}
