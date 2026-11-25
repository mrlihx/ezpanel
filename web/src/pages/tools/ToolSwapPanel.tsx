import { Button, Descriptions, Form, InputNumber, Typography } from 'antd';
import { formatSize } from '../../utils/format';
import type { ToolboxPageState } from './index';

export default function ToolSwapPanel({
  busy,
  confirm,
  formProps,
  save,
  swap,
}: Pick<ToolboxPageState, 'busy' | 'confirm' | 'formProps' | 'save' | 'swap'>) {
  return (
    <>
      {swap.data && (
        <>
          <Descriptions
            items={[
              {
                key: 'total',
                label: '总容量',
                children: formatSize(swap.data.total),
              },
              {
                key: 'used',
                label: '已用',
                children: formatSize(swap.data.used),
              },
              {
                key: 'free',
                label: '可用',
                children: formatSize(swap.data.free),
              },
            ]}
          />
          <Form
            {...formProps}
            initialValues={{ sizeMB: swap.data.ownMB }}
            key={swap.dataUpdatedAt}
            onFinish={(v) =>
              confirm(
                '调整面板 Swap？',
                '仅调整面板创建的 Swap。设为0会停用并删除该文件，系统原有 Swap 保留。',
                () => save('swap', v),
              )
            }
          >
            <Form.Item
              label="Swap 容量"
              name="sizeMB"
              rules={[{ required: true, message: '请输入 Swap 容量' }]}
            >
              <InputNumber min={0} max={16384} precision={0} addonAfter="MB" />
            </Form.Item>
            <Form.Item label=" ">
              <Button htmlType="submit" type="primary" loading={busy}>
                确定
              </Button>
            </Form.Item>
          </Form>
          <Typography.Text type="secondary">
            保存路径：{swap.data.path}
            。需要足够磁盘空间，停用时需要足够可用内存。
          </Typography.Text>
          <Typography.Paragraph>
            <pre style={{ overflowX: 'auto' }}>{swap.data.entries}</pre>
          </Typography.Paragraph>
        </>
      )}
    </>
  );
}
