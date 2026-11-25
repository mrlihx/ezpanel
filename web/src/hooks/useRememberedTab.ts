import { useEffect } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useAuth } from '../stores/auth';
import { request } from '../api/client';

export type Section =
  | 'websites'
  | 'docker'
  | 'dockerImages'
  | 'security'
  | 'waf'
  | 'databases'
  | 'mysqlInstance'
  | 'postgresqlInstance'
  | 'mongodbInstance'
  | 'redisInstance'
  | 'apps'
  | 'oplogs'
  | 'oplogSite'
  | 'tools'
  | 'settings'
  | 'alerts'
  | 'ssh'
  | 'dockerSettings'
  | 'terminal'
  | 'dockerCompose';
type Navigation = Partial<Record<Section, string>>;

export function useRememberedTab(
  section: Section,
  explicit: string | null | undefined,
  tabs: readonly string[],
) {
  const username = useAuth((state) => state.user?.username);
  const client = useQueryClient();
  const queryKey = ['navigation', username];
  const preferences = useQuery({
    queryKey,
    queryFn: () =>
      request<Navigation>('/api/settings/navigation', { silentError: true }),
    enabled: !!username,
  });
  const saved = preferences.data?.[section];
  const { mutate } = useMutation({
    mutationFn: (tab: string) =>
      request('/api/settings/navigation', {
        method: 'PUT',
        data: { section, tab },
        silentError: true,
      }),
    scope: { id: `navigation:${username}:${section}` },
    onSuccess: (_, tab) =>
      client.setQueryData<Navigation>(queryKey, (old) => ({
        ...old,
        [section]: tab,
      })),
  });
  const requested = explicit && tabs.includes(explicit) ? explicit : undefined;
  useEffect(() => {
    if (username && requested && requested !== saved && preferences.isFetched)
      mutate(requested);
  }, [username, requested, saved, preferences.isFetched, mutate]);
  return {
    tab: requested ?? (saved && tabs.includes(saved) ? saved : tabs[0]),
    loading: !requested && preferences.isPending,
  };
}
