import { firewallRuleNames } from './firewallLabels';
import {
  DatePicker,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Switch,
} from 'antd';
import { LAYOUT } from '../../styles/tokens';
import { saveFirewallRule } from '../../api/security';
import { type Dayjs } from 'dayjs';
import { countryOptions } from './countries';

import type { FirewallPageState } from './Firewall';

export default function FirewallRuleEditor({
  editing,
  form,
  kind,
  message,
  peerMode,
  portMode,
  query,
  setEditing,
  setWorking,
  working,
}: Pick<
  FirewallPageState,
  | 'editing'
  | 'form'
  | 'kind'
  | 'message'
  | 'peerMode'
  | 'portMode'
  | 'query'
  | 'setEditing'
  | 'setWorking'
  | 'working'
>) {
  return (
    <Modal
      open={!!editing}
      title={`${editing === 'new' ? '添加' : '修改'}${firewallRuleNames[kind]}`}
      width={LAYOUT.shellWidth}
      maskClosable={false}
      confirmLoading={working}
      onCancel={() => setEditing(undefined)}
      onOk={async () => {
        const values = await form.validateFields();
        setWorking(true);
        try {
          await saveFirewallRule(
            {
              kind,
              protocol: kind === 'region' ? 'both' : values.protocol || 'both',
              ports:
                kind === 'region' && values.portMode === 'all'
                  ? 'all'
                  : values.ports || '',
              peer:
                kind === 'region' ||
                (kind !== 'ip' && values.peerMode === 'all')
                  ? 'any'
                  : values.peer,
              direction: kind === 'region' ? 'both' : values.direction || 'in',
              country:
                kind === 'region'
                  ? (values.countries as string[]).join(',')
                  : '',
              targetIP: values.targetIP || '',
              targetPort: values.targetPort || 0,
              policy: kind === 'forward' ? 'allow' : values.policy,
              enabled: values.enabled,
              remark: values.remark || '',
              expiresAt: values.expiresAt
                ? (values.expiresAt as Dayjs).toISOString()
                : null,
            },
            editing && editing !== 'new' ? editing.id : undefined,
          );
          setEditing(undefined);
          await query.refetch();
          message.success('规则已保存');
        } finally {
          setWorking(false);
        }
      }}
    >
      <Form
        form={form}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1 }}
        colon={false}
      >
        {(kind === 'port' || kind === 'forward') && (
          <>
            <Form.Item
              name="protocol"
              label="协议"
              rules={[{ required: true }]}
            >
              <Select
                options={[
                  { label: 'TCP', value: 'tcp' },
                  { label: 'UDP', value: 'udp' },
                  { label: 'TCP/UDP', value: 'both' },
                ]}
              />
            </Form.Item>
            <Form.Item
              name="ports"
              label={kind === 'forward' ? '源端口' : '端口'}
              rules={[{ required: true }]}
            >
              <Input
                placeholder={
                  kind === 'forward' ? '如8080' : '如80、80,443、8000-8100'
                }
              />
            </Form.Item>
          </>
        )}
        {kind === 'forward' && (
          <>
            <Form.Item name="targetIP" label="目标IP">
              <Input placeholder="留空则转发到本机127.0.0.1" />
            </Form.Item>
            <Form.Item
              name="targetPort"
              label="目标端口"
              rules={[{ required: true }]}
            >
              <InputNumber min={1} max={65535} style={{ width: '100%' }} />
            </Form.Item>
          </>
        )}
        {kind === 'region' ? (
          <>
            <Form.Item
              name="countries"
              label="国家/地区"
              rules={[{ required: true, message: '请选择国家或地区' }]}
            >
              <Select
                mode="multiple"
                showSearch
                optionFilterProp="label"
                options={countryOptions}
                placeholder="可选择多个国家或地区"
                maxTagCount="responsive"
              />
            </Form.Item>
            <Form.Item name="portMode" label="端口">
              <Select
                options={[
                  { value: 'all', label: '全部端口' },
                  { value: 'specified', label: '指定端口' },
                ]}
              />
            </Form.Item>
            {portMode === 'specified' && (
              <Form.Item
                name="ports"
                label="指定端口"
                rules={[{ required: true, message: '请输入端口' }]}
              >
                <Input placeholder="如80,443、8000-8100，匹配TCP/UDP" />
              </Form.Item>
            )}
          </>
        ) : (
          <>
            {kind !== 'ip' && (
              <Form.Item name="peerMode" label="来源">
                <Select
                  options={[
                    { value: 'all', label: '所有IP' },
                    { value: 'specified', label: '指定IP' },
                  ]}
                />
              </Form.Item>
            )}
            {(kind === 'ip' || peerMode === 'specified') && (
              <Form.Item
                name="peer"
                label={kind === 'ip' ? 'IP地址/网段' : '指定IP'}
                rules={[{ required: true, message: '请输入IP或网段' }]}
                extra="每行一个IP或CIDR网段，可填写多个。"
              >
                <Input.TextArea
                  rows={5}
                  placeholder={'192.168.1.10\n192.168.2.0/24\n2001:db8::1'}
                />
              </Form.Item>
            )}
          </>
        )}
        {kind !== 'forward' && (
          <>
            <Form.Item name="policy" label="策略" rules={[{ required: true }]}>
              <Select
                options={[
                  { label: '放行', value: 'allow' },
                  { label: '拒绝', value: 'deny' },
                ]}
              />
            </Form.Item>
            {kind !== 'region' && (
              <Form.Item
                name="direction"
                label="方向"
                rules={[{ required: true }]}
              >
                <Select
                  options={[
                    { label: '入站', value: 'in' },
                    { label: '出站', value: 'out' },
                  ]}
                />
              </Form.Item>
            )}
          </>
        )}
        <Form.Item name="enabled" label="启用规则" valuePropName="checked">
          <Switch />
        </Form.Item>
        <Form.Item name="remark" label="备注">
          <Input maxLength={150} />
        </Form.Item>
        {kind === 'ip' && (
          <Form.Item name="expiresAt" label="过期时间">
            <DatePicker
              showTime
              placeholder="留空为永久"
              style={{ width: '100%' }}
            />
          </Form.Item>
        )}
      </Form>
    </Modal>
  );
}
