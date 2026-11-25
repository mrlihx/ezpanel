import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  Modal,
  Space,
  Tooltip,
  Typography,
  theme,
  Grid,
} from 'antd';
import { RotateCw } from 'lucide-react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import ContentSizedTable from '../../components/ContentSizedTable';
import { request } from '../../api/client';
import { LAYOUT, MONO_FONT } from '../../styles/tokens';
import { formatSize } from '../../utils/format';

type Collection = {
  name: string;
  count: number | null;
  size: number | null;
  average: number | null;
  storage: number | null;
  indexes: number | null;
  indexSize: number | null;
  notice: string;
};
type Document = Record<string, unknown>;
const bytes = (value: number | null) =>
  value === null ? '—' : formatSize(value);
const idText = (value: unknown) =>
  typeof value === 'object' && value !== null && '$oid' in value
    ? String(value.$oid)
    : typeof value === 'string'
      ? value
      : JSON.stringify(value);
export default function MongoDataModal({
  prefix,
  database,
  readOnly,
  onClose,
  onChanged,
}: {
  prefix: string;
  database: string;
  readOnly: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  const { modal } = App.useApp();
  const { token } = theme.useToken();
  const client = useQueryClient();
  const [page, setPage] = useState(1),
    [size, setSize] = useState(10);
  const [collection, setCollection] = useState(''),
    [docPage, setDocPage] = useState(1),
    [docSize, setDocSize] = useState(10);
  const [editor, setEditor] = useState<{
      kind: 'collection' | 'document';
      document?: Document;
    }>(),
    [busy, setBusy] = useState(false);
  const [form] = Form.useForm();
  const collections = useQuery({
    queryKey: ['mongo-collections', prefix, database, page, size],
    queryFn: () =>
      request<{ list: Collection[]; total: number }>(`${prefix}/collections`, {
        params: { database, page, page_size: size },
      }),
  });
  const documents = useQuery({
    queryKey: [
      'mongo-documents',
      prefix,
      database,
      collection,
      docPage,
      docSize,
    ],
    queryFn: () =>
      request<{ list: Document[]; total: number; truncated: boolean }>(
        `${prefix}/documents`,
        { params: { database, collection, page: docPage, page_size: docSize } },
      ),
    enabled: !!collection,
  });
  const locked = readOnly || collection.startsWith('system.');
  function refresh() {
    void client.invalidateQueries({
      queryKey: ['mongo-collections', prefix, database],
    });
    void client.invalidateQueries({
      queryKey: ['mongo-documents', prefix, database],
    });
    onChanged();
  }
  function open(kind: 'collection' | 'document', document?: Document) {
    form.resetFields();
    form.setFieldsValue({
      document: document
        ? JSON.stringify(document, null, 2)
        : kind === 'collection'
          ? ''
          : '{}',
    });
    setEditor({ kind, document });
  }
  return (
    <>
      <Modal
        zIndex={token.zIndexPopupBase}
        title={`【${database}】集合`}
        open
        onCancel={onClose}
        footer={null}
        width={LAYOUT.toolboxWidth}
      >
        <Space style={{ marginBottom: 16 }}>
          <Button
            type="primary"
            disabled={readOnly}
            onClick={() => open('collection')}
          >
            添加集合
          </Button>
          <Button
            icon={<RotateCw size={16} strokeWidth={1.9} />}
            aria-label="刷新集合"
            loading={collections.isFetching}
            onClick={() => void collections.refetch()}
          />
        </Space>
        {collections.error && (
          <Alert type="error" message={collections.error.message} />
        )}
        <ContentSizedTable<Collection>
          fillColumn="name"
          rowKey="name"
          size="middle"
          loading={collections.isFetching}
          dataSource={collections.data?.list}
          pagination={{
            current: page,
            pageSize: size,
            total: collections.data?.total,
            showSizeChanger: true,
            pageSizeOptions: [10, 20, 50, 100],
            showTotal: (total) => `共 ${total} 条`,
            onChange: (p, s) => {
              setPage(p);
              setSize(s);
            },
          }}
          columns={[
            {
              title: '集合名称',
              dataIndex: 'name',
              width: 150,
              ellipsis: true,
            },
            {
              title: '文档数量',
              dataIndex: 'count',
              width: 100,
              render: (value) => value ?? '—',
            },
            {
              title: (
                <Tooltip title="文档未压缩的逻辑大小，不代表缓存实际占用内存">
                  内存中的大小
                </Tooltip>
              ),
              dataIndex: 'size',
              width: 130,
              render: bytes,
            },
            {
              title: '对象平均大小',
              dataIndex: 'average',
              width: 130,
              render: bytes,
            },
            {
              title: '存储大小',
              dataIndex: 'storage',
              width: 115,
              render: bytes,
            },
            {
              title: '索引数量',
              dataIndex: 'indexes',
              width: 100,
              render: (value) => value ?? '—',
            },
            {
              title: '索引大小',
              dataIndex: 'indexSize',
              width: 115,
              render: bytes,
            },
            {
              title: '操作',
              fixed: fixedActions,
              width: 170,
              render: (_, row) => (
                <Space size={4}>
                  <Tooltip title={row.notice || undefined}>
                    <Button
                      type="link"
                      size="small"
                      onClick={() => {
                        setCollection(row.name);
                        setDocPage(1);
                      }}
                    >
                      详细数据
                    </Button>
                  </Tooltip>
                  <Button
                    type="link"
                    size="small"
                    danger
                    disabled={readOnly || row.name.startsWith('system.')}
                    onClick={() =>
                      modal.confirm({
                        zIndex: token.zIndexPopupBase + 30,
                        title: `删除集合 ${row.name}？`,
                        content: '集合中的全部文档和索引将被删除，无法恢复。',
                        okButtonProps: { danger: true },
                        onOk: async () => {
                          await request(`${prefix}/collections`, {
                            method: 'DELETE',
                            params: { database, collection: row.name },
                            data: { confirmName: row.name },
                          });
                          refresh();
                        },
                      })
                    }
                  >
                    删除
                  </Button>
                </Space>
              ),
            },
          ]}
        />
        {collections.data?.list.some((row) => row.notice) && (
          <Alert
            type="warning"
            message="部分集合统计不可用，可能是视图或当前账号缺少统计权限；详细数据仍可尝试读取。"
          />
        )}
      </Modal>
      <Modal
        zIndex={token.zIndexPopupBase + 10}
        title={`集合【${collection}】详细数据`}
        open={!!collection}
        onCancel={() => setCollection('')}
        footer={null}
        width={LAYOUT.toolboxWidth}
      >
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            marginBottom: 16,
          }}
        >
          <Button
            type="primary"
            disabled={locked}
            onClick={() => open('document')}
          >
            添加文档
          </Button>
          <Button
            icon={<RotateCw size={16} strokeWidth={1.9} />}
            aria-label="刷新文档"
            loading={documents.isFetching}
            onClick={() => void documents.refetch()}
          />
        </div>
        {documents.error && (
          <Alert type="error" message={documents.error.message} />
        )}
        {documents.data?.truncated && (
          <Alert
            type="warning"
            message="当前页达到2MiB读取上限，部分文档未显示，请减少每页条数。"
          />
        )}
        <ContentSizedTable<Document>
          fillColumn="文档内容"
          rowKey={(row) => JSON.stringify(row._id)}
          size="middle"
          dataSource={documents.data?.list}
          loading={documents.isFetching}
          pagination={{
            current: docPage,
            pageSize: docSize,
            total: documents.data?.total,
            showSizeChanger: true,
            pageSizeOptions: [1, 10, 20, 50, 100],
            showTotal: (total) => `共 ${total} 条`,
            onChange: (p, s) => {
              setDocPage(p);
              setDocSize(s);
            },
          }}
          columns={[
            { title: '集合名称', width: 140, render: () => collection },
            {
              title: '文档ID',
              width: 260,
              ellipsis: true,
              render: (_, row) => (
                <Typography.Text copyable>{idText(row._id)}</Typography.Text>
              ),
            },
            {
              title: '文档内容',
              ellipsis: true,
              render: (_, row) => {
                const content = JSON.stringify(row);
                return (
                  <Tooltip
                    title={
                      content.length > 2048
                        ? content.slice(0, 2048) + '…'
                        : content
                    }
                  >
                    <span style={{ fontFamily: MONO_FONT }}>
                      {content.length > 300
                        ? content.slice(0, 300) + '…'
                        : content}
                    </span>
                  </Tooltip>
                );
              },
            },
            {
              title: '操作',
              fixed: fixedActions,
              width: 120,
              render: (_, row) => (
                <Space size={4}>
                  <Button
                    type="link"
                    size="small"
                    disabled={locked}
                    onClick={() => open('document', row)}
                  >
                    编辑
                  </Button>
                  <Button
                    type="link"
                    size="small"
                    danger
                    disabled={locked}
                    onClick={() =>
                      modal.confirm({
                        zIndex: token.zIndexPopupBase + 30,
                        title: '删除此文档？',
                        content: `文档ID：${idText(row._id)}`,
                        okButtonProps: { danger: true },
                        onOk: async () => {
                          await request(`${prefix}/documents`, {
                            method: 'DELETE',
                            params: { database, collection },
                            data: { id: row._id },
                          });
                          refresh();
                        },
                      })
                    }
                  >
                    删除
                  </Button>
                </Space>
              ),
            },
          ]}
        />
      </Modal>
      <Modal
        zIndex={token.zIndexPopupBase + 20}
        maskClosable={false}
        title={
          editor?.kind === 'collection'
            ? '添加集合'
            : editor?.document
              ? '编辑文档'
              : '添加文档'
        }
        open={!!editor}
        width={LAYOUT.databaseModalWidth}
        onCancel={() => setEditor(undefined)}
        confirmLoading={busy}
        onOk={async () => {
          const values = await form.validateFields();
          setBusy(true);
          try {
            if (editor?.kind === 'collection') {
              await request(`${prefix}/collections`, {
                method: 'POST',
                params: { database },
                data: {
                  name: values.name,
                  ...(values.document?.trim()
                    ? { document: JSON.parse(values.document) }
                    : {}),
                },
              });
            } else {
              await request(`${prefix}/documents`, {
                method: editor?.document ? 'PUT' : 'POST',
                params: { database, collection },
                data: {
                  ...(editor?.document ? { id: editor.document._id } : {}),
                  document: JSON.parse(values.document),
                },
              });
            }
            setEditor(undefined);
            refresh();
          } finally {
            setBusy(false);
          }
        }}
      >
        <Form
          form={form}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
        >
          {editor?.kind === 'collection' && (
            <Form.Item
              name="name"
              label="集合名称"
              rules={[
                {
                  required: true,
                  pattern: /^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,119}$/,
                },
              ]}
            >
              <Input />
            </Form.Item>
          )}
          <Form.Item
            name="document"
            label="文档内容"
            extra={
              editor?.kind === 'collection'
                ? '选填；填写后将在新集合中插入首条文档。'
                : editor?.document
                  ? '_id不可修改，使用Extended JSON保留原始数据类型。'
                  : undefined
            }
            rules={[
              { required: editor?.kind !== 'collection' },
              {
                validator: (_, value) => {
                  if (!value?.trim() && editor?.kind === 'collection')
                    return Promise.resolve();
                  try {
                    const parsed = JSON.parse(value);
                    return parsed &&
                      typeof parsed === 'object' &&
                      !Array.isArray(parsed)
                      ? Promise.resolve()
                      : Promise.reject(new Error('请输入JSON对象'));
                  } catch {
                    return Promise.reject(new Error('JSON格式无效'));
                  }
                },
              },
            ]}
          >
            <Input.TextArea rows={14} style={{ fontFamily: MONO_FONT }} />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
