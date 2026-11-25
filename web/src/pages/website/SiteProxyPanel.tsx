import ProxyRules from './ProxyRules';

import type { SiteSettingsState } from './SiteSettingsContent';

export default function SiteProxyPanel({
  error,
  manageable,
  pending,
  save,
  site,
}: Pick<SiteSettingsState, 'error' | 'manageable' | 'pending' | 'save'> & {
  site: NonNullable<SiteSettingsState['site']['data']>;
}) {
  return (
    <ProxyRules
      routes={site.proxyRoutes}
      onSave={(proxyRoutes) => save({ proxyRoutes })}
      pending={pending}
      disabled={!manageable}
      error={error}
    />
  );
}
