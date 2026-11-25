import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Input,
  Modal,
  Segmented,
  Space,
  Switch,
  Tabs,
  Tooltip,
  Typography,
} from 'antd';
import { FileOutlined, FolderOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import DatabaseTrash from '../database/DatabaseTrash';
import TaskDrawer from '../../components/TaskDrawer';
import {
  files,
  fileTask,
  type TrashConfig,
  type TrashEntry,
} from '../../api/file';
import { formatTime, formatSize } from '../../utils/format';
import { SPACING } from '../../styles/tokens';
type Props = {
  open: boolean;
  onClose: () => void;
  config?: TrashConfig;
  refresh?: () => void;
  onTask?: (id: string, retry?: () => void) => void;
  initialTab?: 'files' | 'database';
};
export default function TrashModal(props: Props) {
  const [tab, setTab] = useState(props.initialTab ?? 'files');
  const [task, setTask] = useState<string>();
  const client = useQueryClient();
  const config = useQuery({
    queryKey: ['trash-config'],
    queryFn: () => files<TrashConfig>('/trash/config'),
    enabled: props.open && tab === 'files',
    staleTime: 0,
  });
  const refresh = () => {
    props.refresh?.();
    void client.invalidateQueries({ queryKey: ['trash-config'] });
    void client.invalidateQueries({ queryKey: ['trash'] });
    void client.invalidateQueries({ queryKey: ['database-trash'] });
  };
  const onTask = (id: string, retry?: () => void) => {
    if (props.onTask) props.onTask(id, retry);
    else setTask(id);
  };
  return (
    <>
      <Modal
        open={props.open}
        onCancel={props.onClose}
        title="回收站"
        width={1000}
        footer={null}
        destroyOnHidden
      >
        <Tabs
          activeKey={tab}
          onChange={(value) => setTab(value as typeof tab)}
          destroyOnHidden
          items={[
            {
              key: 'files',
              label: '文件回收站',
              children: (
                <>
                  {config.error && (
                    <Alert type="error" message={config.error.message} />
                  )}
                  <FileTrashBody
                    open={props.open && tab === 'files'}
                    config={config.data ?? props.config}
                    refresh={refresh}
                    onTask={onTask}
                  />
                </>
              ),
            },
            {
              key: 'database',
              label: '数据库回收站',
              children: <DatabaseTrash onTask={setTask} />,
            },
          ]}
        />
      </Modal>
      <TaskDrawer
        open={!!task}
        taskId={task ?? null}
        title="回收站任务"
        onClose={() => setTask(undefined)}
        onSettled={refresh}
      />
    </>
  );
}
function FileTrashBody({
  open,
  config,
  refresh,
  onTask,
}: {
  open: boolean;
  config?: TrashConfig;
  refresh: () => void;
  onTask: (id: string, retry?: () => void) => void;
}) {
  const { modal } = App.useApp();
  const [type, setType] = useState('all');
  const [keyword, setKeyword] = useState('');
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [switching, setSwitching] = useState(false);
  const [emptyOpen, setEmptyOpen] = useState(false);
  const [confirmation, setConfirmation] = useState('');
  const [emptying, setEmptying] = useState(false);
  const query = useQuery({
    queryKey: ['trash', type, keyword, page],
    enabled: open,
    staleTime: 0,
    queryFn: () =>
      files<{ list: TrashEntry[]; total: number }>('/trash', {
        params: { type, keyword, page, page_size: 20 },
      }),
  });
  const changed = () => {
    setSelected([]);
    refresh();
  };
  async function submit(
    action: string,
    ids?: React.Key[],
    confirmation?: string,
  ) {
    const r = await fileTask(
      action,
      ids ? { ids } : { confirmation },
      action === '/trash' ? 'DELETE' : 'POST',
    );
    onTask(r.taskId, () => {
      if (action === '/trash/empty') {
        setConfirmation('');
        setEmptyOpen(true);
      } else purge(ids ?? []);
    });
    changed();
  }
  function restore(ids: React.Key[]) {
    let overwrite = false;
    modal.confirm({
      title: `还原 ${ids.length} 项`,
      content: (
        <Space direction="vertical">
          <Typography.Text>
            目标已存在时默认保留原文件。覆盖仅适用于普通文件。
          </Typography.Text>
          <Switch
            checkedChildren="覆盖普通文件"
            unCheckedChildren="不覆盖"
            onChange={(value) => {
              overwrite = value;
            }}
          />
        </Space>
      ),
      onOk: async () => {
        const r = await fileTask('/trash/restore', { ids, overwrite });
        onTask(r.taskId, () => restore(ids));
        changed();
      },
    });
  }
  function purge(ids: React.Key[]) {
    modal.confirm({
      title: `彻底删除 ${ids.length} 项？`,
      content: '删除后不可恢复。',
      okButtonProps: { danger: true },
      onOk: () => submit('/trash', ids),
    });
  }
  return (
    <>
      <Space wrap style={{ marginBottom: SPACING.md }}>
        <Segmented
          value={type}
          options={[
            { label: '全部', value: 'all' },
            { label: '文件', value: 'file' },
            { label: '目录', value: 'dir' },
            { label: '图片', value: 'image' },
            { label: '文档', value: 'doc' },
          ]}
          onChange={(value) => {
            setType(value);
            setPage(1);
            setSelected([]);
          }}
        />
        <Input.Search
          placeholder="搜索文件名"
          allowClear
          onSearch={(value) => {
            setKeyword(value);
            setPage(1);
            setSelected([]);
          }}
        />
        <Space>
          文件回收站
          <Switch
            checked={config?.enabled ?? true}
            loading={switching}
            onChange={async (enabled) => {
              setSwitching(true);
              try {
                await files('/trash/config', {
                  method: 'PUT',
                  data: { enabled },
                });
                refresh();
              } finally {
                setSwitching(false);
              }
            }}
          />
        </Space>
        <Button
          danger
          disabled={!config?.count}
          onClick={() => {
            setConfirmation('');
            setEmptyOpen(true);
          }}
        >
          清空回收站
        </Button>
      </Space>
      <Space style={{ marginBottom: SPACING.md }}>
        <Button disabled={!selected.length} onClick={() => restore(selected)}>
          批量还原
        </Button>
        <Button
          danger
          disabled={!selected.length}
          onClick={() => purge(selected)}
        >
          批量彻底删除
        </Button>
        <Typography.Text type="secondary">
          {formatSize(config?.totalSize ?? 0)} /{' '}
          {formatSize(config?.maxSize ?? 0)}
        </Typography.Text>
      </Space>
      {query.error && (
        <Alert
          type="error"
          message="回收站读取失败"
          action={<Button onClick={() => void query.refetch()}>重试</Button>}
        />
      )}
      <ContentSizedTable<TrashEntry>
        legacySizing
        fillColumn="originalPath"
        size="small"
        rowKey="id"
        loading={query.isFetching}
        dataSource={query.data?.list ?? []}
        scroll={{ y: 400 }}
        rowSelection={{ selectedRowKeys: selected, onChange: setSelected }}
        pagination={{
          current: page,
          pageSize: 20,
          total: query.data?.total ?? 0,
          showSizeChanger: false,
          onChange: (p) => {
            setPage(p);
            setSelected([]);
          },
        }}
        columns={[
          {
            title: '文件名',
            dataIndex: 'name',
            render: (name: string, row) => (
              <Space>
                {row.isDir ? <FolderOutlined /> : <FileOutlined />}
                {name}
              </Space>
            ),
          },
          {
            title: '原路径',
            dataIndex: 'originalPath',
            ellipsis: true,
            width: 250,
            render: (p: string) => <Tooltip title={p}>{p}</Tooltip>,
          },
          {
            title: '大小',
            dataIndex: 'size',
            align: 'right',
            render: formatSize,
          },
          { title: '删除时间', dataIndex: 'deletedAt', render: formatTime },
          {
            title: '操作',
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  size="small"
                  onClick={() => restore([row.id])}
                >
                  还原
                </Button>
                <Button
                  type="link"
                  danger
                  size="small"
                  onClick={() => purge([row.id])}
                >
                  彻底删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        open={emptyOpen}
        title="清空文件回收站"
        onCancel={() => setEmptyOpen(false)}
        confirmLoading={emptying}
        okButtonProps={{
          danger: true,
          disabled: confirmation !== '确认清空文件回收站',
        }}
        onOk={async () => {
          setEmptying(true);
          try {
            await submit('/trash/empty', undefined, confirmation);
            setEmptyOpen(false);
          } finally {
            setEmptying(false);
          }
        }}
      >
        <Typography.Paragraph>
          所有文件将永久删除。输入「确认清空文件回收站」继续。
        </Typography.Paragraph>
        <Input
          aria-label="确认清空文件回收站"
          value={confirmation}
          onChange={(e) => setConfirmation(e.target.value)}
        />
      </Modal>
    </>
  );
}
