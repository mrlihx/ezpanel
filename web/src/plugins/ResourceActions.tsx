import { useQuery } from '@tanstack/react-query';
import { Button, Dropdown, type MenuProps } from 'antd';
import { Ellipsis } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import type { PanelMenuResource } from '../../../sdk/ts/panel-sdk';
import { request } from '../api/client';
import { usePluginList } from '../api/plugin';

type Action = {
  pluginId: string;
  title: string;
  ids: string[] | null;
  all: boolean;
};

export function useResourceActions(kind: PanelMenuResource['kind']) {
  const plugins = usePluginList();
  const active = (plugins.data ?? []).filter(
    (plugin) =>
      plugin.runtimeStatus === 'running' && plugin.resourceActions?.[kind],
  );
  const query = useQuery({
    queryKey: [
      'plugins',
      'resource-actions',
      kind,
      active.map((plugin) => `${plugin.id}:${plugin.version}`),
    ],
    queryFn: ({ signal }) =>
      request<Action[]>(`/api/plugins/resource-actions/${kind}`, {
        signal,
        silentError: true,
      }),
    enabled: active.length > 0,
  });
  const navigate = useNavigate();
  return (id: string, name?: string, engine?: string) =>
    (query.data ?? [])
      .filter((item) => item.all || item.ids?.includes(id))
      .map((item) => ({
        key: `plugin-${item.pluginId}`,
        label: item.title,
        onClick: () =>
          void navigate(`/p/${encodeURIComponent(item.pluginId)}`, {
            state: { resource: { kind, id, name, engine } },
          }),
      }));
}

export default function ResourceActions({
  items,
}: {
  items: MenuProps['items'];
}) {
  if (!items?.length) return null;
  return (
    <Dropdown trigger={['click']} menu={{ items }}>
      <Button
        type="text"
        size="small"
        aria-label="插件操作"
        icon={<Ellipsis size={16} />}
      />
    </Dropdown>
  );
}
