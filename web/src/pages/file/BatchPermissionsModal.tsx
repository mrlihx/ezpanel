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
  theme,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import { files, type Batch, type FileEntry } from '../../api/file';
import { LAYOUT, SPACING } from '../../styles/tokens';

export default function BatchPermissionsModal({
  entries,
  onClose,
  refresh,
}: {
  entries: FileEntry[];
  onClose: () => void;
  refresh: () => void;
}) {
  const { token } = theme.useToken();
  const [mode, setMode] = useState('755'),
    [owner, setOwner] = useState<string>(),
    [changeOwner, setChangeOwner] = useState(false),
    [recursive, setRecursive] = useState(false),
    [busy, setBusy] = useState(false);
  const [results, setResults] = useState<
    { key: string; target: string; action: string; code: number; msg: string }[]
  >([]);
  const accounts = useQuery({
    queryKey: ['file-identities'],
    queryFn: () =>
      files<{ users: { name: string; group: string }[] }>('/identities'),
    enabled: changeOwner,
  });
  const value = Number.parseInt(mode, 8) || 0;
  return (
    <Modal
      open
      title={`批量权限设置 · ${entries.length}项`}
      width={LAYOUT.shellWidth}
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
        <Typography.Paragraph
          ellipsis={{
            rows: 2,
            tooltip: entries.map((entry) => entry.path).join('；'),
          }}
        >
          {entries.map((entry) => entry.name).join('、')}
        </Typography.Paragraph>
        {entries.length > 100 && (
          <Alert type="warning" message="单次最多设置100项，请减少选择。" />
        )}
        <div className="file-permissions-grid">
          {['所有者', '用户组', '公共'].map((label, index) => (
            <fieldset key={label}>
              <legend>{label}</legend>
              <Space direction="vertical">
                {[4, 2, 1].map((bit, i) => (
                  <Checkbox
                    key={bit}
                    disabled={busy}
                    checked={!!(value & (bit << ((2 - index) * 3)))}
                    onChange={(event) => {
                      const mask = bit << ((2 - index) * 3);
                      setMode(
                        (event.target.checked ? value | mask : value & ~mask)
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
            aria-label="批量数字权限"
            className="file-permissions-mode"
            value={mode}
            maxLength={3}
            disabled={busy}
            onChange={(event) => setMode(event.target.value)}
          />
          <Checkbox
            checked={changeOwner}
            disabled={busy}
            onChange={(event) => setChangeOwner(event.target.checked)}
          >
            同时修改所有者
          </Checkbox>
          {changeOwner && (
            <Select
              aria-label="批量所有者"
              className="file-permissions-owner"
              value={owner}
              placeholder="请选择所有者"
              showSearch
              disabled={busy}
              loading={accounts.isFetching}
              options={accounts.data?.users.map((user) => ({
                value: user.name,
                label: user.name,
              }))}
              onChange={setOwner}
            />
          )}
          {entries.some((entry) => entry.isDir) && (
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
            disabled={
              !/^[0-7]{3}$/.test(mode) ||
              entries.length > 100 ||
              (changeOwner && !owner)
            }
            onClick={async () => {
              setBusy(true);
              const output: typeof results = [];
              try {
                for (const action of changeOwner
                  ? ['chmod', 'chown']
                  : ['chmod']) {
                  try {
                    const result = await files<Batch>(`/${action}`, {
                      method: 'POST',
                      silentError: true,
                      data:
                        action === 'chmod'
                          ? {
                              paths: entries.map((entry) => entry.path),
                              mode: '0' + mode,
                              recursive,
                            }
                          : {
                              paths: entries.map((entry) => entry.path),
                              owner,
                              group: accounts.data?.users.find(
                                (user) => user.name === owner,
                              )?.group,
                              recursive,
                            },
                    });
                    output.push(
                      ...result.results.map((row) => ({
                        ...row,
                        key: action + row.target,
                        action: action === 'chmod' ? '权限' : '所有者',
                      })),
                    );
                  } catch (error) {
                    output.push({
                      key: action,
                      target: '所选文件',
                      action: action === 'chmod' ? '权限' : '所有者',
                      code: 1,
                      msg: error instanceof Error ? error.message : '设置失败',
                    });
                  }
                }
                setResults(output);
                refresh();
              } finally {
                setBusy(false);
              }
            }}
          >
            应用
          </Button>
        </div>
        {accounts.error && (
          <Alert type="error" message={accounts.error.message} />
        )}
        {results.length > 0 && (
          <ContentSizedTable
            legacySizing
            fillColumn="target"
            size="small"
            rowKey="key"
            scroll={{ y: 260 }}
            pagination={false}
            dataSource={results}
            columns={[
              { title: '文件', dataIndex: 'target', ellipsis: true },
              { title: '操作', dataIndex: 'action', width: 90 },
              {
                title: '结果',
                render: (_, row) => (
                  <Typography.Text type={row.code ? 'danger' : 'success'}>
                    {row.msg}
                  </Typography.Text>
                ),
              },
            ]}
          />
        )}
      </div>
    </Modal>
  );
}
