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

export function PythonAddModal({
  pending,
  error,
  onClose,
  onCreate,
}: AddProjectProps) {
  const [form] = Form.useForm<SiteFormValues>();
  return (
    <Modal
      title="添加Python项目"
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
      <SiteForm projectType="python" form={form} onFinish={onCreate} />
    </Modal>
  );
}

export function PythonSettingsModal(props: ProjectSettingsProps) {
  const dialog = useSettingsDialog(props, 'Python项目设置');
  const launch = dialog.content.site.data?.runtimeLaunch;
  const sections = [
    { key: 'runtime' },
    { key: 'domains', label: '域名管理' },
    { key: 'ssl' },
    { key: 'redirect' },
    ...(launch === 'uwsgi' || launch === 'gunicorn'
      ? [
          {
            key: 'wsgi',
            label: launch === 'uwsgi' ? 'uWSGI配置' : 'Gunicorn配置',
          },
        ]
      : []),
    { key: 'dependencies' },
    { key: 'runtime-load' },
    { key: 'runtime-service' },
    { key: 'runtime-logs', label: '项目日志' },
    { key: 'logs', label: '网站日志' },
  ];
  return (
    <Modal {...dialog.modalProps}>
      <SiteSettingsContent {...props} {...dialog.content} sections={sections} />
    </Modal>
  );
}
