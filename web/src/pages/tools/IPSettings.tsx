import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  Segmented,
  Select,
  Space,
  Switch,
  Tag,
} from 'antd';
import {
  finishIPSettings,
  tryIPSettings,
  type IPSettingsData,
} from '../../api/toolbox';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';

type IPForm = {
  enabled: boolean;
  dhcp: boolean;
  addresses: string;
  gateway: string;
  acceptRA: 'default' | 'yes' | 'no';
};

export default function IPSettings({
  data,
  onChanged,
}: {
  data: IPSettingsData;
  onChanged: () => Promise<unknown>;
}) {
  const { message } = App.useApp();
  const [selected, setSelected] = useState<string>();
  const [family, setFamily] = useState<4 | 6>(4);
  const start = useMutation({
    mutationFn: tryIPSettings,
    onSettled: onChanged,
  });
  const finish = useMutation({
    mutationFn: finishIPSettings,
    onSuccess: (_, action) =>
      void message.success(
        action === 'confirm' ? 'IP 设置已保存' : '已恢复原 IP 设置',
      ),
    onSettled: onChanged,
  });
  if (!data.supported) {
    return (
      <Alert
        showIcon
        type="info"
        message="当前系统需要 Netplan 和 D-Bus 才能修改 IP 设置"
      />
    );
  }
  const devices = data.list ?? [];
  const device = devices.find((item) => item.id === selected) ?? devices[0];
  if (!device) {
    return (
      <Alert showIcon type="info" message="未找到由 Netplan 管理的有线网卡" />
    );
  }
  const values = family === 4 ? device.ipv4 : device.ipv6;
  const busy = start.isPending || finish.isPending;
  return (
    <Space direction="vertical" size={SPACING.lg} style={{ width: '100%' }}>
      <Space wrap>
        <Select
          aria-label="网卡"
          style={{ minWidth: 160 }}
          value={device.id}
          onChange={setSelected}
          options={devices.map((item) => ({
            value: item.id,
            label: item.name,
          }))}
        />
        <Segmented<4 | 6>
          aria-label="IP 协议"
          value={family}
          onChange={setFamily}
          options={[
            { label: 'IPv4', value: 4 },
            { label: 'IPv6', value: 6 },
          ]}
        />
      </Space>
      <Space wrap>
        当前地址
        {device.current.length
          ? device.current.map((address) => <Tag key={address}>{address}</Tag>)
          : '暂无'}
      </Space>
      <Form<IPForm>
        key={`${device.id}:${family}:${JSON.stringify(values)}`}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1, style: { minWidth: 0 } }}
        colon={false}
        disabled={busy || !!data.trial}
        initialValues={{
          ...values,
          enabled: values.enabled ?? true,
          addresses: values.addresses.join('\n'),
          acceptRA:
            values.enabled === false || values.acceptRA === null
              ? 'default'
              : values.acceptRA
                ? 'yes'
                : 'no',
        }}
        onFinish={(form) => {
          const disableIPv6 = family === 6 && !form.enabled;
          start.mutate({
            interface: device.id,
            family,
            enabled: family === 6 ? form.enabled : undefined,
            dhcp: !disableIPv6 && form.dhcp,
            addresses: disableIPv6
              ? []
              : (form.addresses ?? '')
                  .split(/\r?\n/)
                  .map((address) => address.trim())
                  .filter(Boolean),
            gateway: disableIPv6 ? '' : form.gateway,
            acceptRA: disableIPv6
              ? false
              : family === 4 || form.acceptRA === 'default'
                ? null
                : form.acceptRA === 'yes',
          });
        }}
      >
        {family === 6 && (
          <Form.Item name="enabled" label="启用 IPv6" valuePropName="checked">
            <Switch />
          </Form.Item>
        )}
        <Form.Item
          noStyle
          shouldUpdate={(previous, next) => previous.enabled !== next.enabled}
        >
          {({ getFieldValue }) =>
            family === 6 && !getFieldValue('enabled') ? (
              <Alert
                type="info"
                message="关闭后将清除该网卡的 IPv6 地址和默认网关，并停用 DHCPv6 和路由通告。"
              />
            ) : (
              <>
                <Form.Item
                  name="dhcp"
                  label={family === 4 ? 'DHCP' : 'DHCPv6'}
                  valuePropName="checked"
                >
                  <Switch />
                </Form.Item>
                {family === 6 && (
                  <Form.Item name="acceptRA" label="IPv6 路由通告">
                    <Select
                      options={[
                        { label: '系统默认', value: 'default' },
                        { label: '接收', value: 'yes' },
                        { label: '关闭', value: 'no' },
                      ]}
                    />
                  </Form.Item>
                )}
                <Form.Item
                  name="addresses"
                  label="静态地址"
                  extra="填写 IP 和前缀长度，每行一个。可和 DHCP 一起使用。"
                >
                  <Input.TextArea
                    rows={3}
                    placeholder={
                      family === 4 ? '192.0.2.10/24' : '2001:db8::10/64'
                    }
                  />
                </Form.Item>
                <Form.Item name="gateway" label="静态网关">
                  <Input placeholder="可留空" />
                </Form.Item>
              </>
            )
          }
        </Form.Item>
        <Form.Item label=" ">
          <Button type="primary" htmlType="submit" loading={start.isPending}>
            试用设置
          </Button>
        </Form.Item>
      </Form>
      {data.trial && (
        <Alert
          showIcon
          type="warning"
          message={`IP 设置正在试用，请在 ${formatTime(data.trial.deadline)} 前确认`}
          description="超时自动恢复原设置。如果面板地址已变更，请用可访问的面板地址重新进入此页确认。"
          action={
            <Space wrap>
              <Button loading={busy} onClick={() => finish.mutate('cancel')}>
                恢复原设置
              </Button>
              <Button
                type="primary"
                loading={busy}
                onClick={() => finish.mutate('confirm')}
              >
                确认并保存
              </Button>
            </Space>
          }
        />
      )}
    </Space>
  );
}
