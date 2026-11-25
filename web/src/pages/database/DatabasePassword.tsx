import { useState } from 'react';
import { Button, Space, Tooltip, Typography } from 'antd';
import { Eye, EyeOff } from 'lucide-react';
import { request } from '../../api/client';

export default function DatabasePassword({
  serverId,
  name,
  recorded,
  revealUrl,
}: {
  serverId: string;
  name: string;
  recorded: boolean;
  revealUrl?: string;
}) {
  const [value, setValue] = useState<string>();
  const [busy, setBusy] = useState(false);
  const endpoint =
    revealUrl ??
    `/api/db/servers/${encodeURIComponent(serverId)}/databases/${encodeURIComponent(name)}/password/reveal`;
  if (!recorded)
    return (
      <Tooltip title="仅能显示面板记录的专属账号密码，无法还原已有密码">
        未记录
      </Tooltip>
    );
  const toggle = async () => {
    if (value !== undefined) {
      setValue(undefined);
      return;
    }
    setBusy(true);
    try {
      const result = await request<{ password: string }>(endpoint, {
        method: 'POST',
      });
      setValue(result.password);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Space size={0}>
      <Typography.Text
        copyable={{
          text: async () =>
            value ??
            (
              await request<{ password: string }>(endpoint, {
                method: 'POST',
              })
            ).password,
          tooltips: ['复制密码', '已复制'],
        }}
      >
        {value === undefined ? '********' : value || '空密码'}
      </Typography.Text>
      <Button
        type="text"
        size="small"
        aria-label={value === undefined ? '显示密码' : '隐藏密码'}
        loading={busy}
        icon={
          value === undefined ? (
            <Eye size={14} strokeWidth={1.9} />
          ) : (
            <EyeOff size={14} strokeWidth={1.9} />
          )
        }
        onClick={() => void toggle().catch(() => {})}
      />
    </Space>
  );
}
