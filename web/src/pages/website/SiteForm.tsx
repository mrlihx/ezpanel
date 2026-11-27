import { useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Form,
  Input,
  Select,
  Space,
  type FormInstance,
} from 'antd';
import { FolderOpenOutlined, ReloadOutlined } from '@ant-design/icons';
import DirectoryPicker from '../file/DirectoryPicker';
import { useQuery } from '@tanstack/react-query';
import { charsetList, servers } from '../../api/database';
import { phpVersions, type SiteInput } from '../../api/website';
import { useAuth } from '../../stores/auth';
import { LAYOUT } from '../../styles/tokens';
import './SiteForm.css';
import RuntimeFields from './RuntimeFields';

export type SiteDatabaseInput = {
  serverId: string;
  name: string;
  charset: string;
  username: string;
  password: string;
};
export type SiteFormValues = Omit<SiteInput, 'domains' | 'proxyRoutes'> & {
  domains: string;
  proxyTarget?: string;
  databaseMode?: 'none' | 'create';
  database?: SiteDatabaseInput;
};

function firstBinding(domains: string) {
  const first =
    domains
      .split(/\r?\n/)
      .find((line) => line.trim())
      ?.trim() ?? '';
  const ipv6 = /^\[([^\]]+)\](?::(\d+))?$/.exec(first);
  const [name, port, ...rest] = first.split(':');
  const host = (ipv6?.[1] ?? (rest.length ? first : name))
    .toLowerCase()
    .replace(/^\*\./, '')
    .replace(/\.$/, '');
  return { host, port: Number(ipv6 ? ipv6[2] : rest.length ? '' : port) || 80 };
}

function randomPassword() {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';
  return (
    'Aa9!' +
    Array.from(
      crypto.getRandomValues(new Uint8Array(12)),
      (value) => alphabet[value % alphabet.length],
    ).join('')
  );
}

export default function SiteForm({
  form,
  onFinish,
  projectType = 'php',
}: {
  form: FormInstance<SiteFormValues>;
  onFinish: (values: SiteInput, database?: SiteDatabaseInput) => void;
  projectType?: SiteInput['projectType'];
}) {
  const siteRoot = useAuth((s) => s.user?.siteRoot) || '/www/wwwroot';
  const type = projectType === 'php' ? 'static' : 'proxy';
  const runtime =
    projectType === 'php' || projectType === 'proxy' ? undefined : projectType;
  const phpVersion = Form.useWatch('phpVersion', form) ?? '';
  const databaseMode = Form.useWatch('databaseMode', form);
  const serverId = Form.useWatch(['database', 'serverId'], form);
  const [directoryOpen, setDirectoryOpen] = useState(false);
  const php = useQuery({
    queryKey: ['website-php-versions'],
    queryFn: phpVersions,
    enabled: type === 'static',
  });
  const dbServers = useQuery({
    queryKey: ['db-servers'],
    queryFn: servers,
    enabled: databaseMode === 'create',
  });
  useEffect(() => {
    if (databaseMode === 'create' && !serverId && dbServers.data?.length) {
      form.setFieldValue(
        ['database', 'serverId'],
        (dbServers.data.find((item) => item.local) ?? dbServers.data[0]).id,
      );
    }
  }, [databaseMode, serverId, dbServers.data, form]);
  const charsets = useQuery({
    queryKey: ['db-charsets', serverId],
    queryFn: () => charsetList(serverId!),
    enabled: databaseMode === 'create' && !!serverId,
  });
  const installedPhp = php.data?.list.filter((item) => item.installed) ?? [];
  return (
    <>
      <Form
        form={form}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1, style: { minWidth: 0 } }}
        labelWrap
        className="site-form"
        colon={false}
        scrollToFirstError={{ block: 'nearest' }}
        initialValues={{
          type,
          projectType,
          domains: '',
          root: '',
          proxyTarget: '',
          phpVersion: '',
          remark: '',
          databaseMode: 'none',
        }}
        onFinish={(values) => {
          const {
            domains,
            proxyTarget,
            databaseMode: mode,
            database,
            ...site
          } = values;
          onFinish(
            {
              ...site,
              name: runtime ? site.name : '',
              remark: '',
              type,
              projectType,
              domains: domains
                .split(/\r?\n/)
                .map((line) => line.trim())
                .filter(Boolean),
              root: type === 'static' || runtime ? site.root : '',
              proxyRoutes:
                projectType === 'proxy'
                  ? [
                      {
                        name: '默认代理',
                        path: '/',
                        target: proxyTarget ?? '',
                        host: '$host',
                        enabled: true,
                      },
                    ]
                  : [],
              phpVersion: type === 'static' ? site.phpVersion : '',
            },
            type === 'static' && mode === 'create' && database
              ? { ...database, username: database.name.slice(0, 32) }
              : undefined,
          );
        }}
      >
        <Form.Item name="type" hidden>
          <Input />
        </Form.Item>
        <Form.Item name="projectType" hidden>
          <Input />
        </Form.Item>
        {runtime && (
          <Form.Item
            name="name"
            label="项目名称"
            rules={[
              { required: true, whitespace: true, message: '请输入项目名称' },
            ]}
          >
            <Input maxLength={255} />
          </Form.Item>
        )}
        <Form.Item
          name="domains"
          label="域名"
          rules={[
            {
              required: !runtime,
              whitespace: true,
              message: '请输入域名或 IP，每行一个',
            },
          ]}
          extra="每行一个，支持域名、IPv4、[IPv6]及端口，如 example.com:8080、[2001:db8::1]:8080；默认 HTTP 80。IP 按请求 Host 匹配，不用于指定监听网卡。"
        >
          <Input.TextArea
            autoSize={{ minRows: 4, maxRows: 8 }}
            placeholder={
              'example.com\nwww.example.com\n192.0.2.1:8080\n[2001:db8::1]:8080'
            }
            onChange={(event) => {
              const { host, port } = firstBinding(event.target.value);
              if (
                databaseMode === 'create' &&
                !form.isFieldTouched(['database', 'name'])
              ) {
                form.setFieldValue(
                  ['database', 'name'],
                  host.replace(/\./g, '_'),
                );
              }
              if (!form.isFieldTouched('root')) {
                const suffix = `_${port}`;
                form.setFieldValue(
                  'root',
                  host
                    ? `${siteRoot}/${host.replace(/:/g, '_').slice(0, 255 - suffix.length)}${suffix}`
                    : '',
                );
              }
            }}
          />
        </Form.Item>
        {projectType === 'proxy' ? (
          <Form.Item
            name="proxyTarget"
            label="代理目标"
            rules={[{ required: true }]}
            extra="http/https 地址，可含端口和路径，不接受账号、查询参数或片段。"
          >
            <Input placeholder="http://127.0.0.1:3000" />
          </Form.Item>
        ) : (
          <>
            <Form.Item
              label={runtime ? '项目目录' : '根目录'}
              required
              extra={
                runtime
                  ? '目录不存在时会自动创建。上传代码后，在运行设置中保存启动配置。'
                  : '修改目录不会移动、复制或删除旧目录。'
              }
            >
              <Space.Compact style={{ width: '100%' }}>
                <Form.Item
                  name="root"
                  noStyle
                  rules={[{ required: true, message: '请输入站点根目录' }]}
                >
                  <Input placeholder={`${siteRoot}/example.com`} />
                </Form.Item>
                <Button
                  aria-label="选择根目录"
                  icon={<FolderOpenOutlined />}
                  onClick={() => setDirectoryOpen(true)}
                />
              </Space.Compact>
            </Form.Item>
            {runtime && <RuntimeFields runtime={runtime} />}
            {!runtime && (
              <>
                <Form.Item name="databaseMode" label="数据库">
                  <Select
                    options={[
                      { value: 'none', label: '不创建' },
                      { value: 'create', label: 'MySQL' },
                    ]}
                    onChange={(value) => {
                      form.setFieldValue(
                        'database',
                        value === 'create'
                          ? {
                              name: firstBinding(
                                form.getFieldValue('domains') ?? '',
                              ).host.replace(/\./g, '_'),
                              password: randomPassword(),
                              charset: 'utf8mb4',
                            }
                          : undefined,
                      );
                    }}
                  />
                </Form.Item>
                {databaseMode === 'create' && (
                  <>
                    <Form.Item
                      name={['database', 'serverId']}
                      label="数据库服务器"
                      rules={[
                        {
                          required: true,
                          message: '请选择已登记的数据库服务器',
                        },
                      ]}
                    >
                      <Select
                        loading={dbServers.isFetching}
                        placeholder="选择已登记服务器"
                        options={dbServers.data?.map((server) => ({
                          value: server.id,
                          label: server.name,
                        }))}
                      />
                    </Form.Item>
                    <Form.Item
                      name={['database', 'name']}
                      label="数据库名"
                      rules={[
                        {
                          required: true,
                          pattern: /^[a-zA-Z0-9_]{1,64}$/,
                          message:
                            '请输入1至64位字母、数字或下划线；域名含其它字符时请修改数据库名',
                        },
                      ]}
                      extra="自动创建对应账号（最多32位）。"
                    >
                      <Input placeholder="字母、数字或下划线" maxLength={64} />
                    </Form.Item>
                    <Form.Item
                      name={['database', 'charset']}
                      label="字符集"
                      rules={[{ required: true, message: '请选择字符集' }]}
                    >
                      <Select
                        disabled={!serverId}
                        loading={charsets.isFetching}
                        options={charsets.data?.list.map((item) => ({
                          value: item.charset,
                        }))}
                      />
                    </Form.Item>
                    <Form.Item
                      name={['database', 'password']}
                      label="数据库密码"
                      rules={[
                        { required: true, message: '请输入数据库密码' },
                        { max: 1024 },
                      ]}
                    >
                      <Input
                        autoComplete="new-password"
                        addonAfter={
                          <Button
                            type="text"
                            size="small"
                            icon={<ReloadOutlined />}
                            aria-label="生成随机数据库密码"
                            onClick={() =>
                              form.setFieldValue(
                                ['database', 'password'],
                                randomPassword(),
                              )
                            }
                          />
                        }
                      />
                    </Form.Item>
                    {(dbServers.error || charsets.error) && (
                      <Form.Item label=" ">
                        <Alert
                          type="error"
                          message="数据库选项读取失败"
                          action={
                            <Button
                              onClick={() => {
                                void dbServers.refetch();
                                if (serverId) void charsets.refetch();
                              }}
                            >
                              重试
                            </Button>
                          }
                        />
                      </Form.Item>
                    )}
                  </>
                )}
              </>
            )}
            {!runtime && (
              <>
                <Form.Item
                  name="phpVersion"
                  label="PHP 版本"
                  extra="选择已安装且运行中的 PHP；纯静态站点不执行 PHP。"
                >
                  <Select
                    loading={php.isFetching}
                    options={[
                      { value: '', label: '纯静态' },
                      ...installedPhp.map((item) => ({
                        value: item.version,
                        label: `PHP ${item.version}${item.running ? '' : '（已停止）'}`,
                        disabled: !item.running,
                      })),
                      ...(phpVersion &&
                      !installedPhp.some((item) => item.version === phpVersion)
                        ? [
                            {
                              value: phpVersion,
                              label: `PHP ${phpVersion}（不可用）`,
                              disabled: true,
                            },
                          ]
                        : []),
                    ]}
                  />
                </Form.Item>
                {php.error && (
                  <Form.Item label=" ">
                    <Alert
                      type="error"
                      message="PHP 版本读取失败"
                      action={
                        <Button onClick={() => void php.refetch()}>重试</Button>
                      }
                    />
                  </Form.Item>
                )}
              </>
            )}
          </>
        )}
      </Form>
      {directoryOpen && (
        <DirectoryPicker
          initialPath={siteRoot}
          onClose={() => setDirectoryOpen(false)}
          onSelect={(path) => {
            form.setFields([
              { name: 'root', value: path, touched: true, errors: [] },
            ]);
            setDirectoryOpen(false);
          }}
        />
      )}
    </>
  );
}
