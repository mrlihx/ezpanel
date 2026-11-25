import { useState } from 'react';
import { Button, Input, Space } from 'antd';
import { FolderOpen } from 'lucide-react';
import DirectoryPicker from '../file/DirectoryPicker';

export default function HostToolDirectory({
  value,
  onChange,
}: {
  value?: string;
  onChange?: (path: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Space.Compact block>
        <Input
          readOnly
          value={value}
          placeholder="选择目录"
          onClick={() => setOpen(true)}
        />
        <Button
          icon={<FolderOpen size={15} strokeWidth={1.9} />}
          aria-label="选择目录"
          onClick={() => setOpen(true)}
        />
      </Space.Compact>
      {open && (
        <DirectoryPicker
          initialPath={value || '/'}
          onSelect={(path) => {
            onChange?.(path);
            setOpen(false);
          }}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  );
}
