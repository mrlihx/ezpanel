import { useState, type ReactNode } from 'react';
import type { ModalProps } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { website, type SiteInput } from '../../api/website';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';
import type { SiteDatabaseInput } from './SiteForm';

export type AddProjectProps = {
  pending: boolean;
  error: ReactNode;
  onClose: () => void;
  onCreate: (values: SiteInput, database?: SiteDatabaseInput) => void;
};

export const addProjectStyles: ModalProps['styles'] = {
  content: {
    display: 'flex',
    flexDirection: 'column',
    maxHeight: `calc(100dvh - ${SPACING.xl * 2}px)`,
  },
  header: { flexShrink: 0 },
  body: { overflowY: 'auto', minHeight: 0 },
  footer: { flexShrink: 0 },
};

export type ProjectSettingsProps = {
  id: string;
  manageable: boolean;
  initialTab?: string;
  onClose: () => void;
  onChanged: () => void;
};

export function useSettingsDialog(
  { id, onClose }: ProjectSettingsProps,
  title: string,
) {
  const site = useQuery({
    queryKey: ['website', id],
    queryFn: () => website(id),
  });
  const [pending, setPending] = useState(false);
  const modalProps: ModalProps = {
    open: true,
    title: site.data
      ? `${title} · ${site.data.name} · 添加于 ${formatTime(site.data.createdAt)}`
      : title,
    width: LAYOUT.toolboxWidth,
    centered: true,
    footer: null,
    maskClosable: false,
    closable: !pending,
    keyboard: !pending,
    onCancel: () => {
      if (!pending) onClose();
    },
    styles: {
      body: {
        height: '72dvh',
        display: 'flex',
        flexDirection: 'column',
        overflow: 'hidden',
      },
    },
  };
  return { modalProps, content: { site, pending, setPending } };
}

export type SettingsDialogState = ReturnType<
  typeof useSettingsDialog
>['content'];
