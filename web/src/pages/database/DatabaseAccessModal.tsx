import { useState } from 'react';
import { App, Form, Input, Modal, Select } from 'antd';
import type { Database } from '../../api/database';
import { request } from '../../api/client';
import { LAYOUT } from '../../styles/tokens';

export default function DatabaseAccessModal({
  serverId,
  database,
  onClose,
  onSaved,
}: {
  serverId: string;
  database: Database;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const { message } = App.useApp();
  const access = Form.useWatch('access', form);
  return (
    <Modal
      open
      title={`设置数据库[${database.name}]权限`}
      width={LAYOUT.databaseModalWidth}
      maskClosable={false}
      confirmLoading={busy}
      cancelText="取消"
      okText="确认"
      onCancel={onClose}
      onOk={async () => {
        const values = await form.validateFields();
        setBusy(true);
        try {
          await request(
            `/api/db/servers/${serverId}/databases/${encodeURIComponent(database.name)}/access`,
            {
              method: 'PUT',
              data: {
                host:
                  values.access === 'all'
                    ? '%'
                    : values.access === 'ip'
                      ? values.ip.trim()
                      : 'localhost',
              },
            },
          );
          message.success('访问权限已修改');
          onSaved();
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
          access:
            database.localAccess || database.userHost === 'localhost'
              ? 'local'
              : database.userHost === '%'
                ? 'all'
                : 'ip',
          ip:
            database.localAccess ||
            database.userHost === 'localhost' ||
            database.userHost === '%'
              ? ''
              : database.userHost,
        }}
      >
        <Form.Item
          name="access"
          label="访问权限"
          extra="本地服务器与指定IP均默认允许宿主机及同机容器连接。"
        >
          <Select
            options={[
              { value: 'local', label: '本地服务器' },
              { value: 'all', label: '所有人' },
              { value: 'ip', label: '指定IP' },
            ]}
          />
        </Form.Item>
        {access === 'ip' && (
          <Form.Item
            name="ip"
            label="指定IP"
            rules={[
              {
                required: true,
                whitespace: true,
                message: '请输入允许访问的IP地址或网段',
              },
            ]}
          >
            <Input placeholder="IPv4/IPv6 地址或 IPv4 网段（如 192.168.1.0/24）" />
          </Form.Item>
        )}
      </Form>
    </Modal>
  );
}
