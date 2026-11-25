import { useState } from 'react';
import { Alert, Button, Modal, Skeleton } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { downloadFile, filePreviewUrl, type FileEntry } from '../../api/file';

export default function FilePreview({
  entry,
  onClose,
}: {
  entry: FileEntry;
  onClose: () => void;
}) {
  const url = filePreviewUrl(entry.path);
  const [mediaError, setMediaError] = useState(false);
  const preview = useQuery({
    queryKey: ['file-preview', entry.path, entry.modTime],
    queryFn: async ({ signal }) => {
      const response = await fetch(url, { method: 'HEAD', signal });
      if (!response.ok) throw new Error('无法预览此文件，请下载查看');
      return response.headers.get('Content-Type') ?? '';
    },
    retry: false,
    refetchOnWindowFocus: false,
  });
  const kind = (preview.data ?? '').split(';')[0].trim().toLowerCase();
  return (
    <Modal
      open
      title={entry.name}
      width={850}
      onCancel={onClose}
      footer={[
        ...(kind === 'application/pdf'
          ? [
              <Button key="open" href={url} target="_blank" rel="noopener">
                在新标签页打开
              </Button>,
            ]
          : []),
        <Button key="download" onClick={() => void downloadFile(entry.path)}>
          下载
        </Button>,
        <Button key="close" type="primary" onClick={onClose}>
          关闭
        </Button>,
      ]}
    >
      <div className="file-preview-content">
        {preview.isPending ? (
          <Skeleton active />
        ) : preview.error || mediaError ? (
          <Alert
            type="warning"
            showIcon
            message={preview.error?.message ?? '当前浏览器无法播放此文件'}
          />
        ) : kind.startsWith('image/') ? (
          <img src={url} alt={entry.name} onError={() => setMediaError(true)} />
        ) : kind.startsWith('audio/') ? (
          <audio
            src={url}
            controls
            preload="metadata"
            onError={() => setMediaError(true)}
          />
        ) : kind.startsWith('video/') ? (
          <video
            src={url}
            controls
            playsInline
            preload="metadata"
            onError={() => setMediaError(true)}
          />
        ) : kind === 'application/pdf' ? (
          <iframe src={url} title={entry.name} />
        ) : (
          <Alert
            type="info"
            showIcon
            message="暂不支持在线预览此文件类型，请点击下方下载查看。"
          />
        )}
      </div>
    </Modal>
  );
}
