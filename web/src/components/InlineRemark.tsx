import { useState } from 'react';
import { Button, Input } from 'antd';

export default function InlineRemark({
  value,
  label,
  maxLength = 500,
  disabled,
  onSave,
}: {
  value: string;
  label: string;
  maxLength?: number;
  disabled?: boolean;
  onSave: (value: string) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  const [busy, setBusy] = useState(false);
  async function save() {
    if (draft === value) {
      setEditing(false);
      return;
    }
    setBusy(true);
    try {
      await onSave(draft);
      setEditing(false);
    } catch {
      // 接口统一提示错误，保留输入以便重试。
    } finally {
      setBusy(false);
    }
  }
  return editing ? (
    <Input
      autoFocus
      aria-label={label}
      value={draft}
      maxLength={maxLength}
      disabled={busy}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={() => void save()}
      onPressEnter={(event) => event.currentTarget.blur()}
    />
  ) : (
    <Button
      type="link"
      size="small"
      title={value || '设置备注'}
      aria-label={label}
      disabled={disabled}
      style={{
        display: 'block',
        width: '100%',
        height: 'auto',
        minHeight: 24,
        minWidth: 48,
        maxWidth: '100%',
        paddingInline: 0,
        whiteSpace: 'pre-wrap',
        overflowWrap: 'anywhere',
        textAlign: 'start',
      }}
      onClick={() => {
        setDraft(value);
        setEditing(true);
      }}
    >
      {value}
    </Button>
  );
}
