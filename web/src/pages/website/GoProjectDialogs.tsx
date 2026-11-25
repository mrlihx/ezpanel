import { Form, Modal } from 'antd';
import { LAYOUT } from '../../styles/tokens';
import SiteForm, { type SiteFormValues } from './SiteForm';
import SiteSettingsContent from './SiteSettingsContent';
import {
  addProjectStyles,
  useSettingsDialog,
  type AddProjectProps,
  type ProjectSettingsProps,
} from './projectDialogs';

export function GoAddModal({
  pending,
  error,
  onClose,
  onCreate,
}: AddProjectProps) {
  const [form] = Form.useForm<SiteFormValues>();
  return (
    <Modal
      title="添加Go项目"
      open
      width={LAYOUT.databaseModalWidth}
      centered
      styles={addProjectStyles}
      onCancel={() => {
        if (!pending) onClose();
      }}
      onOk={() => form.submit()}
      okText="创建项目"
      confirmLoading={pending}
      closable={!pending}
      cancelButtonProps={{ disabled: pending }}
    >
      {error}
      <SiteForm projectType="go" form={form} onFinish={onCreate} />
    </Modal>
  );
}

export function GoSettingsModal(props: ProjectSettingsProps) {
  const dialog = useSettingsDialog(props, 'Go项目设置');
  const sections = [
    { key: 'runtime' },
    { key: 'domains', label: '域名管理' },
    { key: 'rewrite' },
    { key: 'ssl' },
    { key: 'redirect' },
    { key: 'runtime-load' },
    { key: 'runtime-service' },
    { key: 'runtime-logs', label: '项目日志' },
    { key: 'logs', label: '网站日志' },
    ...(!dialog.content.site.data?.runtimeVersion
      ? [{ key: 'proxy' }, { key: 'directory' }]
      : []),
  ];
  return (
    <Modal {...dialog.modalProps}>
      <SiteSettingsContent {...props} {...dialog.content} sections={sections} />
    </Modal>
  );
}
