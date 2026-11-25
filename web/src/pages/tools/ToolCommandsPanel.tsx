import { Button, Form, Input, Modal, Space, Typography } from 'antd';
import { Plus } from 'lucide-react';
import ContentSizedTable from '../../components/ContentSizedTable';
import { type SavedCommand } from '../../api/toolbox';
import type { ToolboxPageState } from './index';

export default function ToolCommandsPanel({
  busy,
  commands,
  confirm,
  editingCommand,
  save,
  setEditingCommand,
  setTerminal,
}: Pick<
  ToolboxPageState,
  | 'busy'
  | 'commands'
  | 'confirm'
  | 'editingCommand'
  | 'save'
  | 'setEditingCommand'
  | 'setTerminal'
>) {
  return (
    <>
      {commands.data && (
        <>
          <Button
            type="primary"
            icon={<Plus size={16} strokeWidth={2} />}
            disabled={busy || commands.data.list.length >= 100}
            onClick={() => setEditingCommand('new')}
          >
            添加命令
          </Button>
          <ContentSizedTable<SavedCommand & { index: number }>
            fillColumn="command"
            rowKey="index"
            dataSource={commands.data.list.map((row, index) => ({
              ...row,
              index,
            }))}
            pagination={false}
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
                  <Space size={12}>
                    <Button
                      type="link"
                      onClick={() =>
                        confirm('在终端执行命令？', row.command, async () => {
                          setTerminal(row.command);
                        })
                      }
                    >
                      执行
                    </Button>
                    <Button
                      type="link"
                      disabled={busy}
                      onClick={() => setEditingCommand(row.index)}
                    >
                      编辑
                    </Button>
                    <Button
                      type="link"
                      danger
                      disabled={busy}
                      onClick={() =>
                        confirm('删除常用命令？', row.name, () =>
                          save('commands', {
                            list: commands.data!.list.filter(
                              (_, index) => index !== row.index,
                            ),
                          }),
                        )
                      }
                    >
                      删除
                    </Button>
                  </Space>
                ),
              },
            ]}
          />
          <Modal
            open={editingCommand !== undefined}
            title={editingCommand === 'new' ? '添加命令' : '编辑命令'}
            destroyOnHidden
            maskClosable={false}
            confirmLoading={busy}
            closable={!busy}
            cancelButtonProps={{ disabled: busy }}
            onCancel={() => !busy && setEditingCommand(undefined)}
            okText="保存"
            cancelText="取消"
            okButtonProps={{
              htmlType: 'submit',
              form: 'saved-command-form',
            }}
          >
            <Form<SavedCommand>
              id="saved-command-form"
              layout="vertical"
              preserve={false}
              initialValues={
                typeof editingCommand === 'number'
                  ? commands.data.list[editingCommand]
                  : { name: '', command: '' }
              }
              onFinish={async (values) => {
                const list = [...commands.data!.list];
                const index =
                  typeof editingCommand === 'number'
                    ? editingCommand
                    : list.length;
                list[index] = {
                  name: values.name.trim(),
                  command: values.command,
                };
                await save('commands', { list });
                setEditingCommand(undefined);
              }}
            >
              <Form.Item
                name="name"
                label="名称"
                rules={[{ required: true, whitespace: true, max: 64 }]}
              >
                <Input maxLength={64} autoFocus />
              </Form.Item>
              <Form.Item
                name="command"
                label="命令"
                rules={[{ required: true, whitespace: true, max: 4096 }]}
              >
                <Input.TextArea
                  autoSize={{ minRows: 4, maxRows: 12 }}
                  maxLength={4096}
                />
              </Form.Item>
            </Form>
          </Modal>
        </>
      )}
    </>
  );
}
