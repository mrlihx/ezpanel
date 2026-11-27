import { Button, Dropdown, Space, Typography } from 'antd';
import {
  ClipboardPaste,
  Copy,
  Ellipsis,
  FileArchive,
  Scissors,
  Trash2,
  X,
} from 'lucide-react';
import type { Clipboard } from './FileBrowser';
import type { FileAction, FilePluginAction } from './FileList';

export default function FileBatchToolbar({
  count,
  archiveAction,
  mobileSelect,
  allSelected,
  canSelect,
  clipboard,
  onSelectAll,
  onClear,
  onAction,
  pluginActions,
  onPlugin,
  onPaste,
  onClearClipboard,
}: {
  count: number;
  archiveAction: 'compress' | 'extract';
  mobileSelect: boolean;
  allSelected: boolean;
  canSelect: boolean;
  clipboard: Clipboard;
  onSelectAll: () => void;
  onClear: () => void;
  onAction: (action: FileAction) => void;
  pluginActions: FilePluginAction[];
  onPlugin: (id: string) => void;
  onPaste: () => void;
  onClearClipboard: () => void;
}) {
  const selecting = count > 0 || mobileSelect;
  const archiveLabel = archiveAction === 'extract' ? '解压' : '压缩';
  return (
    <div className="file-toolbar-selection" aria-label="文件批量操作">
      {selecting && (
        <div className="file-batch-files">
          <div className="file-batch-header">
            <Typography.Text className="file-selection-count" role="status">
              已选 <strong>{count}</strong> 项
            </Typography.Text>
            <Space size={4} className="file-batch-mobile-controls">
              <Button
                size="small"
                type="text"
                disabled={!canSelect}
                onClick={onSelectAll}
              >
                {allSelected ? '取消全选' : '全选本页'}
              </Button>
              <Button size="small" type="link" onClick={onClear}>
                完成
              </Button>
            </Space>
          </div>
          <div className="file-batch-actions">
            <Button
              size="small"
              disabled={!count}
              icon={<Copy size={14} />}
              onClick={() => onAction('copy')}
            >
              复制
            </Button>
            <Button
              size="small"
              disabled={!count}
              icon={<Scissors size={14} />}
              onClick={() => onAction('move')}
            >
              剪切
            </Button>
            <Button
              size="small"
              disabled={!count}
              className="file-batch-desktop-action"
              icon={<FileArchive size={14} />}
              onClick={() => onAction(archiveAction)}
            >
              {archiveLabel}
            </Button>
            <Button
              size="small"
              danger
              disabled={!count}
              icon={<Trash2 size={14} />}
              onClick={() => onAction('delete')}
            >
              删除
            </Button>
            <div className="file-batch-mobile-controls">
              <Dropdown
                trigger={['click']}
                disabled={!count}
                menu={{
                  items: [
                    { key: archiveAction, label: archiveLabel },
                    { key: 'permissions', label: '权限' },
                  ],
                  onClick: ({ key }) => onAction(key as FileAction),
                }}
              >
                <Button
                  size="small"
                  icon={<Ellipsis size={14} />}
                  disabled={!count}
                >
                  更多
                </Button>
              </Dropdown>
            </div>
            <Button
              size="small"
              type="text"
              className="file-batch-desktop-action"
              onClick={onClear}
            >
              取消选择
            </Button>
            {count > 0 && pluginActions.length > 0 && (
              <Dropdown
                trigger={['click']}
                menu={{
                  items: pluginActions.map((action) => ({
                    key: action.id,
                    label: action.title,
                  })),
                  onClick: ({ key }) => onPlugin(key),
                }}
              >
                <Button size="small">插件操作</Button>
              </Dropdown>
            )}
          </div>
        </div>
      )}
      {clipboard && (
        <div className="file-batch-clipboard">
          <Typography.Text type="secondary">
            待{clipboard.type === 'copy' ? '复制' : '移动'}{' '}
            {clipboard.paths.length} 项
          </Typography.Text>
          <Button
            size="small"
            icon={<ClipboardPaste size={14} />}
            onClick={onPaste}
          >
            粘贴到此处
          </Button>
          <Button
            type="text"
            size="small"
            aria-label="清空剪贴板"
            icon={<X size={14} />}
            onClick={onClearClipboard}
          />
        </div>
      )}
    </div>
  );
}
