import { Alert, Modal, Select } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import type { PanelResourceKind } from '../../../sdk/ts/panel-sdk';
import { request } from '../api/client';

const labels: Record<PanelResourceKind, string> = {
  sites: '网站',
  containers: '容器',
  databases: '数据库实例',
  certificates: '证书',
  channels: '通知渠道',
};

export default function ResourcePicker({
  pluginId,
  kind,
  onFinish,
}: {
  pluginId: string;
  kind: PanelResourceKind;
  onFinish: (id: string | null) => void;
}) {
  const [value, setValue] = useState<string>();
  const options = useQuery({
    queryKey: ['plugins', 'choices', pluginId, kind],
    queryFn: ({ signal }) =>
      request<{ id: string; name: string }[]>(
        `/api/plugins/${encodeURIComponent(pluginId)}/choices/${encodeURIComponent(kind)}`,
        { signal, silentError: true },
      ),
    retry: false,
  });
  return (
    <Modal
      open
      title={`选择${labels[kind]}`}
      onCancel={() => onFinish(null)}
      onOk={() => onFinish(value ?? null)}
      okButtonProps={{ disabled: !value || options.isError }}
    >
      {options.isError ? (
        <Alert type="error" message={options.error.message} />
      ) : (
        <Select
          aria-label={`选择${labels[kind]}`}
          showSearch
          optionFilterProp="label"
          value={value}
          onChange={setValue}
          loading={options.isPending}
          style={{ width: '100%' }}
          options={options.data?.map((item) => ({
            value: item.id,
            label: item.name,
          }))}
        />
      )}
    </Modal>
  );
}
