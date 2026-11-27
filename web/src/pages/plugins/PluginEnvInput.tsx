import { lazy, Suspense, useState } from 'react';
import { Button, Input, Select, Space } from 'antd';
import { FolderOpenOutlined } from '@ant-design/icons';
import type { PluginEnv } from '../../api/plugin';

const FilePicker = lazy(() => import('../file/FilePicker'));
const DirectoryPicker = lazy(() => import('../file/DirectoryPicker'));

export default function PluginEnvInput({
  field,
  value,
  onChange,
  id,
}: {
  field: PluginEnv;
  value?: string;
  onChange?: (value: string) => void;
  id?: string;
}) {
  const [picking, setPicking] = useState(false);
  if (field.secret) {
    return (
      <Input.Password
        id={id}
        style={{ width: '100%' }}
        value={value}
        onChange={(event) => onChange?.(event.target.value)}
        autoComplete="new-password"
      />
    );
  }
  if (field.type === 'select' || field.type === 'boolean') {
    const options =
      field.type === 'boolean'
        ? [
            { value: 'true', label: '是' },
            { value: 'false', label: '否' },
          ]
        : field.options?.map((option) => ({ value: option, label: option }));
    return (
      <Select
        id={id}
        style={{ width: '100%' }}
        value={value || undefined}
        onChange={(next) => onChange?.(next ?? '')}
        allowClear={!field.required}
        options={options}
      />
    );
  }
  const directory = field.type === 'directory';
  const choosePath = directory || field.type === 'file';
  const initialPath = value
    ? directory
      ? value
      : value.slice(0, value.lastIndexOf('/')) || '/'
    : '/';
  const select = (path: string) => {
    onChange?.(path);
    setPicking(false);
  };
  return (
    <>
      {choosePath ? (
        <Space.Compact style={{ width: '100%' }}>
          <Input
            id={id}
            style={{ width: '100%' }}
            value={value}
            onChange={(event) => onChange?.(event.target.value)}
            type="text"
          />
          <Button
            aria-label={directory ? '选择目录' : '选择文件'}
            icon={<FolderOpenOutlined />}
            onClick={() => setPicking(true)}
          />
        </Space.Compact>
      ) : (
        <Input
          id={id}
          style={{ width: '100%' }}
          value={value}
          onChange={(event) => onChange?.(event.target.value)}
          type={field.type === 'number' ? 'number' : 'text'}
        />
      )}
      {picking && (
        <Suspense fallback={null}>
          {directory ? (
            <DirectoryPicker
              initialPath={initialPath}
              onClose={() => setPicking(false)}
              onSelect={select}
            />
          ) : (
            <FilePicker
              initialPath={initialPath}
              onClose={() => setPicking(false)}
              onSelect={select}
            />
          )}
        </Suspense>
      )}
    </>
  );
}
