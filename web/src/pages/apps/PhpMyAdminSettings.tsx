import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import { useState } from 'react';
import {
  App,
  Button,
  Descriptions,
  Form,
  Modal,
  Select,
  Typography,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { request } from '../../api/client';
import type { ApplicationSettingsProps } from './ApplicationSettings';

export default function PhpMyAdminSettings({
  app,
  busy,
  uninstall,
  onClose,
}: ApplicationSettingsProps) {
  const { message } = App.useApp();
  const client = useQueryClient();
  const [phpVersion, setPHPVersion] = useState<string>();
  const settings = useQuery({
    queryKey: ['phpmyadmin-settings'],
    queryFn: () =>
      request<{ phpVersion: string; versions: string[] }>(
        '/api/phpmyadmin/settings',
      ),
  });
  const save = useMutation({
    mutationFn: (version: string) =>
      request('/api/phpmyadmin/settings', {
        method: 'PUT',
        data: { phpVersion: version },
      }),
    onSuccess: async () => {
      setPHPVersion(undefined);
      await Promise.all([
        client.invalidateQueries({ queryKey: ['phpmyadmin-settings'] }),
        client.invalidateQueries({ queryKey: ['phpmyadmin-status'] }),
      ]);
      void message.success('PHP版本已保存并生效');
    },
  });
  const state = useQuery({
    queryKey: ['phpmyadmin-status'],
    queryFn: () =>
      request<{ phpAvailable: boolean; reason: string }>(
        '/api/phpmyadmin/status',
      ),
  });
  const selected = phpVersion ?? settings.data?.phpVersion ?? '';
  const options: { label: string; value: string; disabled?: boolean }[] = [
    { label: '自动选择', value: '' },
    ...(settings.data?.versions ?? []).map((version) => ({
      label: `PHP ${version}`,
      value: version,
    })),
  ];
  if (selected && !settings.data?.versions.includes(selected)) {
    options.push({
      label: `PHP ${selected}（不可用）`,
      value: selected,
      disabled: true,
    });
  }
  return (
    <Modal
      open
      centered
      title="phpMyAdmin · 设置"
      width={600}
      onCancel={onClose}
      footer={null}
      maskClosable={false}
    >
      <div className="app-settings-stack" style={{ paddingTop: 6 }}>
        <div className="app-settings-section">
          <div className="app-settings-section-title">基本信息</div>
          <Descriptions
            size="small"
            column={1}
            items={[
              { key: 'version', label: '当前版本', children: app.version },
              {
                key: 'php',
                label: 'PHP 环境',
                children: state.data
                  ? state.data.phpAvailable
                    ? '可用'
                    : state.data.reason
                  : state.error?.message || '读取中',
              },
              {
                key: 'entry',
                label: '访问入口',
                children: (
                  <Link to="/databases/mysql" onClick={onClose}>
                    数据库管理
                  </Link>
                ),
              },
            ]}
          />
        </div>
        <div className="app-settings-section">
          <div className="app-settings-section-title">运行环境</div>
          <ApplicationSettingsForm onFinish={() => save.mutate(selected)}>
            <Form.Item
              label="PHP 版本"
              validateStatus={settings.isError ? 'error' : undefined}
              help={settings.error?.message}
              extra="仅显示已安装且满足 phpMyAdmin 运行要求的 PHP 版本。"
            >
              <Select
                aria-label="PHP版本"
                value={selected}
                options={options}
                onChange={setPHPVersion}
                loading={settings.isPending}
                disabled={busy || save.isPending || !settings.data}
              />
            </Form.Item>
            <Form.Item
              className="application-settings-actions"
              style={{ marginBottom: 0 }}
            >
              <Button
                type="primary"
                htmlType="submit"
                loading={save.isPending}
                disabled={
                  busy ||
                  !settings.data ||
                  selected === settings.data.phpVersion
                }
              >
                保存设置
              </Button>
            </Form.Item>
          </ApplicationSettingsForm>
          <Typography.Text type="secondary">
            在数据库列表点击「管理」自动登录；每次访问均需有效面板会话。
          </Typography.Text>
        </div>
        <div className="app-settings-section app-settings-danger">
          <div className="app-settings-row">
            <div className="app-settings-row-info">
              <span className="app-settings-row-title">卸载应用</span>
              <Typography.Text type="secondary">
                卸载后将移除 phpMyAdmin 管理入口，不影响数据库数据。
              </Typography.Text>
            </div>
            <Button
              danger
              disabled={busy || !app.uninstallable}
              onClick={uninstall}
            >
              卸载应用
            </Button>
          </div>
        </div>
      </div>
    </Modal>
  );
}
