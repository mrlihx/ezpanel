import { DiffEditor } from '@monaco-editor/react';
import { useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Flex,
  Grid,
  Modal,
  Segmented,
  Select,
  Skeleton,
  Typography,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import {
  composeRevision,
  composeRevisions,
  rollbackComposeRevision,
  type ComposeProjectDetail,
} from '../../../api/composeProjects';
import '../../../components/CodeEditor';
import { useTheme } from '../../../stores/theme';
import { LAYOUT } from '../../../styles/tokens';
import { formatTime } from '../../../utils/format';

export default function ComposeRevisionModal({
  project,
  online,
  onClose,
  onTask,
}: {
  project: ComposeProjectDetail;
  online: boolean;
  onClose: () => void;
  onTask: (taskId: string, apply: boolean) => void;
}) {
  const { mode, systemDark } = useTheme();
  const { md } = Grid.useBreakpoint();
  const [selected, setSelected] = useState<string>();
  const [apply, setApply] = useState(false);
  const [file, setFile] = useState('yaml');
  const [busy, setBusy] = useState(false);
  const revisions = useQuery({
    queryKey: ['compose-project-revisions', project.name],
    queryFn: ({ signal }) => composeRevisions(project.name, signal),
  });
  const id = selected ?? revisions.data?.[0]?.id;
  const revision = useQuery({
    queryKey: ['compose-project-revision', project.name, id],
    queryFn: ({ signal }) => composeRevision(project.name, id!, signal),
    enabled: !!id,
  });
  const error = revisions.error ?? revision.error;

  async function rollback() {
    if (!id) return;
    setBusy(true);
    try {
      const { taskId } = await rollbackComposeRevision(project.name, id, apply);
      onTask(taskId, apply);
      onClose();
    } catch {
      // 请求错误由统一 API 提示展示。
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      centered
      width={LAYOUT.toolboxWidth}
      title={`版本历史 · ${project.name}`}
      onCancel={onClose}
      footer={[
        <Button key="close" onClick={onClose}>
          关闭
        </Button>,
        <Button
          key="rollback"
          type="primary"
          loading={busy}
          disabled={!revision.data || !!error || (apply && !online)}
          onClick={() => void rollback()}
        >
          回滚到此版本
        </Button>,
      ]}
    >
      <Flex vertical gap={12}>
        {error && <Alert type="error" message={error.message} showIcon />}
        {revisions.isPending ? (
          <Skeleton active />
        ) : revisions.data?.length ? (
          <>
            <Flex wrap gap={12} align="center">
              <Select
                aria-label="配置版本"
                value={id}
                style={{ minWidth: 200 }}
                options={revisions.data.map((item) => ({
                  value: item.id,
                  label: `${formatTime(item.createdAt)}${item.createdBy ? ` · ${item.createdBy}` : ''}`,
                }))}
                onChange={setSelected}
              />
              <Checkbox
                checked={apply}
                disabled={!online}
                onChange={(event) => setApply(event.target.checked)}
              >
                回滚后立即应用
              </Checkbox>
            </Flex>
            <Flex wrap gap={12} align="center">
              <Segmented
                aria-label="版本对比文件"
                value={file}
                onChange={setFile}
                options={[
                  { value: 'yaml', label: '编排文件' },
                  { value: 'env', label: '环境变量（已遮罩）' },
                ]}
              />
              <Typography.Text type="secondary">
                对比历史版本与当前配置。回滚同时恢复编排文件和环境变量。
              </Typography.Text>
            </Flex>
            {revision.data ? (
              <DiffEditor
                height="45dvh"
                key={file}
                language={file === 'env' ? 'plaintext' : 'yaml'}
                original={
                  file === 'env' ? revision.data.env : revision.data.yaml
                }
                modified={file === 'env' ? project.env : project.yaml}
                theme={
                  mode === 'dark' || (mode === 'system' && systemDark)
                    ? 'vs-dark'
                    : 'vs'
                }
                options={{
                  readOnly: true,
                  originalEditable: false,
                  minimap: { enabled: false },
                  renderSideBySide: !!md,
                  scrollBeyondLastLine: false,
                }}
              />
            ) : revision.isFetching ? (
              <Skeleton active />
            ) : null}
          </>
        ) : !error ? (
          <Typography.Text type="secondary">暂无历史版本</Typography.Text>
        ) : null}
      </Flex>
    </Modal>
  );
}
