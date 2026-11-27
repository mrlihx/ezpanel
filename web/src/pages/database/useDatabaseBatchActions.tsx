import { useState, type Key } from 'react';
import { App, Button, Flex, Select } from 'antd';
import DatabaseSelectAll from './DatabaseSelectAll';
import { SPACING } from '../../styles/tokens';

type BatchAction = 'backup' | 'delete';

export function useDatabaseBatchActions<Row extends object>({
  rows,
  rowKey,
  describe,
  deleteNotice,
  operate,
  onChanged,
}: {
  rows: Row[];
  rowKey: (row: Row) => Key;
  describe: (row: Row) => string;
  deleteNotice: string;
  operate: (row: Row, action: BatchAction) => Promise<unknown>;
  onChanged: () => void;
}) {
  const { message, modal } = App.useApp();
  const [selected, setSelected] = useState<Key[]>([]);
  const [action, setAction] = useState<BatchAction>('backup');
  const [busy, setBusy] = useState(false);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const current = Math.min(
    page,
    Math.max(1, Math.ceil(rows.length / pageSize)),
  );
  const pageKeys = rows
    .slice((current - 1) * pageSize, current * pageSize)
    .map(rowKey);
  const selectedSet = new Set(selected);
  const targets = rows.filter((row) => selectedSet.has(rowKey(row)));

  function confirm() {
    if (targets.length > 100) {
      void message.warning('每批最多操作 100 个数据库');
      return;
    }
    const deleting = action === 'delete';
    const title = deleting ? '删除' : '备份';
    modal.confirm({
      title: `${title}选中的 ${targets.length} 个数据库？`,
      content: (
        <>
          <div>
            {deleting ? deleteNotice : '备份将依次保存到各数据库的备份列表。'}
          </div>
          <ul
            style={{
              maxHeight: 180,
              overflow: 'auto',
              overflowWrap: 'anywhere',
            }}
          >
            {targets.map((row) => (
              <li key={rowKey(row)}>{describe(row)}</li>
            ))}
          </ul>
        </>
      ),
      okText: title,
      okButtonProps: { danger: deleting },
      onOk: async () => {
        setBusy(true);
        const failed: { key: Key; label: string; reason: string }[] = [];
        try {
          for (const row of targets) {
            try {
              await operate(row, action);
            } catch (error) {
              failed.push({
                key: rowKey(row),
                label: describe(row),
                reason: error instanceof Error ? error.message : '操作失败',
              });
            }
          }
          setSelected(failed.map(({ key }) => key));
          onChanged();
          if (failed.length) {
            modal.warning({
              title: `批量${title}完成，成功 ${targets.length - failed.length} 项，失败 ${failed.length} 项`,
              content: (
                <ul
                  style={{
                    maxHeight: 240,
                    overflow: 'auto',
                    overflowWrap: 'anywhere',
                  }}
                >
                  {failed.map(({ key, label, reason }) => (
                    <li key={key}>
                      {label}：{reason}
                    </li>
                  ))}
                </ul>
              ),
            });
          } else {
            void message.success(`已${title} ${targets.length} 个数据库`);
          }
        } finally {
          setBusy(false);
        }
      },
    });
  }

  return {
    reset() {
      setSelected([]);
      setPage(1);
    },
    rowSelection: {
      selectedRowKeys: selected,
      onChange: setSelected,
      getCheckboxProps: () => ({ disabled: busy }),
    },
    pagination: {
      current,
      pageSize,
      showSizeChanger: true,
      onChange(next: number, size: number) {
        setPage(size === pageSize ? next : 1);
        setPageSize(size);
      },
    },
    footer: () => (
      <Flex align="center" gap={SPACING.md} wrap>
        <DatabaseSelectAll
          pageKeys={pageKeys}
          selected={selected}
          onChange={setSelected}
          disabled={busy}
        />
        <Select
          aria-label="批量操作类型"
          style={{ width: 144 }}
          value={action}
          disabled={busy}
          onChange={setAction}
          options={[
            { value: 'backup', label: '备份数据库' },
            { value: 'delete', label: '删除数据库' },
          ]}
        />
        <Button disabled={!targets.length || busy} onClick={confirm}>
          批量操作（已选{targets.length}项）
        </Button>
      </Flex>
    ),
  };
}
