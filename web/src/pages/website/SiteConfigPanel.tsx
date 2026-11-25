import { Alert, Button, Spin } from 'antd';
import { LAYOUT } from '../../styles/tokens';
import { lazy, Suspense } from 'react';

import type { SiteSettingsState } from './SiteSettingsContent';
const errorText = (e: unknown) => (e instanceof Error ? e.message : '请求失败');
const CodeEditor = lazy(() => import('../../components/CodeEditor'));

export default function SiteConfigPanel({
  config,
  id,
}: Pick<SiteSettingsState, 'config' | 'id'>) {
  return config.isPending ? (
    <Spin />
  ) : config.isError && !config.data ? (
    <Alert
      type="error"
      message={errorText(config.error)}
      action={<Button onClick={() => void config.refetch()}>重试</Button>}
    />
  ) : (
    <div style={{ height: LAYOUT.configEditorHeight }}>
      <Suspense fallback={<Spin />}>
        <CodeEditor
          path={`panel-${id}.conf`}
          value={config.data?.content ?? ''}
          readOnly
        />
      </Suspense>
    </div>
  );
}
