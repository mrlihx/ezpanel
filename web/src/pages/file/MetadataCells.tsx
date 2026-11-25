import { Button, Tooltip } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { directorySize, saveFileRemark, type FileEntry } from '../../api/file';
import { formatSize } from '../../utils/format';
import InlineRemark from '../../components/InlineRemark';

export function DirectorySize({ entry }: { entry: FileEntry }) {
  const size = useQuery({
    queryKey: ['file-directory-size', entry.path, entry.modTime],
    queryFn: ({ signal }) => directorySize(entry.path, signal),
    enabled: false,
    retry: false,
  });
  return (
    <Tooltip
      title={
        size.error?.message ||
        (size.data ? '点击重新计算，符号链接不展开' : '后台计算文件夹大小')
      }
    >
      <Button
        type="link"
        size="small"
        loading={size.isFetching}
        onClick={() => void size.refetch()}
      >
        {size.isFetching
          ? '计算中'
          : size.error
            ? '重试'
            : size.data
              ? formatSize(size.data.size)
              : '计算'}
      </Button>
    </Tooltip>
  );
}
export function FileRemark({
  entry,
  onSaved,
}: {
  entry: FileEntry;
  onSaved: () => void;
}) {
  return (
    <InlineRemark
      value={entry.remark}
      label={`编辑${entry.name}备注`}
      disabled={entry.protected || entry.remarkReadonly}
      onSave={async (value) => {
        await saveFileRemark(entry.path, value);
        onSaved();
      }}
    />
  );
}
