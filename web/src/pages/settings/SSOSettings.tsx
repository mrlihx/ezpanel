import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Skeleton,
  Switch,
  Typography,
} from 'antd';
import {
  saveSSOConfig,
  ssoConfig,
  type SSOConfig,
  type SSOInput,
} from '../../api/auth';
import { usePluginList } from '../../api/plugin';
import { LAYOUT, SPACING } from '../../styles/tokens';

export default function SSOSettings({
  initialEnabled,
  onSaved,
  hideSubmit,
  formId,
}: {
  initialEnabled?: boolean;
  onSaved?: (data: SSOConfig) => void;
  hideSubmit?: boolean;
  formId?: string;
} = {}) {
  const query = useQuery({ queryKey: ['panel-sso'], queryFn: ssoConfig });
  if (query.error) return <Alert type="error" message={query.error.message} />;
  if (!query.data) return <Skeleton active />;
  return (
    <SSOForm
      key={`${query.dataUpdatedAt}-${String(initialEnabled)}`}
      config={query.data}
      initialEnabled={initialEnabled}
      onSaved={onSaved}
      hideSubmit={hideSubmit}
      formId={formId}
    />
  );
}

export function SSOModal({
  open,
  initialEnabled,
  onClose,
}: {
  open: boolean;
  initialEnabled?: boolean;
  onClose: () => void;
}) {
  return (
    <Modal
      open={open}
      title="单点登录设置"
      width={600}
      okText="保存"
      okButtonProps={{ htmlType: 'submit', form: 'panel-sso-modal-form' }}
      destroyOnHidden
      onCancel={onClose}
    >
      <SSOSettings
        initialEnabled={initialEnabled}
        hideSubmit
        formId="panel-sso-modal-form"
        onSaved={onClose}
      />
    </Modal>
  );
}

function SSOForm({
  config,
  initialEnabled,
  onSaved,
  hideSubmit,
  formId,
}: {
  config: SSOConfig;
  initialEnabled?: boolean;
  onSaved?: (data: SSOConfig) => void;
  hideSubmit?: boolean;
  formId?: string;
}) {
  const [form] = Form.useForm<SSOInput>();
  const { message } = App.useApp();
  const client = useQueryClient();
  const plugins = usePluginList(true);
  const effectiveEnabled = initialEnabled ?? config.enabled;
  const enabled = Form.useWatch('enabled', form) ?? effectiveEnabled;
  const options = (plugins.data ?? []).flatMap((plugin) =>
    plugin.ssoProvider
      ? [
          {
            value: plugin.id,
            label: plugin.ssoProvider.title,
            disabled: plugin.runtimeStatus !== 'running',
          },
        ]
      : [],
  );
  const save = useMutation({
    mutationFn: saveSSOConfig,
    onSuccess: (data) => {
      client.setQueryData(['panel-sso'], data);
      void client.invalidateQueries({ queryKey: ['panel-branding'] });
      void message.success('单点登录设置已保存');
      onSaved?.(data);
    },
  });
  return (
    <Form
      id={formId}
      form={form}
      initialValues={{
        enabled: effectiveEnabled,
        pluginId: config.pluginId,
        issuer: config.issuer,
        clientId: config.clientId,
        subject: config.subject,
      }}
      labelCol={{ flex: LAYOUT.formLabelWidth }}
      wrapperCol={{ flex: 1 }}
      colon={false}
      onFinish={(values) => save.mutate(values)}
    >
      <Form.Item name="enabled" label="单点登录" valuePropName="checked">
        <Switch />
      </Form.Item>
      <Form.Item
        name="pluginId"
        label="登录渠道"
        rules={[{ required: enabled }]}
      >
        <Select
          options={options}
          loading={plugins.isFetching}
          placeholder="选择已启用的 OIDC 插件"
        />
      </Form.Item>
      <Form.Item label="回调地址">
        <Typography.Text copyable>{config.callback}</Typography.Text>
      </Form.Item>
      <Form.Item
        name="issuer"
        label="签发者地址"
        rules={[{ required: enabled }]}
      >
        <Input placeholder="https://identity.example.com/realm" />
      </Form.Item>
      <Form.Item
        name="clientId"
        label="客户端 ID"
        rules={[{ required: enabled }]}
      >
        <Input maxLength={256} />
      </Form.Item>
      <Form.Item
        name="clientSecret"
        label="客户端密钥"
        rules={[{ required: enabled && !config.secretSet }]}
      >
        <Input.Password
          maxLength={4096}
          autoComplete="new-password"
          placeholder={config.secretSet ? '留空保留现有密钥' : undefined}
        />
      </Form.Item>
      <Form.Item
        name="subject"
        label="账号 Subject"
        rules={[{ required: enabled }]}
        style={hideSubmit ? { marginBottom: 0 } : undefined}
      >
        <Input maxLength={255} placeholder="身份系统签发的 sub 值" />
      </Form.Item>
      {!hideSubmit && (
        <Button
          type="primary"
          htmlType="submit"
          loading={save.isPending}
          style={{ marginTop: SPACING.sm }}
        >
          保存
        </Button>
      )}
    </Form>
  );
}
