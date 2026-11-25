import { useState } from 'react';
import { Button, Form, Input, Space } from 'antd';
import { FolderOpenOutlined } from '@ant-design/icons';
import DirectoryPicker from '../file/DirectoryPicker';
import { useAuth } from '../../stores/auth';
export default function SiteDirectoryFields({
  showRoot,
  project = false,
}: {
  showRoot: boolean;
  project?: boolean;
}) {
  const [selecting, setSelecting] = useState(false);
  const form = Form.useFormInstance();
  const siteRoot = useAuth((state) => state.user?.siteRoot) || '/www/wwwroot';
  return (
    <>
      <Form.Item
        name="name"
        label={project ? '项目名称' : '站点名称'}
        rules={[{ required: true, whitespace: true, max: 255 }]}
      >
        <Input maxLength={255} />
      </Form.Item>
      <Form.Item name="remark" label="备注" rules={[{ max: 512 }]}>
        <Input maxLength={512} />
      </Form.Item>
      {showRoot && (
        <Form.Item label={project ? '项目目录' : '根目录'} required>
          <Space.Compact style={{ width: '100%' }}>
            <Form.Item name="root" noStyle rules={[{ required: true }]}>
              <Input />
            </Form.Item>
            <Button
              aria-label="选择目录"
              icon={<FolderOpenOutlined />}
              onClick={() => setSelecting(true)}
            />
          </Space.Compact>
        </Form.Item>
      )}
      {selecting && (
        <DirectoryPicker
          initialPath={siteRoot}
          onSelect={(root) => {
            form.setFieldValue('root', root);
            setSelecting(false);
          }}
          onClose={() => setSelecting(false)}
        />
      )}
    </>
  );
}
