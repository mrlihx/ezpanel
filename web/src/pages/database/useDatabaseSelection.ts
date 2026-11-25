import { useState } from 'react';
import { useRememberedTab } from '../../hooks/useRememberedTab';

type Engine = 'mysql' | 'mongodb' | 'redis';

export function useDatabaseSelection<
  T extends { id: string; local?: boolean; instanceId?: string },
>(engine: Engine, data: T[] | undefined) {
  const [sid, setSid] = useState<string>();
  const rows = data ?? [];
  const initial =
    rows.find((row) => row.local && !row.instanceId) ??
    rows.find((row) => row.local) ??
    rows[0];
  const { tab, loading } = useRememberedTab(
    `${engine}Instance`,
    sid,
    initial
      ? [
          initial.id,
          ...rows.filter((row) => row !== initial).map((row) => row.id),
        ]
      : [],
  );
  const selected = loading ? undefined : rows.find((row) => row.id === tab);
  return { selected, setSid };
}
