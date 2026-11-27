import { useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Form,
  Input,
  Modal,
  Select,
} from 'antd';
import { FolderOpenOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { files } from '../../api/file';
import { LAYOUT } from '../../styles/tokens';
import DirectoryPicker from './DirectoryPicker';

export default function ExtractModal({
  path,
  destination,
  onClose,
  onStarted,
}: {
  path: string;
  destination: string;
  onClose: () => void;
  onStarted: (id: string) => void;
}) {
  const [form] = Form.useForm();
  const [picking, setPicking] = useState(false),
    [busy, setBusy] = useState(false);
  const supportsPassword = /\.(zip|7z|rar)$/i.test(path);
  const accounts = useQuery({
    queryKey: ['file-identities'],
    queryFn: () => files<{ users: { name: string }[] }>('/identities'),
  });
  return (
    <>
      <Modal
        open
        title={`解压文件 [ ${path.split('/').pop()} ]`}
        width={LAYOUT.shellWidth}
        maskClosable={false}
        onCancel={onClose}
        confirmLoading={busy}
        okText="解压"
        onOk={async () => {
          const values = await form.validateFields();
          setBusy(true);
          try {
            const result = await files<{ taskId: string }>('/extract', {
              method: 'POST',
              data: { path, ...values },
            });
            onStarted(result.taskId);
            onClose();
          } finally {
            setBusy(false);
          }
        }}
      >
        <Form
          form={form}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
          initialValues={{
            dest: destination,
            smartFolder: false,
            password: '',
            encoding: 'UTF-8',
            dirMode: '755',
            owner: 'root',
            overwrite: false,
          }}
        >
          <Form.Item label="文件名">
            <Input value={path.split('/').pop()} readOnly />
          </Form.Item>
          <Form.Item name="dest" label="解压到" rules={[{ required: true }]}>
            <Input
              addonAfter={
                <Button
                  type="text"
                  size="small"
                  icon={<FolderOpenOutlined />}
                  aria-label="选择解压目录"
                  onClick={() => setPicking(true)}
                />
              }
            />
          </Form.Item>
          <Form.Item
            name="smartFolder"
            label=" "
            valuePropName="checked"
            extra="仅含单一顶层文件夹时直接解压；有散碎文件或多个文件夹时自动新建文件夹。"
          >
            <Checkbox>智能新建文件夹</Checkbox>
          </Form.Item>
          <Form.Item
            name="password"
            label="解压密码"
            extra={
              !supportsPassword ? '当前格式不支持解压密码，请留空。' : undefined
            }
          >
            <Input.Password
              autoComplete="new-password"
              disabled={!supportsPassword}
              placeholder={
                supportsPassword ? '无密码则留空' : '当前格式不支持解压密码'
              }
            />
          </Form.Item>
          <Form.Item name="encoding" label="编码">
            <Select
              options={['UTF-8', 'GB18030', 'GBK'].map((value) => ({
                value,
                label: value,
              }))}
            />
          </Form.Item>
          <Form.Item name="overwrite" label=" " valuePropName="checked">
            <Checkbox>覆盖同名普通文件</Checkbox>
          </Form.Item>
          <Collapse
            ghost
            items={[
              {
                key: 'advanced',
                label: '高级选项',
                children: (
                  <>
                    <Form.Item
                      name="dirMode"
                      label="解压目录权限"
                      rules={[
                        {
                          required: true,
                          pattern: /^[0-7]{3}$/,
                          message: '请输入三位八进制权限',
                        },
                      ]}
                    >
                      <Input maxLength={3} />
                    </Form.Item>
                    <Form.Item
                      name="owner"
                      label="所有者"
                      rules={[{ required: true }]}
                    >
                      <Select
                        showSearch
                        loading={accounts.isFetching}
                        options={accounts.data?.users.map((user) => ({
                          value: user.name,
                          label: user.name,
                        }))}
                      />
                    </Form.Item>
                  </>
                ),
              },
            ]}
          />
          {accounts.error && (
            <Alert type="error" message={accounts.error.message} />
          )}
          <Alert
            type="info"
            message="默认保留同名文件。启用覆盖后，已覆盖的文件不会因后续条目解压失败而恢复。"
          />
        </Form>
      </Modal>
      {picking && (
        <DirectoryPicker
          initialPath={form.getFieldValue('dest') || destination}
          onClose={() => setPicking(false)}
          onSelect={(value) => {
            form.setFieldValue('dest', value);
            setPicking(false);
          }}
        />
      )}
    </>
  );
}
