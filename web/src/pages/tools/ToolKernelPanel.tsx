import { Button, Form, InputNumber, Col, Row, Space, Typography } from 'antd';
import { SPACING } from '../../styles/tokens';
import type { ToolboxPageState } from './index';

export default function ToolKernelPanel({
  busy,
  confirm,
  kernel,
  message,
  save,
}: Pick<ToolboxPageState, 'busy' | 'confirm' | 'kernel' | 'message' | 'save'>) {
  return (
    <>
      {kernel.data && (
        <Form
          layout="vertical"
          labelWrap
          initialValues={{
            values: Object.fromEntries(
              kernel.data.list
                .filter((p) => p.available)
                .map((p) => [p.key, Number(p.value)]),
            ),
          }}
          key={kernel.dataUpdatedAt}
          onFinish={(v) => {
            const values = Object.fromEntries(
              kernel
                .data!.list.filter(
                  (p) => p.available && v.values?.[p.key] !== Number(p.value),
                )
                .map((p) => [p.key, v.values[p.key]]),
            );
            if (!Object.keys(values).length) {
              message.info('没有需要保存的修改');
              return;
            }
            confirm(
              '应用内核参数？',
              '参数立即生效并写入独立 sysctl 配置。请确认这些值适用于当前业务。',
              () => save('kernel', { values }),
            );
          }}
        >
          <Row gutter={[SPACING.lg, SPACING.sm]}>
            {kernel.data.list.map((p) => (
              <Col key={p.key} xs={24} sm={12} xl={8}>
                <Form.Item
                  name={p.available ? ['values', p.key] : undefined}
                  label={
                    <Space direction="vertical" size={2}>
                      {p.label}
                      <Typography.Text
                        code
                        type="secondary"
                        style={{ overflowWrap: 'anywhere' }}
                      >
                        {p.key}
                      </Typography.Text>
                    </Space>
                  }
                  extra={
                    p.available
                      ? `取值范围 ${p.min}–${p.max}`
                      : '当前系统不支持此参数'
                  }
                  rules={[
                    {
                      type: 'integer',
                      min: p.min,
                      max: p.max,
                      required: true,
                    },
                  ]}
                >
                  <InputNumber
                    min={p.min}
                    max={p.max}
                    precision={0}
                    disabled={!p.available}
                    style={{ width: '100%' }}
                  />
                </Form.Item>
              </Col>
            ))}
          </Row>
          <Form.Item>
            <Button
              type="primary"
              htmlType="submit"
              loading={busy}
              disabled={!kernel.data.list.some((p) => p.available)}
            >
              保存并应用
            </Button>
          </Form.Item>
        </Form>
      )}
    </>
  );
}
