import { Alert, Button, Form, Select, Space, Tag, Typography } from 'antd';
import { request } from '../../api/client';
import { type TimeSyncResult } from '../../api/toolbox';
import type { ToolboxPageState } from './index';

export default function ToolTimePanel({
  busy,
  formProps,
  message,
  save,
  setBusy,
  time,
}: Pick<
  ToolboxPageState,
  'busy' | 'formProps' | 'message' | 'save' | 'setBusy' | 'time'
>) {
  return (
    <>
      {time.data && (
        <>
          <Space wrap>
            <Typography.Text>
              服务器时间：
              {new Date(time.data.now).toLocaleString('zh-CN', {
                timeZone: time.data.zone,
              })}{' '}
              ({time.data.zone})
            </Typography.Text>
            <Tag color={time.data.synchronized ? 'success' : 'default'}>
              {time.data.synchronized ? '已同步' : '尚未同步'}
            </Tag>
            <Typography.Text type="secondary">
              {time.data.service
                ? `${time.data.service}（${time.data.serviceActive ? '运行中' : '未启动'}）`
                : '未检测到 NTP 服务'}
            </Typography.Text>
            <Button onClick={() => void time.refetch()}>刷新</Button>
            <Button
              loading={busy}
              onClick={async () => {
                setBusy(true);
                try {
                  const r = await request<TimeSyncResult>(
                    '/api/toolbox/time/sync',
                    { method: 'POST' },
                  );
                  if (r.synchronized) message.success(r.message);
                  else message.warning(r.message);
                  await time.refetch();
                } finally {
                  setBusy(false);
                }
              }}
            >
              同步时间
            </Button>
          </Space>
          <Form
            {...formProps}
            initialValues={{ zone: time.data.zone }}
            key={time.dataUpdatedAt}
            onFinish={(v) => save('time', v)}
          >
            <Form.Item
              name="zone"
              label="设置时区"
              rules={[{ required: true }]}
            >
              <Select
                showSearch
                options={time.data.zones.map((value) => ({
                  value,
                }))}
              />
            </Form.Item>
            <Form.Item label=" ">
              <Button type="primary" htmlType="submit" loading={busy}>
                确定
              </Button>
            </Form.Item>
          </Form>
          <Alert
            type="info"
            message="北京时间请选择 Asia/Shanghai。同步时间使用当前 NTP 服务，时钟偏差较大时可能跳时；已有计划任务仍遵循面板配置的调度时区。"
          />
        </>
      )}
    </>
  );
}
