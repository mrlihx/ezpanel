import { useState } from 'react';
import {
  Button,
  Card,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  Typography,
} from 'antd';
import { Plus } from 'lucide-react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { saveToolbox, type SavedCommand } from '../../api/toolbox';
import ContentSizedTable from '../../components/ContentSizedTable';
import { LAYOUT } from '../../styles/tokens';

export default function Commands({
  commands,
  loading,
}: {
  commands: SavedCommand[];
  loading: boolean;
}) {
  const client = useQueryClient();
  const [editing, setEditing] = useState<number>();
  const [form] = Form.useForm<SavedCommand>();
  const save = useMutation({
    mutationFn: (list: SavedCommand[]) => saveToolbox('commands', { list }),
    onSuccess: async (_, list) => {
      client.setQueryData(['toolbox', 'commands'], { list });
      setEditing(undefined);
      await client.invalidateQueries({ queryKey: ['toolbox', 'commands'] });
    },
  });
  const startEditing = (index: number) => {
    form.resetFields();
    form.setFieldsValue(commands[index] ?? { name: '', command: '' });
    setEditing(index);
  };
  return (
    <Card>
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Button
          type="primary"
          icon={<Plus size={16} />}
          disabled={loading || save.isPending || commands.length >= 100}
          onClick={() => startEditing(commands.length)}
        >
          添加命令
        </Button>
        <ContentSizedTable
          rowKey="index"
          fillColumn="command"
          dataSource={commands.map((command, index) => ({ ...command, index }))}
          loading={loading}
          pagination={false}
          locale={{ emptyText: '暂无常用命令' }}
          columns={[
            { title: '名称', dataIndex: 'name' },
            {
              title: '命令',
              dataIndex: 'command',
              render: (command: string) => (
                <Typography.Text
                  code
                  copyable
                  style={{ whiteSpace: 'pre-wrap' }}
                >
                  {command}
                </Typography.Text>
              ),
            },
            {
              title: '操作',
              render: (_, row) => (
                <Space>
                  <Button
                    type="link"
                    disabled={save.isPending}
                    onClick={() => startEditing(row.index)}
                  >
                    编辑
                  </Button>
                  <Popconfirm
                    title="删除常用命令？"
                    description={row.name}
                    okText="删除"
                    cancelText="取消"
                    onConfirm={() =>
                      save.mutateAsync(
                        commands.filter((_, index) => index !== row.index),
                      )
                    }
                  >
                    <Button type="link" danger disabled={save.isPending}>
                      删除
                    </Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Space>
      <Modal
        open={editing !== undefined}
        title={editing === commands.length ? '添加命令' : '编辑命令'}
        confirmLoading={save.isPending}
        cancelButtonProps={{ disabled: save.isPending }}
        closable={!save.isPending}
        maskClosable={false}
        okText="保存"
        cancelText="取消"
        onOk={() => form.submit()}
        onCancel={() => {
          if (!save.isPending) setEditing(undefined);
        }}
      >
        <Form
          form={form}
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
          disabled={save.isPending}
          onFinish={(value) => {
            if (editing === undefined) return;
            const list = [...commands];
            list[editing] = value;
            save.mutate(list);
          }}
        >
          <Form.Item
            label="名称"
            name="name"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item
            label="命令"
            name="command"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input.TextArea rows={4} maxLength={4096} spellCheck={false} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
}
