import { Descriptions, Input, Typography } from 'antd';
import { FileText, Folder } from 'lucide-react';
import ContentSizedTable from '../../components/ContentSizedTable';
import type { FileEntry, FileProperties } from '../../api/file';
import { formatSize, formatTime } from '../../utils/format';
import { DirectorySize } from './MetadataCells';

export default function PropertyInfo({
  data,
  entry,
  details = false,
}: {
  data?: FileProperties;
  entry: FileEntry;
  details?: boolean;
}) {
  const file = data?.entry || entry;
  const size = file.isDir ? (
    <DirectorySize entry={file} />
  ) : file.size === null ? (
    '—'
  ) : (
    formatSize(file.size)
  );
  const values = [
    { key: 'name', label: '文件名', children: file.name },
    {
      key: 'type',
      label: '类型',
      children: data?.type || (file.isDir ? '文件夹' : '文件'),
    },
    {
      key: 'path',
      label: '文件路径',
      children: (
        <Typography.Text copyable className="file-property-value">
          {file.path}
        </Typography.Text>
      ),
    },
    { key: 'size', label: '大小', children: size },
    {
      key: 'access',
      label: '访问时间',
      children: data?.accessTime ? formatTime(data.accessTime) : '—',
    },
    { key: 'modified', label: '修改时间', children: formatTime(file.modTime) },
    {
      key: 'change',
      label: '元数据修改时间',
      children: data?.changeTime ? formatTime(data.changeTime) : '—',
    },
    { key: 'md5', label: '文件MD5', children: data?.md5 || '—' },
    { key: 'sha1', label: '文件SHA1', children: data?.sha1 || '—' },
    { key: 'owner', label: '所属用户', children: file.owner },
    { key: 'group', label: '所属组', children: file.group },
    { key: 'mode', label: '文件权限', children: file.mode },
    { key: 'attributes', label: '特殊权限', children: data?.attributes || '—' },
    { key: 'uid', label: '用户id', children: data?.uid ?? '—' },
    { key: 'gid', label: '用户组id', children: data?.gid ?? '—' },
    { key: 'links', label: 'inode链接数', children: data?.links ?? '—' },
    { key: 'inode', label: 'inode节点号', children: data?.inode ?? '—' },
    {
      key: 'inode-mode',
      label: 'inode保护模式',
      children: data?.inodeMode ?? '—',
    },
    { key: 'device', label: 'inode驻留设备', children: data?.device ?? '—' },
    ...(data?.linkTarget
      ? [{ key: 'link', label: '链接目标', children: data.linkTarget }]
      : []),
  ];
  if (details)
    return (
      <ContentSizedTable
        legacySizing
        fillColumn="value"
        rowKey="key"
        size="small"
        pagination={false}
        scroll={{ y: 520 }}
        dataSource={values.map(({ children, ...row }) => ({
          ...row,
          value: children,
        }))}
        columns={[
          { title: '属性', dataIndex: 'label', width: 180 },
          {
            title: '值',
            dataIndex: 'value',
            render: (value) => (
              <span className="file-property-value">{value}</span>
            ),
          },
        ]}
      />
    );
  const group = (keys: string[]) =>
    values.filter((value) => keys.includes(value.key));
  return (
    <>
      <div className="file-property-heading">
        {file.isDir ? (
          <Folder
            size={42}
            strokeWidth={1.5}
            fill="currentColor"
            style={{ color: 'var(--ant-color-primary)', flexShrink: 0 }}
          />
        ) : (
          <FileText
            size={42}
            strokeWidth={1.5}
            style={{ color: 'var(--ant-color-text-secondary)', flexShrink: 0 }}
          />
        )}
        <Input value={file.name} readOnly aria-label="文件名称" />
      </div>
      {[
        group(['type', 'path', 'size', 'link']),
        group(['mode', 'group', 'owner']),
        group(['access', 'modified']),
      ].map((items, index) => (
        <div className="file-property-group" key={index}>
          <Descriptions column={1} items={items} labelStyle={{ width: 150 }} />
        </div>
      ))}
    </>
  );
}
