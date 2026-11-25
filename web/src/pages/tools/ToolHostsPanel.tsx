import { Button, Skeleton, Space, Typography } from 'antd';
import { LAYOUT } from '../../styles/tokens';
import { lazy, Suspense } from 'react';
import type { ToolboxPageState } from './index';
const CodeEditor = lazy(() => import('../../components/CodeEditor'));

export default function ToolHostsPanel({
  busy,
  confirm,
  hosts,
  hostsDraft,
  save,
  setHostsDraft,
}: Pick<
  ToolboxPageState,
  'busy' | 'confirm' | 'hosts' | 'hostsDraft' | 'save' | 'setHostsDraft'
>) {
  return (
    <>
      {hosts.data && (
        <>
          <Typography.Text code>{hosts.data.path}</Typography.Text>
          <div
            style={{
              height: LAYOUT.configEditorHeight,
              width: '100%',
            }}
          >
            <Suspense fallback={<Skeleton active />}>
              <CodeEditor
                path="/etc/hosts"
                value={hostsDraft ?? hosts.data.content}
                onChange={setHostsDraft}
              />
            </Suspense>
          </div>
          <Space>
            <Button
              type="primary"
              loading={busy}
              disabled={
                hostsDraft === undefined || hostsDraft === hosts.data.content
              }
              onClick={() =>
                void save('hosts', {
                  content: hostsDraft,
                  revision: hosts.data!.revision,
                })
              }
            >
              保存
            </Button>
            <Button
              onClick={() =>
                confirm(
                  '重新加载 hosts？',
                  '未保存的修改将丢弃。',
                  async () => {
                    setHostsDraft(undefined);
                    await hosts.refetch();
                  },
                )
              }
            >
              重新加载
            </Button>
          </Space>
        </>
      )}
    </>
  );
}
