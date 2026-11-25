import { useRef, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Dropdown,
  Flex,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Tag,
} from 'antd';
import { Download, Plus, Upload } from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  composeTemplate,
  composeTemplates,
  deleteComposeTemplate,
  exportComposeTemplate,
  importComposeTemplate,
  saveComposeProjectTemplate,
  saveComposeTemplate,
  type ComposeTemplate,
  type ComposeTemplateCategory,
  type ComposeTemplateFormat,
  type ComposeTemplateInput,
} from '../../api/docker';
import ContentSizedTable from '../../components/ContentSizedTable';
import { LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';

const templateKey = ['docker-compose-templates'];
const categories: Record<ComposeTemplateCategory, string> = {
  database: '数据库',
  website: '建站',
  ai: 'AI',
  tool: '工具',
  custom: '自定义',
};
const categoryOptions = Object.entries(categories).map(([value, label]) => ({
  value,
  label,
}));
const example = `services:
  web:
    image: nginx:alpine
    ports:
      - "127.0.0.1:8080:80"
`;

export default function ComposeTemplates({
  online,
  active,
  onCreateFromTemplate,
}: {
  online: boolean;
  active: boolean;
  onCreateFromTemplate: (id: string) => void;
}) {
  const { modal, message } = App.useApp();
  const queryClient = useQueryClient();
  const templates = useQuery({
    queryKey: templateKey,
    queryFn: composeTemplates,
    enabled: active,
  });
  const importInput = useRef<HTMLInputElement>(null);
  const [keyword, setKeyword] = useState('');
  const [category, setCategory] = useState<ComposeTemplateCategory>();
  const [editor, setEditor] = useState<{
    id?: string;
    variables?: string[];
    envKeys?: string[];
  }>();
  const [editForm] = Form.useForm<ComposeTemplateInput>();
  const edit = useMutation({
    mutationFn: composeTemplate,
    onSuccess: ({ id, name, remark, category, yaml, variables, envKeys }) => {
      editForm.setFieldsValue({ name, remark, category, yaml });
      setEditor({ id, variables, envKeys });
    },
  });
  const save = useMutation({
    mutationFn: (values: ComposeTemplateInput) =>
      saveComposeTemplate({ ...values, envKeys: editor?.envKeys }, editor?.id),
    onSuccess: ({ id }) => {
      setEditor(undefined);
      void queryClient.invalidateQueries({ queryKey: templateKey });
      void queryClient.invalidateQueries({
        queryKey: ['compose-template', id],
      });
      void message.success('模板已保存');
    },
  });
  const importTemplate = useMutation({
    mutationFn: async ({
      file,
      format,
    }: {
      file: File;
      format: ComposeTemplateFormat;
    }) =>
      importComposeTemplate({
        format,
        name: file.name.replace(/\.[^.]+$/, ''),
        content: await file.text(),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: templateKey });
      void message.success('模板已导入');
    },
  });
  const exportTemplate = useMutation({
    mutationFn: ({
      id,
      format,
    }: {
      id: string;
      format: ComposeTemplateFormat;
    }) => exportComposeTemplate(id, format),
    onSuccess: ({ filename, content }, { format }) => {
      const url = URL.createObjectURL(
        new Blob([content], {
          type: format === 'json' ? 'application/json' : 'application/yaml',
        }),
      );
      const link = document.createElement('a');
      link.href = url;
      link.download = filename;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    },
  });
  const search = keyword.trim().toLowerCase();
  const rows = (templates.data ?? []).filter(
    (item) =>
      (!category || category === item.category) &&
      (!search ||
        [item.name, item.remark, categories[item.category]].some((value) =>
          value.toLowerCase().includes(search),
        )),
  );

  return (
    <Flex vertical gap={SPACING.lg}>
      {templates.error && (
        <Alert type="error" showIcon message={templates.error.message} />
      )}
      <Flex wrap="wrap" gap={SPACING.sm} justify="space-between">
        <Flex wrap="wrap" gap={SPACING.sm}>
          <Button
            type="primary"
            icon={<Plus size={16} />}
            disabled={edit.isPending}
            onClick={() => {
              editForm.setFieldsValue({
                name: '',
                remark: '',
                category: 'custom',
                yaml: example,
              });
              setEditor({});
            }}
          >
            添加模板
          </Button>
          <Button
            icon={<Upload size={16} />}
            loading={importTemplate.isPending}
            onClick={() => importInput.current?.click()}
          >
            导入模板
          </Button>
          <input
            ref={importInput}
            type="file"
            hidden
            accept=".json,.yaml,.yml"
            onChange={(event) => {
              const file = event.target.files?.[0];
              event.target.value = '';
              if (!file) return;
              const extension = file.name.split('.').at(-1)?.toLowerCase();
              if (extension === 'json') {
                importTemplate.mutate({ file, format: 'json' });
              } else if (extension === 'yaml' || extension === 'yml') {
                importTemplate.mutate({ file, format: 'yaml' });
              } else {
                void message.error('请选择 JSON 或 YAML 模板文件');
              }
            }}
          />
        </Flex>
        <Flex wrap="wrap" gap={SPACING.sm}>
          <Input
            allowClear
            aria-label="搜索模板"
            placeholder="搜索名称、分类或备注"
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            style={{ width: 210 }}
          />
          <Select<ComposeTemplateCategory>
            allowClear
            aria-label="模板分类"
            placeholder="全部分类"
            options={categoryOptions}
            value={category}
            onChange={setCategory}
            style={{ minWidth: 120 }}
          />
        </Flex>
      </Flex>
      <ContentSizedTable<ComposeTemplate>
        fillColumn="remark"
        rowKey="id"
        loading={templates.isLoading}
        dataSource={rows}
        locale={{
          emptyText:
            search || category ? '没有匹配的模板' : '暂无 Compose 编排模板',
        }}
        columns={[
          { title: '模板名称', dataIndex: 'name' },
          {
            title: '分类',
            dataIndex: 'category',
            render: (value: ComposeTemplateCategory) => (
              <Tag>{categories[value]}</Tag>
            ),
          },
          {
            title: '备注',
            dataIndex: 'remark',
            render: (value: string) => value || '—',
          },
          {
            title: '操作',
            key: 'actions',
            render: (_, row) => (
              <Space size={SPACING.sm}>
                <Button
                  type="link"
                  disabled={!online}
                  onClick={() => onCreateFromTemplate(row.id)}
                >
                  部署
                </Button>
                <Button
                  type="link"
                  loading={edit.isPending && edit.variables === row.id}
                  disabled={edit.isPending}
                  onClick={() => edit.mutate(row.id)}
                >
                  编辑
                </Button>
                <Dropdown
                  menu={{
                    items: [
                      { key: 'json', label: '导出 JSON' },
                      { key: 'yaml', label: '导出 YAML' },
                    ],
                    onClick: ({ key }) =>
                      exportTemplate.mutate({
                        id: row.id,
                        format: key === 'json' ? 'json' : 'yaml',
                      }),
                  }}
                  trigger={['click']}
                >
                  <Button
                    type="link"
                    aria-label={`导出模板 ${row.name}`}
                    icon={<Download size={16} />}
                    loading={
                      exportTemplate.isPending &&
                      exportTemplate.variables?.id === row.id
                    }
                  />
                </Dropdown>
                <Button
                  type="link"
                  danger
                  onClick={() =>
                    modal.confirm({
                      title: `删除模板「${row.name}」？`,
                      content: '已部署的 Compose 项目会继续运行。',
                      okButtonProps: { danger: true },
                      onOk: async () => {
                        await deleteComposeTemplate(row.id);
                        await queryClient.invalidateQueries({
                          queryKey: templateKey,
                        });
                      },
                    })
                  }
                >
                  删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        open={!!editor}
        forceRender
        centered
        title={editor?.id ? '编辑 Compose 模板' : '添加 Compose 模板'}
        width={LAYOUT.databaseModalWidth}
        onCancel={() => setEditor(undefined)}
        onOk={() => editForm.submit()}
        confirmLoading={save.isPending}
        maskClosable={false}
        okText="保存"
      >
        <Form
          form={editForm}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1, style: { minWidth: 0 } }}
          colon={false}
          onFinish={(values) => save.mutate(values)}
        >
          <Form.Item
            label="模板名称"
            name="name"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item label="分类" name="category">
            <Select options={categoryOptions} />
          </Form.Item>
          <Form.Item label="备注" name="remark">
            <Input maxLength={512} />
          </Form.Item>
          <Form.Item
            label="Compose YAML"
            name="yaml"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input.TextArea
              autoSize={{ minRows: 10, maxRows: 16 }}
              spellCheck={false}
              style={{ fontFamily: MONO_FONT }}
            />
          </Form.Item>
          {!!editor?.variables?.length && (
            <Form.Item label="已保存的变量">
              <Space wrap size={[0, SPACING.xs]}>
                {editor.variables.map((name) => (
                  <Tag key={name}>{name}</Tag>
                ))}
              </Space>
            </Form.Item>
          )}
        </Form>
      </Modal>
    </Flex>
  );
}

export function ComposeSaveTemplateModal({
  project,
  onClose,
}: {
  project: { name: string; remark?: string } | undefined;
  onClose: () => void;
}) {
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [form] =
    Form.useForm<Pick<ComposeTemplate, 'name' | 'remark' | 'category'>>();
  const save = useMutation({
    mutationFn: ({
      name,
      data,
    }: {
      name: string;
      data: Pick<ComposeTemplate, 'name' | 'remark' | 'category'>;
    }) => saveComposeProjectTemplate(name, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: templateKey });
      void message.success('项目已另存为模板');
      onClose();
    },
  });

  return (
    <Modal
      open={!!project}
      destroyOnHidden
      centered
      title="另存为编排模板"
      width={LAYOUT.databaseModalWidth}
      onCancel={onClose}
      onOk={() => form.submit()}
      confirmLoading={save.isPending}
      maskClosable={false}
      okText="保存模板"
    >
      <Form
        form={form}
        preserve={false}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: 1, style: { minWidth: 0 } }}
        colon={false}
        initialValues={{
          name: project?.name,
          remark: project?.remark ?? '',
          category: 'custom',
        }}
        onFinish={(data) => {
          if (project) save.mutate({ name: project.name, data });
        }}
      >
        <Form.Item
          label="模板名称"
          name="name"
          rules={[{ required: true, whitespace: true }]}
        >
          <Input maxLength={64} />
        </Form.Item>
        <Form.Item label="分类" name="category">
          <Select options={categoryOptions} />
        </Form.Item>
        <Form.Item label="备注" name="remark">
          <Input maxLength={512} />
        </Form.Item>
        <Alert type="info" showIcon message="模板只保留 .env 中的变量名。" />
      </Form>
    </Modal>
  );
}
