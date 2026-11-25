import { useEffect } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useRememberedTab, type Section } from './useRememberedTab';

type LegacyTab = {
  keys: readonly string[];
  tab?: string | null;
  suffix?: string;
};

export function useRoutedTab(
  section: Section,
  base: string,
  requested: string | undefined,
  tabs: readonly string[],
  legacy?: LegacyTab,
  pending = false,
) {
  const location = useLocation();
  const navigate = useNavigate();
  const explicit = requested ?? legacy?.tab;
  const { tab, loading } = useRememberedTab(section, explicit, tabs);
  const invalid = !!requested && !tabs.includes(requested);
  let search = location.search;
  const params = new URLSearchParams(search);
  if (legacy?.keys.some((key) => params.has(key))) {
    for (const key of legacy.keys) params.delete(key);
    search = params.size ? `?${params}` : '';
  }
  const suffix = explicit === tab ? (legacy?.suffix ?? '') : '';
  const destination =
    !invalid && !loading && !pending && tab
      ? `${requested ? location.pathname : `${base}/${tab}${suffix}`}${search}${location.hash}`
      : undefined;
  const redirect =
    destination !== undefined &&
    destination !== location.pathname + location.search + location.hash;

  useEffect(() => {
    if (redirect && destination) void navigate(destination, { replace: true });
  }, [destination, navigate, redirect]);

  return {
    tab,
    loading: loading || pending || redirect,
    invalid,
    selectTab: (next: string) => navigate(`${base}/${next}`),
  };
}
