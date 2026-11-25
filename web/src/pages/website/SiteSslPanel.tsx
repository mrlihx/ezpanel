import {
  Alert,
  Row,
  Col,
  Button,
  Input,
  Space,
  Switch,
  Tabs,
  Typography,
  Upload,
} from 'antd';
import { MONO_FONT } from '../../styles/tokens';
import { UploadOutlined } from '@ant-design/icons';
import { disableSSL, checkSSLCertificate, saveSSL } from '../../api/website';
import CertificateWorkspace from '../../components/CertificateWorkspace';

import type { SiteSettingsState } from './SiteSettingsContent';
const errorText = (e: unknown) => (e instanceof Error ? e.message : '请求失败');

export default function SiteSslPanel({
  cert,
  certName,
  certificate,
  finish,
  force,
  id,
  privateKey,
  keyName,
  manageable,
  modal,
  pending,
  setCert,
  setCertName,
  setError,
  setForce,
  setKey,
  setKeyName,
  setPending,
  site,
  submitSSL,
  upload,
}: Pick<
  SiteSettingsState,
  | 'cert'
  | 'certName'
  | 'certificate'
  | 'finish'
  | 'force'
  | 'id'
  | 'privateKey'
  | 'keyName'
  | 'manageable'
  | 'modal'
  | 'pending'
  | 'setCert'
  | 'setCertName'
  | 'setError'
  | 'setForce'
  | 'setKey'
  | 'setKeyName'
  | 'setPending'
  | 'submitSSL'
  | 'upload'
> & {
  site: NonNullable<SiteSettingsState['site']['data']>;
}) {
  return (
    <Tabs
      items={[
        {
          key: 'current',
          label: '当前证书',
          children: (
            <Space direction="vertical" size="large" style={{ width: '100%' }}>
              <Typography.Text>
                {site.sslEnabled
                  ? `SSL 已启用，到期时间：${site.sslExpireAt ? new Date(site.sslExpireAt).toLocaleString() : '无法读取'}`
                  : 'SSL 尚未启用'}
              </Typography.Text>
              <Typography.Text type="secondary">
                粘贴 PEM
                证书链及匹配私钥，也可上传文件。支持直接查看、复制或修改已保存的证书与私钥。HTTPS监听443，强制HTTPS会跳转到443。
              </Typography.Text>
              {certificate.isError && (
                <Alert
                  type="error"
                  message={certificate.error.message}
                  action={
                    <Button onClick={() => void certificate.refetch()}>
                      重试
                    </Button>
                  }
                />
              )}
              <Row gutter={[16, 16]}>
                <Col xs={24} md={12}>
                  <Space direction="vertical" style={{ width: '100%' }}>
                    <Typography.Text strong>证书（PEM格式）</Typography.Text>
                    <Input.TextArea
                      aria-label="证书"
                      value={cert ?? certificate.data?.certificate ?? ''}
                      onChange={(e) => setCert(e.target.value)}
                      rows={14}
                      spellCheck={false}
                      style={{ fontFamily: MONO_FONT }}
                      placeholder="-----BEGIN CERTIFICATE-----"
                    />
                    <Upload
                      accept=".pem,.crt,.cer"
                      showUploadList={false}
                      beforeUpload={(file) => upload(file, false)}
                    >
                      <Button icon={<UploadOutlined />}>上传证书链</Button>
                    </Upload>
                    {certName && (
                      <Typography.Text type="secondary">
                        {certName}
                      </Typography.Text>
                    )}
                  </Space>
                </Col>
                <Col xs={24} md={12}>
                  <Space direction="vertical" style={{ width: '100%' }}>
                    <Typography.Text strong>私钥（KEY格式）</Typography.Text>
                    <Input.TextArea
                      aria-label="私钥"
                      value={privateKey ?? certificate.data?.privateKey ?? ''}
                      onChange={(e) => setKey(e.target.value)}
                      rows={14}
                      spellCheck={false}
                      autoComplete="off"
                      style={{ fontFamily: MONO_FONT }}
                      placeholder="-----BEGIN PRIVATE KEY-----"
                    />
                    <Upload
                      accept=".pem,.key"
                      showUploadList={false}
                      beforeUpload={(file) => upload(file, true)}
                    >
                      <Button icon={<UploadOutlined />}>上传私钥</Button>
                    </Upload>
                    {keyName && (
                      <Typography.Text type="secondary">
                        {keyName}
                      </Typography.Text>
                    )}
                  </Space>
                </Col>
              </Row>
              <Space>
                <Switch
                  checked={force ?? site.forceHttps}
                  onChange={setForce}
                />
                <Typography.Text>强制 HTTPS</Typography.Text>
              </Space>
              <Space>
                <Button
                  type="primary"
                  disabled={
                    !manageable ||
                    (!site.sslEnabled &&
                      (!(cert ?? certificate.data?.certificate ?? '').trim() ||
                        !(
                          privateKey ??
                          certificate.data?.privateKey ??
                          ''
                        ).trim()))
                  }
                  loading={pending}
                  onClick={() => void submitSSL()}
                >
                  保存 SSL
                </Button>
                <Button
                  danger
                  disabled={!manageable || !site.sslEnabled || pending}
                  onClick={() =>
                    modal.confirm({
                      title: '关闭 SSL？',
                      content: '同时关闭强制 HTTPS，恢复 HTTP 访问。',
                      onOk: async () => {
                        setError('');
                        try {
                          await disableSSL(id);
                          setForce(false);
                          await finish();
                        } catch (e) {
                          setError(errorText(e));
                          throw e;
                        }
                      },
                    })
                  }
                >
                  关闭 SSL
                </Button>
              </Space>
            </Space>
          ),
        },
        ...(['free', 'folder'] as const).map((mode) => ({
          key: mode,
          label: mode === 'free' ? '免费证书' : '证书夹',
          children: (
            <CertificateWorkspace
              mode={mode}
              defaultDomains={site?.domains}
              siteId={id}
              onDeploy={async (certificateId) => {
                if (!manageable) throw new Error('当前无法修改站点配置');
                const { uncoveredDomains } = await checkSSLCertificate(
                  id,
                  certificateId,
                );
                const allowDomainMismatch = uncoveredDomains.length > 0;
                if (allowDomainMismatch) {
                  const confirmed = await new Promise<boolean>((resolve) => {
                    modal.confirm({
                      title: '证书未覆盖全部站点域名',
                      content: `未覆盖：${uncoveredDomains.join('、')}。这些域名访问 HTTPS 时会出现证书警告，是否继续部署？`,
                      okText: '继续部署',
                      cancelText: '取消',
                      onOk: () => resolve(true),
                      onCancel: () => resolve(false),
                    });
                  });
                  if (!confirmed) return false;
                }
                setPending(true);
                try {
                  await saveSSL(
                    id,
                    '',
                    '',
                    force ?? site?.forceHttps ?? false,
                    certificateId,
                    allowDomainMismatch,
                  );
                  setCert(undefined);
                  setKey('');
                  setCertName('');
                  setKeyName('');
                  await finish();
                  await certificate.refetch();
                } finally {
                  setPending(false);
                }
              }}
            />
          ),
        })),
      ]}
    />
  );
}
