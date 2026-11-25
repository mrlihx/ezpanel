import { useState } from 'react';
import {
  Alert,
  Checkbox,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Typography,
} from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import type { Database } from '../../api/database';
import { request } from '../../api/client';
import { LAYOUT } from '../../styles/tokens';
import { formatSize } from '../../utils/format';

export type DatabaseBatchAction = 'access' | 'password' | 'delete';
const titles = {
  access: '设置权限',
  password: '修改密码',
  delete: '删除数据库',
};

export default function DatabaseBatchModal({
  serverId,
  action,
  databases,
  recyclable,
  onClose,
  onStarted,
}: {
  serverId: string;
  action: DatabaseBatchAction;
  databases: Database[];
  recyclable: boolean;
  onClose: () => void;
  onStarted: (id: string) => void;
}) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const [permanent, setPermanent] = useState(false);
  const [verify, setVerify] = useState(false);
  const [confirmation, setConfirmation] = useState('');
  const access = Form.useWatch('access', form);
  const removePermanently = !recyclable || permanent;
  const submit = async () => {
    const values = action === 'delete' ? {} : await form.validateFields();
    setBusy(true);
    try {
      const result = await request<{ taskId: string }>(
        `/api/db/servers/${encodeURIComponent(serverId)}/databases/batch`,
        {
          method: 'POST',
          data: {
            action,
            databases: databases.map((database) => database.name),
            ...(action === 'access'
              ? {
                  host:
                    values.access === 'all'
                      ? '%'
                      : values.access === 'ip'
                        ? values.ip.trim()
                        : 'localhost',
                }
              : action === 'password'
                ? { password: values.password }
                : { permanent: removePermanently, confirmation }),
          },
        },
      );
      onStarted(result.taskId);
      onClose();
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Modal
        open
        title={`批量${titles[action]}`}
        width={
          action === 'delete'
            ? LAYOUT.trashModalWidth
            : LAYOUT.databaseModalWidth
        }
        maskClosable={false}
        confirmLoading={busy}
        okButtonProps={{ danger: action === 'delete' }}
        onCancel={onClose}
        onOk={() => {
          if (action === 'delete') {
            setConfirmation('');
            setVerify(true);
          } else void submit().catch(() => {});
        }}
      >
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <Typography.Paragraph>
            已选 {databases.length} 个数据库：
            {databases.map((database) => database.name).join('、')}
          </Typography.Paragraph>
          {action === 'delete' ? (
            <>
              <Alert
                type="warning"
                showIcon
                message={
                  removePermanently
                    ? '删除后不可恢复，请确认已保留需要的数据。'
                    : '逐库备份至回收站后删除，某个库备份失败则保留该库。数据库账号及授权保留。'
                }
              />
              <ContentSizedTable
                fillColumn="name"
                size="small"
                pagination={false}
                rowKey="name"
                scroll={{ y: 240 }}
                dataSource={databases}
                columns={[
                  { title: '数据库名称', dataIndex: 'name' },
                  {
                    title: '数据库大小',
                    dataIndex: 'size',
                    render: (size: number) => formatSize(size),
                  },
                  {
                    title: '删除结果',
                    render: () =>
                      removePermanently ? '彻底删除' : '移至回收站',
                  },
                ]}
              />
              {recyclable && (
                <Checkbox
                  checked={permanent}
                  onChange={(event) => setPermanent(event.target.checked)}
                >
                  永久删除，不放入回收站
                </Checkbox>
              )}
            </>
          ) : (
            <Form
              form={form}
              layout="horizontal"
              labelCol={{ flex: LAYOUT.formLabelWidth }}
              wrapperCol={{ flex: 1 }}
              colon={false}
              initialValues={{ access: 'local' }}
              style={{ width: '100%' }}
            >
              {action === 'access' ? (
                <>
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
                </>
              ) : (
                <Form.Item
                  name="password"
                  label="新密码"
                  rules={[
                    { required: true, message: '请输入新密码' },
                    { max: 1024, message: '密码不能超过1024个字符' },
                  ]}
                >
                  <Input.Password autoComplete="new-password" />
                </Form.Item>
              )}
            </Form>
          )}
        </Space>
      </Modal>
      {action === 'delete' && (
        <Modal
          open={verify}
          title="二次验证信息—批量删除数据库"
          width={LAYOUT.shellWidth}
          confirmLoading={busy}
          onCancel={() => setVerify(false)}
          okButtonProps={{
            danger: true,
            disabled: confirmation !== '删除数据库',
          }}
          onOk={() => void submit().catch(() => {})}
        >
          <Alert
            type="warning"
            message={`即将${removePermanently ? '彻底删除' : '移至回收站'} ${databases.length} 个数据库`}
          />
          <Typography.Paragraph style={{ marginTop: 16 }}>
            请手动输入「删除数据库」完成验证
          </Typography.Paragraph>
          <Input
            autoFocus
            value={confirmation}
            aria-label="输入删除数据库完成二次确认"
            onChange={(event) => setConfirmation(event.target.value)}
          />
        </Modal>
      )}
    </>
  );
}
