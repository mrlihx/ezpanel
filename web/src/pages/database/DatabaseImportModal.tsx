import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Flex,
  Form,
  Input,
  Modal,
  Space,
  Tag,
  Typography,
} from 'antd';
import {
  FileTextOutlined,
  FolderOpenOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import { request } from '../../api/client';
import { databaseImportPath, type ImportFile } from '../../api/database';
import type { TaskSummary } from '../../api/task';
import ContentSizedTable from '../../components/ContentSizedTable';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { formatSize, formatTime } from '../../utils/format';

const pageSize = 10;
const taskStatus = {
  running: { color: 'processing', label: '执行中' },
  success: { color: 'success', label: '成功' },
  failed: { color: 'error', label: '失败' },
  interrupted: { color: 'warning', label: '已中断' },
};

export default function DatabaseImportModal({
  serverId,
  serverName,
  database,
  onClose,
  onTask,
}: {
  serverId: string;
  serverName: string;
  database: string;
  onClose: () => void;
  onTask: (id: string) => void;
}) {
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const fileInput = useRef<HTMLInputElement>(null);
  const [localPath, setLocalPath] = useState<string>();
  const [fileToRun, setFileToRun] = useState<ImportFile>();
  const [confirmation, setConfirmation] = useState('');
  const [search, setSearch] = useState('');
  const [keyword, setKeyword] = useState('');
  const [page, setPage] = useState(1);
  const [logPage, setLogPage] = useState(1);
  const [showLogs, setShowLogs] = useState(false);
  const path = databaseImportPath(serverId, database);
  const files = useQuery({
    queryKey: ['database-import-files', path, page, keyword],
    queryFn: ({ signal }) =>
      request<{ directory: string; list: ImportFile[]; total: number }>(path, {
        params: { page, page_size: pageSize, keyword },
        signal,
        silentError: true,
      }),
  });
  const logs = useQuery({
    queryKey: ['database-import-logs', path, logPage],
    queryFn: ({ signal }) =>
      request<{ list: TaskSummary[]; total: number }>(`${path}/logs`, {
        params: { page: logPage, page_size: pageSize },
        signal,
        silentError: true,
      }),
    enabled: showLogs,
    refetchInterval: (query) =>
      query.state.data?.list.some((task) => task.status === 'running')
        ? 2000
        : false,
  });
  const stage = useMutation({
    mutationFn: (source: File | string) => {
      if (typeof source === 'string') {
        return request(`${path}/local`, {
          method: 'POST',
          data: { path: source },
        });
      }
      const data = new FormData();
      data.append('file', source);
      return request(`${path}/upload`, { method: 'POST', data });
    },
    onSuccess: () => {
      setLocalPath(undefined);
      setShowLogs(false);
      setPage(1);
      setSearch('');
      setKeyword('');
      void message.success('备份文件已添加');
      return client.invalidateQueries({
        queryKey: ['database-import-files', path],
      });
    },
  });
  const start = useMutation({
    mutationFn: (file: ImportFile) =>
      request<{ taskId: string }>(`${path}/run`, {
        method: 'POST',
        data: {
          ...(file.backupId
            ? { backupId: file.backupId }
            : { name: file.name }),
          confirmDatabase: confirmation,
        },
      }),
    onSuccess: ({ taskId }) => {
      setFileToRun(undefined);
      setLogPage(1);
      onTask(taskId);
      return client.invalidateQueries({
        queryKey: ['database-import-logs', path],
      });
    },
  });
  const grid = showLogs ? logs : files;

  return (
    <>
      <Modal
        open
        title="导入本地备份"
        width={LAYOUT.trashModalWidth}
        footer={null}
        maskClosable={false}
        closable={!stage.isPending && !start.isPending}
        keyboard={!stage.isPending && !start.isPending}
        onCancel={onClose}
      >
        <Flex vertical gap={SPACING.lg}>
          <Typography.Text type="secondary">
            目标数据库：{serverName} / {database}
          </Typography.Text>
          <Flex align="center" justify="space-between" gap={SPACING.sm} wrap>
            <Space wrap>
              <input
                type="file"
                ref={fileInput}
                accept=".sql,.zip,.sql.gz,.tar.gz,.gz,.tgz"
                hidden
                onChange={(event) => {
                  const file = event.currentTarget.files?.[0];
                  event.currentTarget.value = '';
                  if (file) stage.mutate(file);
                }}
              />
              <Button
                type="primary"
                icon={<UploadOutlined />}
                loading={stage.isPending && localPath === undefined}
                disabled={stage.isPending}
                onClick={() => fileInput.current?.click()}
              >
                从本地上传
              </Button>
              <Button
                icon={<FolderOpenOutlined />}
                disabled={stage.isPending}
                onClick={() => setLocalPath('')}
              >
                从本机导入
              </Button>
              <Button
                icon={<FileTextOutlined />}
                onClick={() => setShowLogs(!showLogs)}
              >
                {showLogs ? '返回备份文件' : '显示日志'}
              </Button>
            </Space>
            {!showLogs && (
              <Input.Search
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="搜索文件名"
                aria-label="搜索文件名"
                allowClear
                style={{ width: LAYOUT.appSearchWidth, maxWidth: '100%' }}
                onSearch={(value) => {
                  setKeyword(value.trim());
                  setPage(1);
                }}
              />
            )}
          </Flex>
          {grid.error && <Alert type="error" message={grid.error.message} />}
          {showLogs ? (
            <ContentSizedTable<TaskSummary>
              rowKey="taskId"
              fillColumn="startedAt"
              size="small"
              loading={logs.isFetching}
              dataSource={logs.data?.list}
              locale={{ emptyText: '暂无导入记录' }}
              pagination={{
                current: logPage,
                pageSize,
                total: logs.data?.total,
                showSizeChanger: false,
                onChange: setLogPage,
              }}
              columns={[
                {
                  title: '开始时间',
                  dataIndex: 'startedAt',
                  render: formatTime,
                },
                {
                  title: '状态',
                  render: (_, task) => (
                    <Tag color={taskStatus[task.status].color}>
                      {taskStatus[task.status].label}
                    </Tag>
                  ),
                },
                {
                  title: '操作',
                  align: 'right',
                  render: (_, task) => (
                    <Button
                      type="link"
                      size="small"
                      onClick={() => onTask(task.taskId)}
                    >
                      查看日志
                    </Button>
                  ),
                },
              ]}
            />
          ) : (
            <ContentSizedTable<ImportFile>
              rowKey={(file) =>
                file.backupId
                  ? `backup:${file.backupId}`
                  : `staged:${file.name}`
              }
              fillColumn="name"
              size="small"
              loading={files.isFetching}
              dataSource={files.data?.list}
              locale={{ emptyText: keyword ? '无匹配文件' : '暂无备份文件' }}
              pagination={{
                current: page,
                pageSize,
                total: files.data?.total,
                showSizeChanger: false,
                onChange: setPage,
              }}
              columns={[
                { title: '文件名', dataIndex: 'name', ellipsis: true },
                {
                  title: '来源',
                  render: (_, file) =>
                    file.backupId ? '本机备份' : '暂存文件',
                },
                {
                  title: '修改时间',
                  dataIndex: 'modTime',
                  width: 185,
                  render: formatTime,
                },
                {
                  title: '大小',
                  dataIndex: 'size',
                  align: 'right',
                  render: formatSize,
                },
                {
                  title: '操作',
                  align: 'right',
                  render: (_, file) => (
                    <Space size={SPACING.xs}>
                      <Button
                        type="link"
                        size="small"
                        onClick={() => {
                          setConfirmation('');
                          setFileToRun(file);
                        }}
                      >
                        导入
                      </Button>
                      {!file.backupId && (
                        <Button
                          type="link"
                          size="small"
                          danger
                          onClick={() =>
                            modal.confirm({
                              title: '删除暂存备份？',
                              content: file.name,
                              okButtonProps: { danger: true },
                              onOk: async () => {
                                await request(path, {
                                  method: 'DELETE',
                                  data: { name: file.name },
                                });
                                if (page > 1 && files.data?.list.length === 1)
                                  setPage(page - 1);
                                await client.invalidateQueries({
                                  queryKey: ['database-import-files', path],
                                });
                              },
                            })
                          }
                        >
                          删除
                        </Button>
                      )}
                    </Space>
                  ),
                },
              ]}
            />
          )}
          <Flex vertical gap={SPACING.xs}>
            <Typography.Text type="secondary">
              支持 .sql、.zip、.sql.gz、.tar.gz、.gz、.tgz。ZIP 和 TAR
              压缩包须包含一个 SQL 文件。
            </Typography.Text>
            {files.data?.directory && (
              <Typography.Text
                type="secondary"
                style={{ overflowWrap: 'anywhere' }}
              >
                暂存目录：{files.data.directory}
              </Typography.Text>
            )}
          </Flex>
        </Flex>
      </Modal>
      <Modal
        open={localPath !== undefined}
        title="从本机导入"
        onCancel={() => setLocalPath(undefined)}
        confirmLoading={stage.isPending}
        closable={!stage.isPending}
        keyboard={!stage.isPending}
        maskClosable={false}
        okText="添加文件"
        cancelButtonProps={{ disabled: stage.isPending }}
        okButtonProps={{ disabled: !localPath?.trim() }}
        onOk={() => stage.mutate(localPath!.trim())}
      >
        <Form layout="horizontal" colon={false}>
          <Form.Item
            label="服务器文件路径"
            htmlFor="database-import-local-path"
          >
            <Input
              id="database-import-local-path"
              value={localPath ?? ''}
              disabled={stage.isPending}
              placeholder="/www/backup/example.sql.gz"
              onChange={(event) => setLocalPath(event.target.value)}
            />
          </Form.Item>
        </Form>
        <Typography.Text type="secondary">
          输入面板服务器上的文件绝对路径，添加后可在列表中执行导入。
        </Typography.Text>
      </Modal>
      <Modal
        open={fileToRun !== undefined}
        title="确认导入备份"
        onCancel={() => setFileToRun(undefined)}
        confirmLoading={start.isPending}
        closable={!start.isPending}
        keyboard={!start.isPending}
        maskClosable={false}
        okText="开始导入"
        cancelButtonProps={{ disabled: start.isPending }}
        okButtonProps={{ danger: true, disabled: confirmation !== database }}
        onOk={() => start.mutate(fileToRun!)}
      >
        <Flex vertical gap={SPACING.md}>
          <Typography.Text style={{ overflowWrap: 'anywhere' }}>
            将 {fileToRun?.name} 导入 {serverName} / {database}
          </Typography.Text>
          <Alert
            type="warning"
            showIcon
            message="请使用可信的单库备份。SQL 可能覆盖已有数据，也可能通过 USE 或带库名的语句修改其他库。导入失败后已执行的语句可能已生效。"
          />
          <Form layout="horizontal" colon={false}>
            <Form.Item
              label="确认数据库名"
              htmlFor="database-import-confirmation"
              style={{ marginBottom: 0 }}
            >
              <Input
                id="database-import-confirmation"
                placeholder={database}
                value={confirmation}
                disabled={start.isPending}
                onChange={(event) => setConfirmation(event.target.value)}
              />
            </Form.Item>
          </Form>
        </Flex>
      </Modal>
    </>
  );
}
