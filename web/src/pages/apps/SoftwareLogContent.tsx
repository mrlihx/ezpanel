import { useEffect, useState } from 'react';
import { Alert, App, Button, Space, Typography } from 'antd';
import { request } from '../../api/client';
import { openEventStream } from '../../api/eventStream';
import type { TextLog } from '../../api/oplog';
import LogText from '../../components/LogText';
import './software-log.css';

export default function SoftwareLogContent({
  url,
  minHeight = 280,
}: {
  url: string;
  minHeight?: number;
}) {
  const { modal, message } = App.useApp();
  const [text, setText] = useState<TextLog>();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(
    () =>
      openEventStream(
        `${url}&stream=true`,
        {
          logs(data) {
            setText(data as TextLog);
            setError('');
          },
        },
        (message) => setError(message),
      ),
    [url, attempt],
  );
  return (
    <div className="software-log-content">
      <div className="app-settings-toolbar">
        <Space wrap>
          <Button onClick={() => setAttempt((value) => value + 1)}>
            刷新日志
          </Button>
          <Button
            disabled={!text?.clearable}
            onClick={() =>
              modal.confirm({
                title: '清空当前日志？',
                okButtonProps: { danger: true },
                onOk: async () => {
                  await request(url, { method: 'DELETE' });
                  message.success('日志已清空');
                  setAttempt((value) => value + 1);
                },
              })
            }
          >
            清理日志
          </Button>
        </Space>
        {text?.path && (
          <Button
            href={`/files?path=${encodeURIComponent(text.path.slice(0, text.path.lastIndexOf('/')) || '/')}`}
            target="_blank"
          >
            打开日志目录
          </Button>
        )}
      </div>
      {text?.path && (
        <Typography.Text code copyable>
          {text.path}
        </Typography.Text>
      )}
      {error && <Alert type="error" message={error} />}
      {text?.truncated && (
        <Typography.Text type="secondary">显示最近 1 MiB 日志</Typography.Text>
      )}
      <LogText style={{ height: '100%', minHeight }}>
        {text?.content || '当前暂无日志'}
      </LogText>
    </div>
  );
}
