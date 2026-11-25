import { useState } from 'react';
import {
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Tag,
  Tooltip,
} from 'antd';
import { FolderOpen } from 'lucide-react';
import { useMutation } from '@tanstack/react-query';
import { saveToolbox, type DiskDevice } from '../../api/toolbox';
import ContentSizedTable from '../../components/ContentSizedTable';
import { formatSize } from '../../utils/format';
import DirectoryPicker from '../file/DirectoryPicker';

export default function DiskMounts({
  list,
  onChanged,
}: {
  list: DiskDevice[];
  onChanged: () => Promise<unknown>;
}) {
  const { message } = App.useApp();
  const [selection, setSelection] = useState<{
    action: 'mount' | 'unmount';
    disk: DiskDevice;
  }>();
  const [directoryPicker, setDirectoryPicker] = useState(false);
  const [form] = Form.useForm<{ target: string }>();
  const change = useMutation({
    mutationFn: (data: {
      action: 'mount' | 'unmount';
      device: string;
      target: string;
    }) => saveToolbox('disks', data),
    onSuccess: async (_, data) => {
      setSelection(undefined);
      void message.success(
        data.action === 'mount' ? '磁盘已挂载' : '磁盘已卸载',
      );
      await onChanged();
    },
  });
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Button onClick={() => void onChanged()}>刷新</Button>
      <ContentSizedTable<DiskDevice>
        fillColumn={['model', 'target']}
        rowKey="path"
        dataSource={list}
        size="small"
        pagination={{ defaultPageSize: 10, showSizeChanger: true }}
        columns={[
          { title: '设备', dataIndex: 'path' },
          {
            title: '型号',
            dataIndex: 'model',
            render: (value) => value || '—',
          },
          { title: '类型', dataIndex: 'type' },
          { title: '容量', dataIndex: 'size', render: formatSize },
          {
            title: '文件系统',
            dataIndex: 'fstype',
            render: (value) => value || '—',
          },
          {
            title: '挂载目录',
            key: 'target',
            render: (_, row) =>
              row.mountpoints?.length
                ? row.mountpoints.map((path) => (
                    <div key={path} style={{ overflowWrap: 'anywhere' }}>
                      {path}
                    </div>
                  ))
                : '—',
          },
          {
            title: '状态',
            render: (_, row) => (
              <Space>
                {row.mountpoints?.length ? (
                  <Tag color="success">已挂载</Tag>
                ) : (
                  <Tag>未挂载</Tag>
                )}
                {row.system && <Tag color="blue">系统盘</Tag>}
              </Space>
            ),
          },
          {
            title: '操作',
            key: 'actions',
            render: (_, row) => {
              const mounted = !!row.mountpoints?.length;
              const action = mounted ? 'unmount' : 'mount';
              return (
                <Tooltip
                  title={
                    row.system
                      ? '系统盘受保护'
                      : !mounted && !row.mountable
                        ? '没有可挂载的文件系统'
                        : undefined
                  }
                >
                  <span>
                    <Button
                      type="link"
                      danger={mounted}
                      disabled={row.system || (!mounted && !row.mountable)}
                      onClick={() => {
                        setSelection({ action, disk: row });
                        form.setFieldsValue({
                          target: mounted
                            ? row.mountpoints?.[0]
                            : `/mnt/${row.path.split('/').at(-1)}`,
                        });
                      }}
                    >
                      {mounted ? '卸载' : '挂载'}
                    </Button>
                  </span>
                </Tooltip>
              );
            },
          },
        ]}
      />
      {selection && (
        <Modal
          title={`${selection.action === 'mount' ? '挂载' : '卸载'} ${selection.disk.path}`}
          open
          confirmLoading={change.isPending}
          onCancel={() => {
            if (!change.isPending) setSelection(undefined);
          }}
          onOk={() => form.submit()}
          okText={selection.action === 'mount' ? '挂载' : '确认卸载'}
          okButtonProps={{ danger: selection.action === 'unmount' }}
        >
          <Form
            form={form}
            layout="vertical"
            disabled={change.isPending}
            onFinish={({ target }) =>
              change.mutate({
                action: selection.action,
                device: selection.disk.path,
                target,
              })
            }
          >
            {selection.action === 'mount' ? (
              <Form.Item
                label="挂载目录"
                required
                extra="使用空目录。此次挂载在重启后失效。"
              >
                <Space.Compact style={{ width: '100%' }}>
                  <Form.Item
                    name="target"
                    noStyle
                    rules={[{ required: true, whitespace: true }]}
                  >
                    <Input placeholder="输入绝对路径" />
                  </Form.Item>
                  <Button
                    icon={<FolderOpen size={15} strokeWidth={1.9} />}
                    onClick={() => setDirectoryPicker(true)}
                  >
                    选择目录
                  </Button>
                </Space.Compact>
              </Form.Item>
            ) : (
              <Form.Item
                name="target"
                label="卸载目录"
                rules={[{ required: true }]}
              >
                <Select
                  options={selection.disk.mountpoints?.map((path) => ({
                    label: path,
                    value: path,
                  }))}
                />
              </Form.Item>
            )}
          </Form>
        </Modal>
      )}
      {directoryPicker && (
        <DirectoryPicker
          initialPath="/mnt"
          onClose={() => setDirectoryPicker(false)}
          onSelect={(target) => {
            form.setFieldValue('target', target);
            setDirectoryPicker(false);
          }}
        />
      )}
    </Space>
  );
}
