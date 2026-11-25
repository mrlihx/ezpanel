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

export function PhpAddModal({
  pending,
  error,
  onClose,
  onCreate,
}: AddProjectProps) {
  const [form] = Form.useForm<SiteFormValues>();
  return (
    <Modal
      title="添加PHP项目"
      open
      width={LAYOUT.databaseModalWidth}
      centered
      styles={addProjectStyles}
      onCancel={() => {
        if (!pending) onClose();
      }}
      onOk={() => form.submit()}
      okText="创建站点"
      confirmLoading={pending}
      closable={!pending}
      cancelButtonProps={{ disabled: pending }}
    >
      {error}
      <SiteForm projectType="php" form={form} onFinish={onCreate} />
    </Modal>
  );
}

export function PhpSettingsModal(props: ProjectSettingsProps) {
  const dialog = useSettingsDialog(props, 'PHP项目设置');
  const sections = [
    'domains',
    'directory',
    'php',
    'proxy',
    'ssl',
    'rewrite',
    'redirect',
    'logs',
    'config',
  ].map((key) => ({ key }));
  return (
    <Modal {...dialog.modalProps}>
      <SiteSettingsContent {...props} {...dialog.content} sections={sections} />
    </Modal>
  );
}
