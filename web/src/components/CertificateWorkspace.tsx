import { useState } from 'react';
import ResourceActions, {
  useResourceActions,
} from '../plugins/ResourceActions';
import {
  Alert,
  App,
  Button,
  Descriptions,
  Flex,
  Form,
  Input,
  Modal,
  Space,
  Tag,
  Typography,
  Upload,
} from 'antd';
import { UploadOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import ContentSizedTable from './ContentSizedTable';
import {
  certificate,
  certificates,
  deleteCertificate,
  downloadCertificate,
  importCertificate,
  type Certificate,
} from '../api/certificate';
import { LAYOUT, MONO_FONT, SPACING } from '../styles/tokens';
import { formatTime } from '../utils/format';
import CertificateRequest from './CertificateRequest';

type Props = {
  mode: 'free' | 'folder';
  defaultDomains?: string[];
  siteId?: string;
  autoTempSite?: boolean;
  onDeploy: (id: string) => Promise<void | false>;
};
export default function CertificateWorkspace(props: Props) {
  if (props.mode === 'free') return <CertificateRequest {...props} />;
  return <CertificateFolder {...props} />;
}
function CertificateFolder({ onDeploy, defaultDomains, siteId }: Props) {
  const pluginActions = useResourceActions('certificates');
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(10);
  const [keyword, setKeyword] = useState('');
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [viewId, setViewId] = useState<string>();
  const [importing, setImporting] = useState(false);
  const [renewal, setRenewal] = useState<Certificate>();
  const [form] = Form.useForm<{ certificate: string; privateKey: string }>();
  const list = useQuery({
    queryKey: ['certificates', page, size, keyword],
    queryFn: () => certificates(page, size, keyword),
  });
  const detail = useQuery({
    queryKey: ['certificate', viewId],
    queryFn: () => certificate(viewId!),
    enabled: !!viewId,
  });
  const run = async (key: string, action: () => Promise<void>) => {
    setBusy(key);
    setError('');
    try {
      await action();
    } catch (e) {
      setError(e instanceof Error ? e.message : '操作失败');
    } finally {
      setBusy('');
    }
  };
  const upload = async (file: File, field: 'certificate' | 'privateKey') => {
    if (file.size > 2 * 1024 * 1024) {
      void message.error('单个 PEM 文件不能超过 2MiB');
      return false;
    }
    form.setFieldValue(field, await file.text());
    return false;
  };
  return (
    <Flex vertical gap={SPACING.lg}>
      <Flex gap={SPACING.sm} wrap justify="space-between">
        <Input.Search
          aria-label="搜索证书域名或签发机构"
          placeholder="搜索域名或签发机构"
          allowClear
          style={{ width: LAYOUT.fileSearchWidth, maxWidth: '100%' }}
          onSearch={(value) => {
            setKeyword(value);
            setPage(1);
          }}
        />
        <Space>
          <Button loading={list.isFetching} onClick={() => void list.refetch()}>
            刷新
          </Button>
          <Button
            type="primary"
            onClick={() => {
              setError('');
              form.resetFields();
              setImporting(true);
            }}
          >
            导入证书
          </Button>
        </Space>
      </Flex>
      {error && !importing && (
        <Alert
          type="error"
          showIcon
          message={error}
          closable
          onClose={() => setError('')}
        />
      )}
      {list.isError && <Alert type="error" message={list.error.message} />}
      <ContentSizedTable<Certificate>
        fillColumn="domains"
        rowKey="id"
        size="small"
        loading={list.isPending}
        dataSource={list.data?.list ?? []}
        locale={{ emptyText: '暂无证书，可导入 PEM 或前往免费证书页申请' }}
        pagination={{
          current: page,
          pageSize: size,
          total: list.data?.total ?? 0,
          showSizeChanger: true,
          showTotal: (total) => `共 ${total} 张证书`,
          onChange: (next, nextSize) => {
            setPage(nextSize === size ? next : 1);
            setSize(nextSize);
          },
        }}
        columns={[
          {
            title: '域名',
            dataIndex: 'domains',
            render: (domains: string[]) => (
              <Typography.Text style={{ overflowWrap: 'anywhere' }}>
                {domains.join('、')}
              </Typography.Text>
            ),
          },
          { title: '签发机构', dataIndex: 'issuer' },
          { title: '算法', dataIndex: 'algorithm' },
          {
            title: '到期时间',
            dataIndex: 'notAfter',
            render: (value: string) => (
              <Space direction="vertical" size={0}>
                <span>{formatTime(value)}</span>
                {new Date(value).getTime() <= Date.now() && (
                  <Tag color="error">已过期</Tag>
                )}
              </Space>
            ),
          },
          { title: '位置', width: 70, render: () => '本地' },
          {
            title: '操作',
            width: 256,
            render: (_, row) => (
              <Space size={0} wrap>
                <Button
                  type="link"
                  size="small"
                  onClick={() => setViewId(row.id)}
                >
                  查看
                </Button>
                <Button
                  type="link"
                  size="small"
                  disabled={!!busy}
                  loading={busy === `deploy-${row.id}`}
                  onClick={() =>
                    void run(`deploy-${row.id}`, async () => {
                      if ((await onDeploy(row.id)) === false) return;
                      void message.success('证书已部署');
                    })
                  }
                >
                  部署
                </Button>
                <Button
                  type="link"
                  size="small"
                  disabled={!!busy}
                  onClick={() =>
                    void run(`download-${row.id}`, () =>
                      downloadCertificate(row.id, siteId),
                    )
                  }
                >
                  下载
                </Button>
                {row.source === 'letsencrypt' && (
                  <Button
                    type="link"
                    size="small"
                    onClick={() => setRenewal(row)}
                  >
                    续签
                  </Button>
                )}
                <ResourceActions
                  items={pluginActions(row.id, row.domains.join('、'))}
                />
                <Button
                  type="link"
                  size="small"
                  danger
                  disabled={!!busy}
                  onClick={() =>
                    modal.confirm({
                      title: '删除证书？',
                      content: `${row.domains.join('、')}。正在部署使用的证书不能删除。`,
                      okText: '删除',
                      okButtonProps: { danger: true },
                      onOk: async () => {
                        try {
                          await deleteCertificate(row.id);
                        } catch (e) {
                          void message.error(
                            e instanceof Error ? e.message : '删除失败',
                          );
                          throw e;
                        }
                        void message.success('证书已删除');
                        if (list.data?.list.length === 1 && page > 1)
                          setPage(page - 1);
                        await client.invalidateQueries({
                          queryKey: ['certificates'],
                        });
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
      <Typography.Text type="secondary">
        下载 ZIP
        包含证书链和私钥，请妥善保管。导入及查看界面不会回显已保存的私钥。
      </Typography.Text>
      <Modal
        title="导入证书"
        open={importing}
        width={LAYOUT.databaseModalWidth}
        okText="导入"
        confirmLoading={busy === 'import'}
        onOk={() => form.submit()}
        onCancel={() => {
          if (!busy) {
            setImporting(false);
            form.resetFields();
          }
        }}
        destroyOnHidden
      >
        <Form
          form={form}
          layout="vertical"
          preserve={false}
          onFinish={(values) =>
            void run('import', async () => {
              await importCertificate(
                values.certificate.trim(),
                values.privateKey.trim(),
              );
              setImporting(false);
              form.resetFields();
              void message.success('证书已导入');
              await client.invalidateQueries({ queryKey: ['certificates'] });
            })
          }
        >
          <Form.Item
            name="certificate"
            label="证书链（PEM）"
            rules={[
              {
                required: true,
                whitespace: true,
                message: '请粘贴或上传证书链',
              },
            ]}
          >
            <Input.TextArea
              rows={7}
              spellCheck={false}
              style={{ fontFamily: MONO_FONT }}
              placeholder="-----BEGIN CERTIFICATE-----"
            />
          </Form.Item>
          <Upload
            accept=".pem,.crt,.cer"
            showUploadList={false}
            beforeUpload={(file) => upload(file, 'certificate')}
          >
            <Button icon={<UploadOutlined />}>上传证书链</Button>
          </Upload>
          <Form.Item
            name="privateKey"
            label="私钥（KEY）"
            style={{ marginTop: SPACING.lg }}
            rules={[
              {
                required: true,
                whitespace: true,
                message: '请粘贴或上传匹配的私钥',
              },
            ]}
          >
            <Input.TextArea
              rows={7}
              autoComplete="off"
              spellCheck={false}
              style={{ fontFamily: MONO_FONT }}
              placeholder="-----BEGIN PRIVATE KEY-----"
            />
          </Form.Item>
          <Upload
            accept=".pem,.key"
            showUploadList={false}
            beforeUpload={(file) => upload(file, 'privateKey')}
          >
            <Button icon={<UploadOutlined />}>上传私钥</Button>
          </Upload>
          {error && (
            <Alert
              style={{ marginTop: SPACING.lg }}
              type="error"
              message={error}
            />
          )}
        </Form>
      </Modal>
      <Modal
        open={!!viewId}
        title="证书详情"
        loading={detail.isPending && !!viewId}
        width={LAYOUT.databaseModalWidth}
        onCancel={() => setViewId(undefined)}
        footer={
          <Button
            loading={detail.isFetching}
            onClick={() => void detail.refetch()}
          >
            刷新
          </Button>
        }
      >
        {detail.isError && (
          <Alert type="error" message={detail.error.message} />
        )}
        {detail.data && (
          <Flex vertical gap={SPACING.lg}>
            <Descriptions
              column={1}
              size="small"
              items={[
                {
                  key: 'domains',
                  label: '域名',
                  children: detail.data.domains.join('、'),
                },
                {
                  key: 'algorithm',
                  label: '证书算法',
                  children: detail.data.algorithm,
                },
                {
                  key: 'issuer',
                  label: '签发机构',
                  children: detail.data.issuer,
                },
                {
                  key: 'from',
                  label: '生效时间',
                  children: formatTime(detail.data.notBefore),
                },
                {
                  key: 'until',
                  label: '到期时间',
                  children: formatTime(detail.data.notAfter),
                },
                {
                  key: 'source',
                  label: '来源',
                  children:
                    detail.data.source === 'letsencrypt'
                      ? "Let's Encrypt"
                      : '手动导入',
                },
                {
                  key: 'key',
                  label: '私钥',
                  children: detail.data.keyConfigured ? '已配置' : '未配置',
                },
              ]}
            />
            <Typography.Text strong>证书内容（PEM）</Typography.Text>
            <Input.TextArea
              aria-label="证书内容"
              readOnly
              rows={8}
              spellCheck={false}
              value={detail.data.certificate}
              style={{ fontFamily: MONO_FONT }}
            />
            {detail.data.privateKey && (
              <>
                <Flex justify="space-between" align="center">
                  <Typography.Text strong>私钥内容（KEY）</Typography.Text>
                  <Button
                    size="small"
                    onClick={() => {
                      void navigator.clipboard
                        ?.writeText(detail.data!.privateKey ?? '')
                        .then(() => message.success('私钥已复制'));
                    }}
                  >
                    复制私钥
                  </Button>
                </Flex>
                <Input.TextArea
                  aria-label="私钥内容"
                  readOnly
                  rows={8}
                  spellCheck={false}
                  value={detail.data.privateKey}
                  style={{ fontFamily: MONO_FONT }}
                />
              </>
            )}
          </Flex>
        )}
      </Modal>
      <Modal
        open={!!renewal}
        title="续签免费证书"
        width={LAYOUT.toolboxWidth}
        footer={null}
        onCancel={() => setRenewal(undefined)}
        destroyOnHidden
      >
        {renewal && (
          <CertificateRequest
            key={renewal.id}
            renewal={renewal}
            defaultDomains={defaultDomains}
            siteId={siteId}
            onDeploy={onDeploy}
          />
        )}
      </Modal>
    </Flex>
  );
}
