import { lazy, Suspense, useState } from 'react';
import {
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Skeleton,
  Upload,
  theme,
} from 'antd';
import { UploadOutlined } from '@ant-design/icons';
import { request } from '../../api/client';
import { LAYOUT } from '../../styles/tokens';
const CodeEditor = lazy(() => import('../../components/CodeEditor'));
export default function ImageTaskModal({
  mode,
  onClose,
  onSubmitted,
}: {
  mode: 'load' | 'build';
  onClose: () => void;
  onSubmitted: (id: string) => void;
}) {
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [form] = Form.useForm();
  const [source, setSource] = useState<'inline' | 'upload' | 'server'>(
    'inline',
  );
  const [content, setContent] = useState('FROM alpine:latest\n');
  const [fileName, setFileName] = useState('');
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      open
      title={mode === 'load' ? '导入镜像' : '构建镜像'}
      width={mode === 'build' ? 860 : LAYOUT.databaseModalWidth}
      onCancel={onClose}
      confirmLoading={busy}
      okText="提交任务"
      onOk={() => form.submit()}
      styles={{ body: { maxHeight: '70dvh', overflowY: 'auto' } }}
    >
      <Form
        form={form}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1, style: { minWidth: 0 } }}
        colon={false}
        onFinish={async (values) => {
          if (mode === 'build' && source === 'upload' && !fileName) {
            void message.error('请选择Dockerfile');
            return;
          }
          setBusy(true);
          try {
            const result = await request<{ taskId: string }>(
              `/api/docker/images/${mode}`,
              {
                method: 'POST',
                data:
                  mode === 'load'
                    ? { path: values.path }
                    : {
                        tag: values.tag,
                        contextPath: values.contextPath ?? '',
                        ...(source === 'server'
                          ? { filePath: values.filePath }
                          : { dockerfile: content }),
                      },
              },
            );
            onSubmitted(result.taskId);
            onClose();
          } finally {
            setBusy(false);
          }
        }}
      >
        {mode === 'load' ? (
          <Form.Item
            name="path"
            label="镜像归档"
            rules={[{ required: true }]}
            extra="填写服务器上的绝对路径，支持docker save导出的tar及Docker支持的压缩归档。"
          >
            <Input placeholder="/www/backup/images/app.tar.gz" />
          </Form.Item>
        ) : (
          <>
            <Form.Item name="tag" label="镜像名称" rules={[{ required: true }]}>
              <Input placeholder="my-app:latest" />
            </Form.Item>
            <Form.Item label="Dockerfile来源">
              <Select
                value={source}
                onChange={setSource}
                options={[
                  { value: 'inline', label: '直接输入内容' },
                  { value: 'upload', label: '选择本机文件' },
                  { value: 'server', label: '服务器文件' },
                ]}
              />
            </Form.Item>
            {source === 'server' ? (
              <Form.Item
                name="filePath"
                label="Dockerfile路径"
                rules={[{ required: true }]}
              >
                <Input placeholder="/www/project/Dockerfile" />
              </Form.Item>
            ) : (
              <>
                {source === 'upload' && (
                  <Form.Item label="选择文件">
                    <Upload
                      maxCount={1}
                      showUploadList={false}
                      beforeUpload={async (file) => {
                        if (file.size > 1024 * 1024) {
                          void message.error('Dockerfile不能超过1MiB');
                          return false;
                        }
                        setContent(await file.text());
                        setFileName(file.name);
                        return false;
                      }}
                    >
                      <Button icon={<UploadOutlined />}>选择Dockerfile</Button>
                    </Upload>
                    {fileName && (
                      <span style={{ marginLeft: 12 }}>{fileName}</span>
                    )}
                  </Form.Item>
                )}
                <Form.Item label="Dockerfile">
                  <div
                    style={{
                      height: 300,
                      border: `1px solid ${token.colorBorder}`,
                      borderRadius: 6,
                      overflow: 'hidden',
                    }}
                  >
                    <Suspense fallback={<Skeleton active />}>
                      <CodeEditor
                        path="docker-build.Dockerfile"
                        value={content}
                        onChange={setContent}
                      />
                    </Suspense>
                  </div>
                </Form.Item>
              </>
            )}
            <Form.Item
              name="contextPath"
              label="构建目录"
              extra={
                source === 'server'
                  ? '留空使用Dockerfile所在目录，遵循目录内的.dockerignore。'
                  : '可选服务器目录，COPY/ADD所需文件放在此目录；留空时仅包含Dockerfile。'
              }
            >
              <Input placeholder="/www/project" />
            </Form.Item>
          </>
        )}
      </Form>
    </Modal>
  );
}
