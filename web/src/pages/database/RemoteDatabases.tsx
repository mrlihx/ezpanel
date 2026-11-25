import { Button, Modal, Space, Grid } from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
type Remote = {
  id: string;
  name: string;
  host: string;
  port: number;
  username: string;
};
export default function RemoteDatabases<T extends Remote>({
  label,
  rows,
  onClose,
  onAdd,
  onEdit,
  onDelete,
}: {
  label: string;
  rows: T[];
  onClose: () => void;
  onAdd: () => void;
  onEdit: (row: T) => void;
  onDelete: (row: T) => void;
}) {
  const fixedActions = Grid.useBreakpoint().lg ? 'right' : undefined;
  return (
    <Modal
      open
      title={`${label} · 远程数据库`}
      width={920}
      footer={null}
      onCancel={onClose}
    >
      <Button type="primary" onClick={onAdd} style={{ marginBottom: 16 }}>
        添加远程数据库
      </Button>
      <ContentSizedTable<T>
        fillColumn="host"
        rowKey="id"
        dataSource={rows}
        columns={[
          {
            title: '服务器地址',
            dataIndex: 'host',
            width: 220,
            ellipsis: true,
          },
          { title: '数据库端口', dataIndex: 'port', width: 110 },
          {
            title: '管理员名称',
            dataIndex: 'username',
            width: 160,
            ellipsis: true,
          },
          { title: '备注', dataIndex: 'name', width: 200 },
          {
            title: '操作',
            width: 140,
            fixed: fixedActions,
            render: (_, row) => (
              <Space>
                <Button type="link" onClick={() => onEdit(row)}>
                  编辑
                </Button>
                <Button type="link" danger onClick={() => onDelete(row)}>
                  删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
    </Modal>
  );
}
