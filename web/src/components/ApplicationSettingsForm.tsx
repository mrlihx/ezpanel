import { Form, type FormProps } from 'antd';
import type { ReactNode } from 'react';
import './ApplicationSettingsForm.css';

export default function ApplicationSettingsForm<Values>({
  className,
  ...props
}: Omit<FormProps<Values>, 'children'> & { children?: ReactNode }) {
  return (
    <Form<Values>
      {...props}
      layout="horizontal"
      labelAlign="left"
      labelWrap
      colon={false}
      className={['application-settings-form', className]
        .filter(Boolean)
        .join(' ')}
      labelCol={{ flex: '0 0 var(--application-setting-label-width)' }}
      wrapperCol={{ flex: '1 1 0' }}
    />
  );
}
