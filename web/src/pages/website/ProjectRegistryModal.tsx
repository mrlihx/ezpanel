import {
  Alert,
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Skeleton,
  Typography,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { projectRegistry, saveProjectRegistry } from '../../api/website';

const pipSources = [
  { label: 'PyPI 官方源', value: 'https://pypi.org/simple' },
  { label: '清华大学', value: 'https://pypi.tuna.tsinghua.edu.cn/simple' },
  { label: '阿里云', value: 'https://mirrors.aliyun.com/pypi/simple' },
  {
    label: '中国科学技术大学',
    value: 'https://mirrors.ustc.edu.cn/pypi/simple',
  },
];

const npmSources = [
  { label: 'npm 官方源', value: 'https://registry.npmjs.org/' },
  { label: 'npmmirror', value: 'https://registry.npmmirror.com/' },
];

export default function ProjectRegistryModal({
  id,
  isNode,
  onClose,
}: {
  id: string;
  isNode: boolean;
  onClose: () => void;
}) {
  const { message } = App.useApp();
  const client = useQueryClient();
  const [form] = Form.useForm<{ source: string; url?: string }>();
  const selected = Form.useWatch('source', form);
  const sources = isNode ? npmSources : pipSources;
  const queryKey = ['project-registry', id];
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => projectRegistry(id, signal),
  });
  const save = useMutation({
    mutationFn: (url: string) => saveProjectRegistry(id, url),
    onSuccess: (data) => {
      client.setQueryData(queryKey, data);
      void client.invalidateQueries({ queryKey: ['project-service', id] });
      void message.success('项目镜像源已保存');
      onClose();
    },
  });
  const current = query.data?.url ?? '';
  const initial =
    current && !sources.some((source) => source.value === current)
      ? { source: 'custom', url: current }
      : { source: current };
  return (
    <Modal
      open
      title={isNode ? 'npm 源设置' : 'pip 源设置'}
      okText="保存"
      onOk={() => form.submit()}
      onCancel={onClose}
      confirmLoading={save.isPending}
      okButtonProps={{ disabled: !query.data }}
    >
      {query.error ? (
        <Alert
          type="error"
          message={query.error.message}
          action={<Button onClick={() => void query.refetch()}>重试</Button>}
        />
      ) : !query.data ? (
        <Skeleton active paragraph={{ rows: 1 }} />
      ) : (
        <Form
          form={form}
          layout="vertical"
          initialValues={initial}
          onFinish={({ source, url }) =>
            save.mutate(source === 'custom' ? url!.trim() : source)
          }
        >
          <Form.Item label="镜像源" name="source">
            <Select
              options={[
                { label: '使用运行环境默认源', value: '' },
                ...sources.map(({ label, value }) => ({
                  label: `${label} · ${value}`,
                  value,
                })),
                { label: '自定义源', value: 'custom' },
              ]}
            />
          </Form.Item>
          {selected === 'custom' && (
            <Form.Item
              label="镜像地址"
              name="url"
              rules={[
                { required: true, whitespace: true, message: '请输入镜像地址' },
              ]}
            >
              <Input
                placeholder={
                  isNode
                    ? 'https://registry.example.com/'
                    : 'https://pypi.example.com/simple'
                }
                maxLength={2048}
              />
            </Form.Item>
          )}
          <Typography.Text type="secondary">
            新的依赖安装任务立即使用所选源，项目容器在下次启动或重启时应用。
          </Typography.Text>
        </Form>
      )}
    </Modal>
  );
}
