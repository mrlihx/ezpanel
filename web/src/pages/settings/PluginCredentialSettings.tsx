import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { App, Button, Flex, Form, Input, Modal, Popconfirm, Space } from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import {
  deletePluginCredential,
  pluginCredentials,
  savePluginCredential,
  type PluginCredential,
} from '../../api/plugin';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';

export default function PluginCredentialSettings() {
  const client = useQueryClient();
  const { message } = App.useApp();
  const [editing, setEditing] = useState<PluginCredential | null>();
  const [form] = Form.useForm<{ name: string; value?: string }>();
  const rows = useQuery({
    queryKey: ['plugin-credentials'],
    queryFn: pluginCredentials,
  });
  const refresh = () =>
    client.invalidateQueries({ queryKey: ['plugin-credentials'] });
  const save = useMutation({
    mutationFn: (data: { name: string; value?: string }) =>
      savePluginCredential(editing?.id, data),
    onSuccess: () => {
      setEditing(undefined);
      form.resetFields();
      void refresh();
      void message.success('凭据已保存');
    },
  });
  function open(row: PluginCredential | null) {
    form.resetFields();
    form.setFieldsValue({ name: row?.name, value: '' });
    setEditing(row);
  }
  return (
    <Flex vertical gap={SPACING.md}>
      <Flex justify="flex-start">
        <Button type="primary" onClick={() => open(null)}>
          添加凭据
        </Button>
      </Flex>
      <ContentSizedTable<PluginCredential>
        fillColumn="name"
        rowKey="id"
        loading={rows.isFetching}
        dataSource={rows.data}
        columns={[
          { title: '名称', dataIndex: 'name' },
          { title: '更新时间', dataIndex: 'updatedAt', render: formatTime },
          {
            title: '操作',
            key: 'action',
            render: (_, row) => (
              <Space>
                <Button type="link" onClick={() => open(row)}>
                  修改
                </Button>
                <Popconfirm
                  title="删除这项凭据？"
                  onConfirm={async () => {
                    await deletePluginCredential(row.id);
                    await refresh();
                  }}
                >
                  <Button type="link" danger>
                    删除
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title={editing ? '修改凭据' : '添加凭据'}
        open={editing !== undefined}
        onCancel={() => {
          setEditing(undefined);
          form.resetFields();
        }}
        okText="保存"
        onOk={() => form.submit()}
        confirmLoading={save.isPending}
      >
        <Form
          form={form}
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          colon={false}
          onFinish={(values) => save.mutate(values)}
        >
          <Form.Item
            name="name"
            label="凭据名称"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item
            name="value"
            label="凭据内容"
            rules={[{ required: editing === null }]}
          >
            <Input.Password
              autoComplete="new-password"
              maxLength={8192}
              placeholder={editing ? '留空保留现有凭据' : undefined}
            />
          </Form.Item>
        </Form>
      </Modal>
    </Flex>
  );
}
