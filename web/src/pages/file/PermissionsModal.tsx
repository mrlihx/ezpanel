import { useState, type CSSProperties } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Input,
  Modal,
  Select,
  Space,
  Typography,
  Tabs,
  Spin,
  theme,
} from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  files,
  fileProperties,
  type Batch,
  type FileEntry,
} from '../../api/file';
import { LAYOUT, SPACING } from '../../styles/tokens';
import PropertyInfo from './PropertyInfo';

export default function PermissionsModal({
  entry,
  initialTab = 'permissions',
  onClose,
  refresh,
}: {
  entry: FileEntry;
  initialTab?: 'general' | 'permissions';
  onClose: () => void;
  refresh: () => void;
}) {
  const { token } = theme.useToken();
  const client = useQueryClient();
  const [tab, setTab] = useState<string>(initialTab);
  const properties = useQuery({
    queryKey: ['file-properties', entry.path],
    queryFn: ({ signal }) => fileProperties(entry.path, false, signal),
    enabled: tab === 'general',
  });
  const details = useQuery({
    queryKey: ['file-properties', entry.path, 'details'],
    queryFn: ({ signal }) => fileProperties(entry.path, true, signal),
    enabled: tab === 'details',
  });

  const [mode, setMode] = useState(entry.mode.slice(-3));
  const [owner, setOwner] = useState(entry.owner);
  const [group, setGroup] = useState(entry.group);
  const [recursive, setRecursive] = useState(false);
  const [applied, setApplied] = useState<{
    mode: string | null;
    owner: string | null;
    group: string | null;
  }>({ mode: entry.mode.slice(-3), owner: entry.owner, group: entry.group });
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<
    { action: string; success: boolean; message: string }[]
  >([]);
  const accounts = useQuery({
    queryKey: ['file-identities'],
    enabled: tab === 'permissions',
    queryFn: ({ signal }) =>
      files<{ users: { name: string; group: string }[]; groups: string[] }>(
        '/identities',
        { signal },
      ),
  });
  const value = Number.parseInt(mode, 8) || 0;
  const valid = /^[0-7]{3}$/.test(mode) && Boolean(owner && group);
  async function save() {
    if (!valid) return;
    setBusy(true);
    const messages: typeof results = [];
    try {
      for (const action of ['chmod', 'chown']) {
        if (
          !recursive &&
          (action === 'chmod'
            ? mode === applied.mode
            : owner === applied.owner && group === applied.group)
        )
          continue;
        try {
          const result = await files<Batch>(`/${action}`, {
            method: 'POST',
            silentError: true,
            data:
              action === 'chmod'
                ? { paths: [entry.path], mode: '0' + mode, recursive }
                : { paths: [entry.path], owner, group, recursive },
          });
          setApplied((current) =>
            action === 'chmod'
              ? { ...current, mode: result.failedCount ? null : mode }
              : {
                  ...current,
                  owner: result.failedCount ? null : owner,
                  group: result.failedCount ? null : group,
                },
          );
          messages.push({
            action,
            success: result.failedCount === 0,
            message: `成功 ${result.successCount}，失败 ${result.failedCount}${
              result.failedCount
                ? '；' +
                  result.results
                    .filter((row) => row.code)
                    .map((row) => `${row.target}: ${row.msg}`)
                    .join('；')
                : ''
            }`,
          });
        } catch (error) {
          setApplied((current) =>
            action === 'chmod'
              ? { ...current, mode: null }
              : { ...current, owner: null, group: null },
          );
          messages.push({
            action,
            success: false,
            message: error instanceof Error ? error.message : '应用失败',
          });
        }
      }
      setResults(
        messages.length
          ? messages
          : [
              {
                action: '设置',
                success: true,
                message: '权限、所有者和用户组均未变化',
              },
            ],
      );
      refresh();
      void client.invalidateQueries({
        queryKey: ['file-properties', entry.path],
      });
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      title={`[ ${entry.name} ] — 文件属性`}
      open
      width={LAYOUT.formMaxWidth}
      onCancel={onClose}
      footer={null}
    >
      <div
        style={
          {
            '--permission-gap': `${SPACING.lg}px`,
            '--permission-small-gap': `${SPACING.sm}px`,
            '--permission-border': token.colorBorder,
            '--permission-muted': token.colorTextSecondary,
            '--permission-radius': `${token.borderRadius}px`,
            '--permission-mode-width': `${LAYOUT.filePermissionModeWidth}px`,
            '--permission-owner-width': `${LAYOUT.filePermissionOwnerWidth}px`,
          } as CSSProperties
        }
      >
        <Tabs
          type="card"
          activeKey={tab}
          onChange={setTab}
          items={[
            { key: 'general', label: '常规' },
            { key: 'details', label: '详细信息' },
            { key: 'permissions', label: '权限' },
          ]}
        />
        {tab !== 'permissions' && (
          <>
            {(tab === 'details' ? details.error : properties.error) && (
              <Alert
                type="error"
                message={
                  (tab === 'details' ? details.error : properties.error)
                    ?.message
                }
              />
            )}
            <Spin
              spinning={
                tab === 'details' ? details.isFetching : properties.isFetching
              }
            >
              <PropertyInfo
                data={
                  tab === 'details'
                    ? details.data || properties.data
                    : properties.data
                }
                entry={entry}
                details={tab === 'details'}
              />
            </Spin>
          </>
        )}
        {tab === 'permissions' && (
          <>
            <Typography.Paragraph
              type="secondary"
              ellipsis={{ tooltip: entry.path }}
            >
              {entry.path}
            </Typography.Paragraph>
            <div className="file-permissions-grid">
              {['所有者', '用户组', '公共'].map((label, index) => (
                <fieldset key={label}>
                  <legend>{label}</legend>
                  <Space direction="vertical" size="middle">
                    {[4, 2, 1].map((bit, i) => (
                      <Checkbox
                        key={bit}
                        checked={Boolean(value & (bit << ((2 - index) * 3)))}
                        disabled={busy}
                        onChange={(event) => {
                          const mask = bit << ((2 - index) * 3);
                          setMode(
                            (event.target.checked
                              ? value | mask
                              : value & ~mask
                            )
                              .toString(8)
                              .padStart(3, '0'),
                          );
                        }}
                      >
                        {['读取', '写入', '执行'][i]}
                      </Checkbox>
                    ))}
                  </Space>
                </fieldset>
              ))}
            </div>
            <div className="file-permissions-apply">
              <Input
                aria-label="数字权限"
                className="file-permissions-mode"
                value={mode}
                maxLength={3}
                disabled={busy}
                status={/^[0-7]{3}$/.test(mode) ? '' : 'error'}
                onChange={(event) => setMode(event.target.value)}
              />
              <Typography.Text>权限，所有者</Typography.Text>
              <Select
                aria-label="文件所有者"
                className="file-permissions-owner"
                value={owner}
                showSearch
                loading={accounts.isFetching}
                disabled={busy}
                options={Array.from(
                  new Set([
                    owner,
                    ...(accounts.data?.users.map((user) => user.name) ?? []),
                  ]),
                ).map((name) => ({ value: name, label: name }))}
                onChange={(name) => {
                  setOwner(name);
                  const primaryGroup = accounts.data?.users.find(
                    (user) => user.name === name,
                  )?.group;
                  if (primaryGroup) setGroup(primaryGroup);
                }}
              />
              {entry.isDir && (
                <Checkbox
                  checked={recursive}
                  disabled={busy}
                  onChange={(event) => setRecursive(event.target.checked)}
                >
                  应用到子目录
                </Checkbox>
              )}
              <Button
                type="primary"
                loading={busy}
                disabled={!valid}
                onClick={() => void save()}
              >
                应用
              </Button>
            </div>
            {accounts.error && (
              <Alert
                type="error"
                message="系统账号列表加载失败"
                action={
                  <Button size="small" onClick={() => void accounts.refetch()}>
                    重试
                  </Button>
                }
              />
            )}
            {recursive && (
              <Typography.Paragraph type="secondary">
                所选权限、所有者和用户组将应用到全部子目录和文件，跳过符号链接。
              </Typography.Paragraph>
            )}
            {results.map((result) => (
              <Alert
                key={result.action}
                type={result.success ? 'success' : 'error'}
                message={`${result.action === 'chmod' ? '权限' : result.action === 'chown' ? '所有者 / 用户组' : result.action}：${result.message}`}
              />
            ))}
          </>
        )}
      </div>
    </Modal>
  );
}
