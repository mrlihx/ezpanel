import { Alert, Button, Select, Space, Tabs, Typography } from 'antd';
import { LAYOUT } from '../../styles/tokens';
import { testMirrors, type MirrorResult } from '../../api/toolbox';
import ContentSizedTable from '../../components/ContentSizedTable';
import { formatSize } from '../../utils/format';
import type { ToolboxPageState } from './index';

export default function ToolMirrorsPanel({
  busy,
  confirm,
  currentMirror,
  mirrorAddresses,
  mirrorKind,
  mirrorNotice,
  mirrorRows,
  mirrors,
  save,
  selectedMirror,
  setBusy,
  setMirrorKind,
  setMirrorNotice,
  setMirrorRows,
}: Pick<
  ToolboxPageState,
  | 'busy'
  | 'confirm'
  | 'currentMirror'
  | 'mirrorAddresses'
  | 'mirrorKind'
  | 'mirrorNotice'
  | 'mirrorRows'
  | 'mirrors'
  | 'save'
  | 'selectedMirror'
  | 'setBusy'
  | 'setMirrorKind'
  | 'setMirrorNotice'
  | 'setMirrorRows'
>) {
  return (
    <>
      {mirrors.data && (
        <>
          <Tabs
            activeKey={mirrorKind}
            onChange={(v) => {
              setMirrorKind(v);
              setMirrorRows(undefined);
              setMirrorNotice('');
            }}
            items={[
              { key: 'apt', label: 'Yum/Apt' },
              { key: 'pip', label: 'PIP' },
            ]}
          />
          {mirrorKind === 'apt' && !mirrors.data.supported && (
            <Alert
              type="info"
              message="此系统暂不支持一键切换发行版镜像，PIP 镜像仍可设置。"
            />
          )}
          <Space wrap>
            <Select
              placeholder="选择镜像源"
              style={{ width: LAYOUT.cronTypeWidth }}
              value={selectedMirror}
              options={[
                ...(!currentMirror && mirrorAddresses?.length
                  ? [
                      {
                        value: 'current',
                        label:
                          mirrorAddresses.length === 1
                            ? `当前 · ${mirrorAddresses[0]}`
                            : `当前 · ${mirrorAddresses.length} 个镜像源`,
                        title: mirrorAddresses.join('、'),
                        disabled: true,
                      },
                    ]
                  : []),
                ...mirrors.data.providers.map((p) => ({
                  value: p.id,
                  label: p.id === currentMirror ? `${p.name}（当前）` : p.name,
                  disabled:
                    (mirrorKind === 'apt' && !mirrors.data?.supported) ||
                    p.id === currentMirror,
                })),
              ]}
              onChange={(id) =>
                confirm(
                  '切换镜像源？',
                  '保存前备份配置；Apt 将更新并验证索引，失败恢复原配置。',
                  () =>
                    save('mirrors', {
                      kind: mirrorKind,
                      provider: id,
                    }),
                )
              }
            />
            <Button
              loading={busy}
              disabled={mirrorKind === 'apt' && !mirrors.data.supported}
              onClick={async () => {
                setBusy(true);
                try {
                  const r = await testMirrors(mirrorKind);
                  setMirrorRows(r.list);
                  setMirrorNotice(r.notice);
                } finally {
                  setBusy(false);
                }
              }}
            >
              测速并推荐
            </Button>
            <Button onClick={() => void mirrors.refetch()}>刷新</Button>
          </Space>
          {mirrorRows && (
            <ContentSizedTable<MirrorResult>
              fillColumn="镜像源"
              size="small"
              rowKey="id"
              pagination={false}
              dataSource={[...mirrorRows].sort(
                (a, b) =>
                  Number(b.complete) - Number(a.complete) ||
                  Number(b.reachable) - Number(a.reachable) ||
                  (b.speed ?? 0) - (a.speed ?? 0),
              )}
              columns={[
                {
                  title: '镜像源',
                  render: (_, r) => (
                    <>
                      <div>{r.name}</div>
                      <Typography.Text type="secondary">
                        {r.url}
                      </Typography.Text>
                    </>
                  ),
                },
                {
                  title: '延迟',
                  render: (_, r) =>
                    r.latency === null ? '—' : `${r.latency.toFixed(1)} ms`,
                },
                {
                  title: '下载速度',
                  render: (_, r) =>
                    r.speed === null ? '—' : `${formatSize(r.speed)}/s`,
                },
                {
                  title: '连接状态',
                  render: (_, r) => (r.reachable ? '可连接' : '失败'),
                },
                {
                  title: '索引检查',
                  render: (_, r) => (r.complete ? '可读取' : '未通过'),
                },
                {
                  title: '操作',
                  render: (_, r) => (
                    <Button
                      type="link"
                      disabled={!r.complete}
                      onClick={() =>
                        confirm('使用此镜像？', r.url, () =>
                          save('mirrors', {
                            kind: mirrorKind,
                            provider: r.id,
                          }),
                        )
                      }
                    >
                      选用
                    </Button>
                  ),
                },
              ]}
            />
          )}
          {mirrorNotice && <Alert type="info" message={mirrorNotice} />}
          {mirrorKind === 'apt' ? (
            mirrors.data.files.map((f) => (
              <div key={f.path}>
                <Typography.Text code>{f.path}</Typography.Text>
                <Typography.Paragraph>
                  <pre style={{ whiteSpace: 'pre-wrap' }}>{f.content}</pre>
                </Typography.Paragraph>
              </div>
            ))
          ) : (
            <Typography.Paragraph>
              <pre style={{ overflowX: 'auto' }}>
                {mirrors.data.pip || '使用 pip 默认源'}
              </pre>
            </Typography.Paragraph>
          )}
        </>
      )}
    </>
  );
}
