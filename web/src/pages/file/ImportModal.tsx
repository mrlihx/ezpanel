import { useRef, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Form,
  Input,
  Modal,
  Progress,
  Space,
  Typography,
} from 'antd';
import { FileOutlined, FolderOutlined } from '@ant-design/icons';
import { fileTask, resumeUploadThreshold, type Batch } from '../../api/file';
import { LAYOUT } from '../../styles/tokens';
import {
  droppedEntries,
  pickedEntries,
  type UploadEntry,
} from './uploadEntries';

export default function ImportModal({
  type,
  path,
  onClose,
  initialEntries,
  onUpload,
  uploadProgress,
  onStarted,
}: {
  type: 'upload' | 'download' | 'git';
  path: string;
  onClose: () => void;
  initialEntries: UploadEntry[];
  onUpload: (entries: UploadEntry[], overwrite: boolean) => Promise<Batch>;
  uploadProgress: number;
  onStarted: (id: string) => void;
}) {
  const { message } = App.useApp();
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);
  const [queue, setQueue] = useState(initialEntries);
  const [overwrite, setOverwrite] = useState(false);
  const [readError, setReadError] = useState(false);
  const [reading, setReading] = useState(false);
  const [uploadFailures, setUploadFailures] = useState<Batch['results']>([]);
  const filesRef = useRef<HTMLInputElement>(null);
  const directoriesRef = useRef<HTMLInputElement>(null);
  const add = (entries: UploadEntry[]) =>
    setQueue((previous) => [
      ...new Map(
        [...previous, ...entries].map((item) => [item.path, item]),
      ).values(),
    ]);
  return (
    <Modal
      open
      title={
        type === 'upload'
          ? '上传文件/文件夹'
          : type === 'download'
            ? 'URL链接下载'
            : 'git仓库克隆'
      }
      width={LAYOUT.shellWidth}
      maskClosable={false}
      onCancel={() => {
        if (!saving) onClose();
      }}
      closable={!saving}
      keyboard={!saving}
      cancelButtonProps={{ disabled: saving }}
      confirmLoading={saving}
      okText={
        type === 'upload'
          ? uploadFailures.length
            ? '重试未完成项'
            : '开始上传'
          : type === 'download'
            ? '下载'
            : '克隆'
      }
      okButtonProps={{
        disabled: type === 'upload' && (!queue.length || reading),
      }}
      cancelText="取消"
      onOk={async () => {
        if (type === 'upload') {
          setSaving(true);
          try {
            const result = await onUpload(queue, overwrite);
            if (result.successCount)
              void message.success(`已上传 ${result.successCount} 项`);
            const failures = result.results.filter((row) => row.code !== 0);
            if (!failures.length) {
              onClose();
            } else {
              const failedPaths = new Set(failures.map((row) => row.target));
              setQueue((previous) =>
                previous.filter((entry) => failedPaths.has(entry.path)),
              );
              setUploadFailures(failures);
            }
          } finally {
            setSaving(false);
          }
          return;
        }
        const values = await form.validateFields();
        setSaving(true);
        try {
          const result = await fileTask('/remote', {
            type,
            dir: path,
            name: values.name,
            url: values.url,
            branch: values.branch || '',
          });
          onClose();
          onStarted(result.taskId);
        } finally {
          setSaving(false);
        }
      }}
    >
      {type === 'upload' ? (
        <div
          onDragOver={(event) => event.preventDefault()}
          onDrop={(event) => {
            event.preventDefault();
            event.stopPropagation();
            if (saving || reading) return;
            setReadError(false);
            setReading(true);
            void droppedEntries(event.dataTransfer)
              .then(add, () => setReadError(true))
              .finally(() => setReading(false));
          }}
        >
          <Typography.Paragraph style={{ overflowWrap: 'anywhere' }}>
            上传到 <Typography.Text code>{path}</Typography.Text>
          </Typography.Paragraph>
          <input
            ref={filesRef}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              add(pickedEntries(event.currentTarget.files));
              event.currentTarget.value = '';
            }}
          />
          <input
            ref={directoriesRef}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              add(pickedEntries(event.currentTarget.files));
              event.currentTarget.value = '';
            }}
          />
          <Space wrap>
            <Button
              type="primary"
              icon={<FileOutlined />}
              disabled={saving || reading}
              onClick={() => filesRef.current?.click()}
            >
              选择文件
            </Button>
            <Button
              icon={<FolderOutlined />}
              disabled={saving || reading}
              onClick={() => {
                directoriesRef.current?.setAttribute('webkitdirectory', '');
                directoriesRef.current?.click();
              }}
            >
              选择文件夹
            </Button>
          </Space>
          <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>
            可拖入多个文件和文件夹。{resumeUploadThreshold / 1024 ** 2} MiB
            及以上文件上传中断后，可在同一目录重新选择文件并沿用覆盖选项继续上传。
          </Typography.Paragraph>
          {saving && <Progress percent={uploadProgress} size="small" />}
          {readError && (
            <Alert
              type="error"
              message="有项目无法读取，请重新选择后再上传。"
            />
          )}
          {!!uploadFailures.length && (
            <Alert
              type="warning"
              showIcon
              message={`${uploadFailures.length} 项上传未完成`}
              description={
                <ul className="file-upload-failures">
                  {uploadFailures.map(({ target, msg }) => (
                    <li key={target}>
                      <strong>{target}</strong> · {msg}
                    </li>
                  ))}
                </ul>
              }
            />
          )}
          {queue.length > 0 && (
            <>
              <Space wrap>
                <Typography.Text strong>已选 {queue.length} 项</Typography.Text>
                <Button
                  type="link"
                  disabled={saving}
                  onClick={() => {
                    setQueue([]);
                    setUploadFailures([]);
                  }}
                >
                  清空队列
                </Button>
              </Space>
              <ul
                style={{
                  maxHeight: 176,
                  overflow: 'auto',
                  paddingInlineStart: 22,
                  overflowWrap: 'anywhere',
                }}
              >
                {queue.map((item) => (
                  <li key={item.path}>
                    {item.path}
                    {item.file ? '' : '/'}
                  </li>
                ))}
              </ul>
              <Checkbox
                checked={overwrite}
                disabled={saving}
                onChange={(event) => setOverwrite(event.target.checked)}
              >
                覆盖同名普通文件
              </Checkbox>
            </>
          )}
        </div>
      ) : (
        <Form
          form={form}
          layout="vertical"
          initialValues={{ name: '', branch: '' }}
        >
          <Form.Item
            name="url"
            label={type === 'download' ? '下载链接' : '仓库地址'}
            rules={[
              { required: true, whitespace: true, message: '请输入链接地址' },
            ]}
          >
            <Input
              placeholder={
                type === 'download'
                  ? 'https://example.com/file.zip'
                  : 'https://github.com/user/repo.git 或 git@host:user/repo.git'
              }
              autoComplete="off"
            />
          </Form.Item>
          <Form.Item
            name="name"
            label={type === 'download' ? '保存文件名' : '保存目录名'}
          >
            <Input placeholder="留空则根据链接自动命名" />
          </Form.Item>
          {type === 'git' && (
            <Form.Item name="branch" label="分支">
              <Input placeholder="留空使用默认分支" />
            </Form.Item>
          )}
          <Form.Item label="保存位置">
            <Input value={path} readOnly />
          </Form.Item>
        </Form>
      )}
    </Modal>
  );
}
