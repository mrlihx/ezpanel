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

export function ProxyAddModal({
  pending,
  error,
  onClose,
  onCreate,
}: AddProjectProps) {
  const [form] = Form.useForm<SiteFormValues>();
  return (
    <Modal
      title="添加反向代理"
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
      <SiteForm projectType="proxy" form={form} onFinish={onCreate} />
    </Modal>
  );
}

export function ProxySettingsModal(props: ProjectSettingsProps) {
  const dialog = useSettingsDialog(props, '反向代理设置');
  const sections = [
    'domains',
    'directory',
    'proxy',
    'ssl',
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
