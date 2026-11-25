import { useState } from 'react';
import { Alert, Form, Input, Modal, Radio, Typography } from 'antd';
import {
  fileTask,
  previewTransfer,
  type TransferConflict,
  type TransferPlan,
} from '../../api/file';
import { formatSize, formatTime } from '../../utils/format';
import { LAYOUT } from '../../styles/tokens';

export type PasteConflict = TransferPlan & {
  type: 'copy' | 'move';
  conflicts: TransferConflict[];
};
export default function TransferConflictModal({
  plan,
  onClose,
  onStarted,
}: {
  plan: PasteConflict;
  onClose: () => void;
  onStarted: (taskId: string) => void;
}) {
  const [conflicts, setConflicts] = useState(plan.conflicts);
  const [index, setIndex] = useState(0);
  const [decisions, setDecisions] = useState({
    names: plan.names ?? {},
    overwrites: plan.overwrites ?? {},
  });
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm<{
    action: 'overwrite' | 'rename';
    name: string;
  }>();
  const action = Form.useWatch('action', form);
  const current = conflicts[index];
  return (
    <Modal
      open
      title={
        conflicts.length > 1
          ? `重复文件提示 · ${index + 1}/${conflicts.length}`
          : '重复文件提示'
      }
      width={LAYOUT.shellWidth}
      maskClosable={false}
      onCancel={onClose}
      confirmLoading={busy}
      okText="确认"
      onOk={async () => {
        const values = await form.validateFields();
        const next = {
          names: {
            ...decisions.names,
            [current.source]:
              values.action === 'rename' ? values.name : current.entry.name,
          },
          overwrites: {
            ...decisions.overwrites,
            [current.source]: values.action === 'overwrite',
          },
        };
        setDecisions(next);
        if (index + 1 < conflicts.length) {
          setIndex(index + 1);
          form.setFieldsValue({
            action: 'rename',
            name: conflicts[index + 1].suggestedName,
          });
          return;
        }
        setBusy(true);
        try {
          const transfer = { paths: plan.paths, dest: plan.dest, ...next };
          const preview = await previewTransfer(transfer);
          if (preview.conflicts.length) {
            setConflicts(preview.conflicts);
            setIndex(0);
            form.setFieldsValue({
              action: 'rename',
              name: preview.conflicts[0].suggestedName,
            });
            return;
          }
          const result = await fileTask(`/${plan.type}`, transfer);
          onStarted(result.taskId);
          onClose();
        } finally {
          setBusy(false);
        }
      }}
    >
      <Typography.Paragraph type="secondary">
        目标目录中已有「{current.entry.name}」，请选择处理方式。
      </Typography.Paragraph>
      <Form
        form={form}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1 }}
        colon={false}
        initialValues={{ action: 'rename', name: current.suggestedName }}
      >
        <Form.Item name="action" label="操作类型">
          <Radio.Group
            onChange={(event) =>
              form.setFieldValue(
                'name',
                event.target.value === 'overwrite'
                  ? current.entry.name
                  : current.suggestedName,
              )
            }
            options={[
              {
                label: '覆盖文件',
                value: 'overwrite',
                disabled: !current.canOverwrite,
              },
              { label: '重命名文件', value: 'rename' },
            ]}
          />
        </Form.Item>
        <Form.Item
          name="name"
          label="文件名"
          rules={
            action === 'overwrite'
              ? []
              : [
                  { required: true, message: '请输入文件名' },
                  {
                    validator: (_, value: string) =>
                      !value ||
                      ['.', '..'].includes(value) ||
                      value.includes('/') ||
                      value.includes('\\') ||
                      value.includes(String.fromCharCode(0))
                        ? Promise.reject(new Error('请输入单个有效文件名'))
                        : Promise.resolve(),
                  },
                ]
          }
        >
          <Input disabled={action === 'overwrite'} />
        </Form.Item>
        <Form.Item label="大小">
          <Typography.Text>
            {current.entry.size === null
              ? '文件夹'
              : formatSize(current.entry.size)}
          </Typography.Text>
        </Form.Item>
        <Form.Item label="最后修改时间">
          <Typography.Text>{formatTime(current.entry.modTime)}</Typography.Text>
        </Form.Item>
      </Form>
      {!current.canOverwrite && (
        <Alert
          type="info"
          message="文件夹、链接和源文件自身不直接覆盖，请重命名后粘贴。"
        />
      )}
      {action === 'overwrite' && (
        <Alert
          type="warning"
          message="确认后将覆盖目标文件，原文件内容不可恢复。"
        />
      )}
    </Modal>
  );
}
