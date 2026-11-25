import { useState } from 'react';
import { Alert, Checkbox, Input, Modal, Space, Typography } from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import { request } from '../../api/client';
import { LAYOUT } from '../../styles/tokens';
import { formatSize, formatTime } from '../../utils/format';

export default function DeleteDatabaseModal({
  name,
  url,
  recyclable,
  size,
  location,
  createdAt,
  onClose,
  onDeleted,
}: {
  name: string;
  url: string;
  recyclable: boolean;
  size?: number;
  location?: string;
  createdAt?: string;
  onClose: () => void;
  onDeleted: (result: { taskId?: string }) => void;
}) {
  const [confirmation, setConfirmation] = useState(''),
    [verify, setVerify] = useState(false),
    [permanent, setPermanent] = useState(false),
    [busy, setBusy] = useState(false);
  const removePermanently = !recyclable || permanent;
  return (
    <>
      <Modal
        open
        title="删除数据库"
        width={LAYOUT.trashModalWidth}
        onCancel={onClose}
        okText="确定"
        okButtonProps={{ danger: true }}
        onOk={() => {
          setConfirmation('');
          setVerify(true);
        }}
      >
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <Alert
            showIcon
            type="warning"
            message="警告"
            description={
              removePermanently
                ? '删除数据库后，数据库内的所有数据将被清空，且不可恢复。'
                : '删除前会备份至数据库回收站；备份失败则不会删除原库。数据库账号和授权保留。'
            }
          />
          <ContentSizedTable
            fillColumn="name"
            size="small"
            pagination={false}
            rowKey="name"
            dataSource={[{ name, size, location, createdAt }]}
            columns={[
              { title: '数据库名称', dataIndex: 'name' },
              {
                title: '数据库大小',
                dataIndex: 'size',
                render: (value) => (value == null ? '—' : formatSize(value)),
              },
              {
                title: '数据库位置',
                dataIndex: 'location',
                render: (value) => value || '—',
              },
              {
                title: '创建时间',
                dataIndex: 'createdAt',
                render: (value) => (value ? formatTime(value) : '—'),
              },
              {
                title: '删除结果',
                render: () => (
                  <Typography.Text
                    type={removePermanently ? 'danger' : undefined}
                  >
                    {removePermanently ? '彻底删除' : '移至回收站'}
                  </Typography.Text>
                ),
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
          <Typography.Paragraph type="danger">
            请仔细核对以上删除信息，防止数据库被误删。
            {!recyclable && '当前数据库不支持回收站，请谨慎操作！'}
          </Typography.Paragraph>
        </Space>
      </Modal>
      <Modal
        open={verify}
        title="二次验证信息—删除数据库"
        width={LAYOUT.shellWidth}
        onCancel={() => setVerify(false)}
        confirmLoading={busy}
        okText="确定"
        okButtonProps={{
          danger: true,
          disabled: confirmation !== '删除数据库',
        }}
        onOk={async () => {
          setBusy(true);
          try {
            const result = await request<{ taskId?: string } | null>(url, {
              method: 'DELETE',
              data: {
                confirmName: name,
                confirmation,
                ...(url.includes('/nosql/')
                  ? {}
                  : { permanent: removePermanently }),
              },
            });
            onDeleted(result || {});
            onClose();
          } finally {
            setBusy(false);
          }
        }}
      >
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <Alert
            type="warning"
            showIcon
            message={`即将${removePermanently ? '彻底删除' : '移至回收站'}数据库「${name}」`}
            description={
              removePermanently
                ? '删除后不可恢复，请确认已保留需要的数据。'
                : '数据库将先备份后删除，恢复请前往数据库回收站。'
            }
          />
          <div>
            <Typography.Paragraph>
              请手动输入{' '}
              <Typography.Text type="danger">删除数据库</Typography.Text>
              ，完成验证
            </Typography.Paragraph>
            <Input
              autoFocus
              value={confirmation}
              aria-label="输入删除数据库完成二次确认"
              onChange={(event) => setConfirmation(event.target.value)}
            />
          </div>
        </Space>
      </Modal>
    </>
  );
}
