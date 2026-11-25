import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Collapse,
  Form,
  Input,
  Modal,
  Segmented,
  Select,
  Skeleton,
  Space,
} from 'antd';
import { useQuery } from '@tanstack/react-query';
import {
  createComposeProject,
  validateComposeProject,
  type ComposeProject,
  type ComposeProjectInput,
} from '../../../api/composeProjects';
import { composeTemplate, composeTemplates } from '../../../api/docker';
import ApplicationSettingsForm from '../../../components/ApplicationSettingsForm';
import { LAYOUT } from '../../../styles/tokens';
import DirectoryPicker from '../../file/DirectoryPicker';
import ComposeEnvEditor from './ComposeEnvEditor';
import ComposeYaml, { composeYamlError } from './ComposeYaml';

type Mode = 'editor' | 'template' | 'external';
type Values = {
  name: string;
  workDir?: string;
  composeFile?: string;
  remark?: string;
  deploy: boolean;
  pull?: boolean;
  removeOrphans?: boolean;
  forceRecreate?: boolean;
};

const modes: { label: string; value: Mode }[] = [
  { label: '在线编辑', value: 'editor' },
  { label: '从模板创建', value: 'template' },
  { label: '导入本地路径', value: 'external' },
];

export default function ComposeCreateModal({
  initialTemplateId,
  adopt,
  importing,
  onClose,
  onTask,
}: {
  initialTemplateId?: string;
  adopt?: ComposeProject;
  importing?: boolean;
  onClose: () => void;
  onTask: (taskId: string) => void;
}) {
  const { message } = App.useApp();
  const [form] = Form.useForm<Values>();
  const [mode, setMode] = useState<Mode>(
    adopt || importing ? 'external' : initialTemplateId ? 'template' : 'editor',
  );
  const [templateId, setTemplateId] = useState(initialTemplateId);
  const [onlineYaml, setOnlineYaml] = useState('services:\n');
  const [templateYaml, setTemplateYaml] = useState<string>();
  const [env, setEnv] = useState('');
  const [variables, setVariables] = useState<Record<string, string>>({});
  const [picker, setPicker] = useState(false);
  const [busy, setBusy] = useState<'validate' | 'submit'>();
  const deploy = Form.useWatch('deploy', form);
  const templates = useQuery({
    queryKey: ['docker-compose-templates'],
    queryFn: composeTemplates,
    enabled: mode === 'template',
  });
  const template = useQuery({
    queryKey: ['compose-template', templateId],
    queryFn: () => composeTemplate(templateId!),
    enabled: mode === 'template' && !!templateId,
  });

  const yaml =
    mode === 'template'
      ? (templateYaml ?? template.data?.yaml ?? '')
      : onlineYaml;
  const local = mode === 'external';
  const templateLoading = mode === 'template' && !template.data;

  async function submit(kind: 'validate' | 'submit') {
    const values = await form.validateFields().catch(() => undefined);
    if (!values || (mode === 'template' && !template.data)) return;
    if (!local && (!yaml.trim() || composeYamlError(yaml))) {
      void message.error('请填写有效的 Compose YAML');
      return;
    }
    const entries = Object.entries(mode === 'template' ? variables : {}).map(
      ([key, value]) => {
        // Compose 把 $$ 作为字面量，保留密码中的美元符号。
        const literal = value
          .replaceAll('\\', '\\\\')
          .replaceAll('"', '\\"')
          .replaceAll('$', () => '$$');
        return `${key}="${literal}"`;
      },
    );
    const data: ComposeProjectInput = {
      ...values,
      source: local ? 'external' : 'managed',
      workDir: values.workDir?.trim() || undefined,
      composeFile: local ? values.composeFile?.trim() || undefined : undefined,
      yaml: local ? undefined : yaml,
      env: local
        ? undefined
        : [env, ...entries].filter(Boolean).join('\n') || undefined,
      templateId: mode === 'template' ? templateId : undefined,
    };
    setBusy(kind);
    try {
      if (kind === 'validate') {
        const result = await validateComposeProject(data);
        if (result.previewOnly) {
          void message.info('预检通过。创建时将按实际项目目录重新校验。');
        } else {
          void message.success('Compose 配置校验通过');
        }
      } else {
        const { taskId } = await createComposeProject(data);
        onTask(taskId);
        onClose();
      }
    } catch {
      // 请求错误由统一 API 提示展示。
    } finally {
      setBusy(undefined);
    }
  }

  return (
    <>
      <Modal
        open
        centered
        maskClosable={false}
        width={LAYOUT.toolboxWidth}
        styles={{ body: { maxHeight: '72dvh', overflow: 'auto' } }}
        title={
          adopt
            ? `纳管编排项目 · ${adopt.name}`
            : local
              ? '导入编排项目'
              : '创建编排项目'
        }
        onCancel={onClose}
        okText={adopt ? '确认纳管' : local ? '导入项目' : '创建项目'}
        onOk={() => void submit('submit')}
        okButtonProps={{ disabled: templateLoading || busy === 'validate' }}
        confirmLoading={busy === 'submit'}
        footer={(_, { OkBtn, CancelBtn }) => (
          <>
            <Button
              disabled={templateLoading || busy === 'submit'}
              loading={busy === 'validate'}
              onClick={() => void submit('validate')}
            >
              校验配置
            </Button>
            <CancelBtn />
            <OkBtn />
          </>
        )}
      >
        <ApplicationSettingsForm<Values>
          form={form}
          className="compose-form"
          initialValues={{
            name: adopt?.name,
            workDir: adopt?.workDir,
            composeFile: adopt?.composeFile,
            remark: adopt?.remark,
            deploy: !adopt && mode !== 'external',
            removeOrphans: false,
          }}
        >
          {!adopt && (
            <Form.Item label="创建方式">
              <Segmented<Mode>
                options={modes}
                value={mode}
                onChange={(value) => {
                  setMode(value);
                  form.setFieldValue('deploy', value !== 'external');
                }}
              />
            </Form.Item>
          )}
          <Form.Item
            name="name"
            label="项目名称"
            rules={[
              { required: true, message: '请输入项目名称' },
              {
                pattern: /^[a-z0-9][a-z0-9_-]{0,62}$/,
                message:
                  '以小写字母或数字开头，可使用小写字母、数字、下划线和连字符，最多 63 字符',
              },
            ]}
          >
            <Input disabled={!!adopt} placeholder="my-project" />
          </Form.Item>
          <Form.Item label="工作目录" required={local}>
            <Space.Compact block>
              <Form.Item
                noStyle
                name="workDir"
                rules={
                  local
                    ? [
                        {
                          required: true,
                          whitespace: true,
                          message: '请选择工作目录',
                        },
                      ]
                    : []
                }
              >
                <Input
                  disabled={!!adopt}
                  placeholder={
                    local ? '/opt/my-project' : '留空使用面板默认目录'
                  }
                />
              </Form.Item>
              {!adopt && <Button onClick={() => setPicker(true)}>选择</Button>}
            </Space.Compact>
          </Form.Item>
          {local && (
            <>
              <Form.Item name="composeFile" label="Compose 文件">
                <Input
                  disabled={!!adopt}
                  placeholder="留空自动识别，也可输入相对路径或绝对路径"
                />
              </Form.Item>
              <Form.Item label=" ">
                <Alert
                  type="info"
                  message="选择已有文件的目录，面板使用原路径和原文件。"
                />
              </Form.Item>
            </>
          )}
          <Form.Item name="remark" label="备注">
            <Input maxLength={256} />
          </Form.Item>
          {mode === 'template' && (
            <>
              <Form.Item label="编排模板">
                <Select
                  aria-label="选择编排模板"
                  placeholder="请选择模板"
                  value={templateId}
                  loading={templates.isFetching}
                  options={templates.data?.map(({ id, name }) => ({
                    value: id,
                    label: name,
                  }))}
                  onChange={(next) => {
                    setTemplateId(next);
                    setTemplateYaml(undefined);
                    setVariables({});
                  }}
                />
              </Form.Item>
              {templates.error && (
                <Alert type="error" message={templates.error.message} />
              )}
              {template.error && (
                <Alert type="error" message={template.error.message} />
              )}
              {templateId && template.isPending && <Skeleton active />}
              {template.data?.variables?.map((name) => (
                <Form.Item key={name} label={name}>
                  <Input.Password
                    aria-label={name}
                    autoComplete="new-password"
                    value={variables[name] ?? ''}
                    onChange={(event) =>
                      setVariables({ ...variables, [name]: event.target.value })
                    }
                  />
                </Form.Item>
              ))}
            </>
          )}
          {!local && !templateLoading && (
            <>
              <Form.Item label="编排文件">
                <ComposeYaml
                  id={`compose-create-${mode}-${templateId ?? 'new'}`}
                  value={yaml}
                  onChange={
                    mode === 'template' ? setTemplateYaml : setOnlineYaml
                  }
                />
              </Form.Item>
              <Form.Item label="环境变量">
                <ComposeEnvEditor value={env} onChange={setEnv} />
              </Form.Item>
            </>
          )}
          <Form.Item label="启动项目" name="deploy" valuePropName="checked">
            <Checkbox>创建后立即启动</Checkbox>
          </Form.Item>
          {deploy && (
            <Collapse
              size="small"
              items={[
                {
                  key: 'deploy',
                  label: '高级部署选项',
                  children: (
                    <Space wrap>
                      <Form.Item name="pull" valuePropName="checked" noStyle>
                        <Checkbox>先拉取镜像</Checkbox>
                      </Form.Item>
                      <Form.Item
                        name="removeOrphans"
                        valuePropName="checked"
                        noStyle
                      >
                        <Checkbox>移除孤儿容器</Checkbox>
                      </Form.Item>
                      <Form.Item
                        name="forceRecreate"
                        valuePropName="checked"
                        noStyle
                      >
                        <Checkbox>强制重建容器</Checkbox>
                      </Form.Item>
                    </Space>
                  ),
                },
              ]}
            />
          )}
        </ApplicationSettingsForm>
      </Modal>
      {picker && (
        <DirectoryPicker
          initialPath={form.getFieldValue('workDir') || '/'}
          onClose={() => setPicker(false)}
          onSelect={(path) => {
            form.setFieldValue('workDir', path);
            setPicker(false);
          }}
        />
      )}
    </>
  );
}
