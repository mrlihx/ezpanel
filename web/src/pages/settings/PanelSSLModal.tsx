import { useState } from 'react';
import {
  Alert,
  Button,
  Col,
  Descriptions,
  Form,
  Input,
  Modal,
  Row,
  Tabs,
} from 'antd';
import CertificateWorkspace from '../../components/CertificateWorkspace';
import type { NetworkSettings, PanelSettings } from '../../api/settings';
import { LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';
import { formatTime } from '../../utils/format';

export default function PanelSSLModal({
  settings,
  boundDomain,
  busy,
  onApply,
  onClose,
}: {
  settings: PanelSettings;
  boundDomain: string;
  busy: boolean;
  onApply: (values: NetworkSettings) => Promise<void>;
  onClose: () => void;
}) {
  const [form] = Form.useForm();
  const [tab, setTab] = useState('current');
  const info = settings.certificateInfo;
  const domain =
    boundDomain ||
    new URL(settings.publicUrl || window.location.origin).hostname;
  async function deploy(certificateId: string) {
    await onApply({
      boundDomain,
      sslEnabled: true,
      certificate: '',
      certificateId,
    });
  }
  return (
    <Modal
      open
      maskClosable={false}
      title="面板SSL配置"
      width={LAYOUT.toolboxWidth}
      onCancel={onClose}
      footer={null}
    >
      <Form
        form={form}
        component={false}
        layout="vertical"
        initialValues={{
          certificate: settings.certificate,
          privateKey: settings.privateKey ?? '',
        }}
        onFinish={(values) =>
          void onApply({
            boundDomain,
            sslEnabled: true,
            certificate: values.certificate,
            privateKey: values.privateKey,
          })
        }
      >
        <Tabs
          activeKey={tab}
          onChange={setTab}
          items={[
            {
              key: 'current',
              label: '当前证书',
              forceRender: true,
              children: (
                <>
                  {info && (
                    <Descriptions
                      size="small"
                      column={{ xs: 1, sm: 2 }}
                      style={{ marginBottom: SPACING.lg }}
                      items={[
                        {
                          key: 'domains',
                          label: '证书域名',
                          children: info.domains.join('、'),
                          span: 2,
                        },
                        {
                          key: 'issuer',
                          label: '签发机构',
                          children: info.issuer,
                        },
                        {
                          key: 'expiry',
                          label: '到期时间',
                          children: formatTime(info.notAfter),
                        },
                      ]}
                    />
                  )}
                  <Row gutter={SPACING.lg}>
                    <Col xs={24} md={12}>
                      <Form.Item
                        label="证书（PEM）"
                        name="certificate"
                        rules={[
                          {
                            required: tab === 'current',
                            message: '请填写完整证书链',
                          },
                        ]}
                        extra="完整证书链须覆盖面板访问域名或IP。"
                      >
                        <Input.TextArea
                          rows={12}
                          style={{ fontFamily: MONO_FONT }}
                          placeholder="-----BEGIN CERTIFICATE-----"
                          spellCheck={false}
                        />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={12}>
                      <Form.Item
                        label="私钥（KEY）"
                        name="privateKey"
                        rules={[
                          {
                            required:
                              tab === 'current' && !settings.keyConfigured,
                            message: '请填写与证书匹配的私钥',
                          },
                        ]}
                        extra="请粘贴与证书匹配的 PEM 私钥，支持直接查看或修改。"
                      >
                        <Input.TextArea
                          rows={12}
                          style={{ fontFamily: MONO_FONT }}
                          placeholder="-----BEGIN PRIVATE KEY-----"
                          autoComplete="off"
                          spellCheck={false}
                        />
                      </Form.Item>
                    </Col>
                  </Row>
                </>
              ),
            },
            {
              key: 'free',
              label: '申请证书',
              children: (
                <CertificateWorkspace
                  mode="free"
                  defaultDomains={[domain]}
                  autoTempSite
                  onDeploy={deploy}
                />
              ),
            },
            {
              key: 'folder',
              label: '证书夹',
              children: (
                <CertificateWorkspace mode="folder" onDeploy={deploy} />
              ),
            },
          ]}
        />
        <Alert
          type="info"
          showIcon
          message="保存或部署证书后，面板将切换为HTTPS并要求重新登录。"
          style={{ marginBottom: SPACING.lg }}
        />
        {tab === 'current' && (
          <Button type="primary" onClick={() => form.submit()} loading={busy}>
            保存并应用
          </Button>
        )}
      </Form>
    </Modal>
  );
}
