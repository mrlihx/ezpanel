import { useEffect, useState } from 'react';
import { Alert, App, Button, Form, Input, Space } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  changePythonPackage,
  changeNodePackage,
  nodePackages,
  pythonPackages,
  savePythonRequirements,
  type PythonPackage,
  type Website,
} from '../../api/website';
import { waitForTask } from '../../api/task';
import ContentSizedTable from '../../components/ContentSizedTable';
import { useMessageBox } from '../../stores/messageBox';
import ProjectRegistryModal from './ProjectRegistryModal';

export default function ProjectDependencies({ site }: { site: Website }) {
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const [form] = Form.useForm<{ name: string; version?: string }>();
  const [taskId, setTaskId] = useState<string>();
  const [registryOpen, setRegistryOpen] = useState(false);
  const [requirementsDraft, setRequirementsDraft] = useState<string>();
  const { id, runtimeEnvironment, runtimeLaunch } = site;
  const isNode = site.projectType === 'nodejs';
  const manifest = useQuery({
    queryKey: ['node-project-manifest', id],
    queryFn: ({ signal }) => nodePackages(id, signal),
    enabled: isNode,
  });
  const packages = useQuery({
    queryKey: ['python-project-packages', id, runtimeEnvironment],
    queryFn: ({ signal }) => pythonPackages(id, signal),
    enabled: !isNode,
  });
  const requirementsFile =
    requirementsDraft ?? packages.data?.file ?? 'requirements.txt';
  const requirements = useMutation({
    mutationFn: (file: string) => savePythonRequirements(id, file),
    onSuccess: async ({ file }) => {
      const result = await packages.refetch();
      setRequirementsDraft(result.isSuccess ? undefined : file);
      void message.success('依赖文件已保存');
    },
  });
  const operation = useMutation({
    mutationFn: (data: Parameters<typeof changeNodePackage>[1]) =>
      isNode
        ? changeNodePackage(id, data)
        : changePythonPackage(id, {
            ...data,
            action: data.action === 'uninstall' ? 'uninstall' : 'install',
          }),
    onSuccess: ({ taskId }, data) => {
      setTaskId(taskId);
      if (data.action === 'install') form.resetFields();
    },
  });
  const result = useQuery({
    queryKey: ['project-package-task', taskId],
    queryFn: ({ signal }) => waitForTask(taskId!, signal),
    enabled: !!taskId,
  });
  const status = result.data?.status;
  useEffect(() => {
    if (status && status !== 'running')
      void client.invalidateQueries({
        queryKey: [
          isNode ? 'node-project-manifest' : 'python-project-packages',
          id,
        ],
      });
  }, [status, client, id, isNode]);
  const busy =
    requirements.isPending ||
    operation.isPending ||
    (!!taskId && (!status || status === 'running') && !result.isError);
  const missingManifest = isNode && !manifest.data?.exists;
  const rows = isNode
    ? [
        ...Object.entries(manifest.data?.dependencies ?? {}).map(
          ([name, version]) => ({ name, version, scope: '生产依赖' }),
        ),
        ...Object.entries(manifest.data?.devDependencies ?? {}).map(
          ([name, version]) => ({ name, version, scope: '开发依赖' }),
        ),
      ]
    : packages.data?.list;
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {!isNode && (
        <>
          <Form
            layout="inline"
            style={{ rowGap: 12 }}
            onFinish={() => requirements.mutate(requirementsFile)}
          >
            <Form.Item label="依赖文件" htmlFor="python-requirements-file">
              <Input
                id="python-requirements-file"
                value={requirementsFile}
                onChange={(event) => setRequirementsDraft(event.target.value)}
                placeholder="项目目录内的相对路径"
                disabled={busy || packages.isPending}
                maxLength={4096}
              />
            </Form.Item>
            <Form.Item>
              <Button
                htmlType="submit"
                loading={requirements.isPending}
                disabled={
                  busy ||
                  packages.isFetching ||
                  requirementsFile === packages.data?.file
                }
              >
                保存依赖文件
              </Button>
            </Form.Item>
          </Form>
          {packages.data && !packages.data.fileExists && (
            <Alert
              type="info"
              message={`未找到依赖文件 ${packages.data.file}`}
            />
          )}
        </>
      )}
      {isNode && (
        <Button
          disabled={busy || !manifest.data?.exists}
          onClick={() => operation.mutate({ action: 'sync', name: '' })}
        >
          安装项目依赖
        </Button>
      )}
      <Form
        form={form}
        layout="inline"
        style={{ rowGap: 12 }}
        onFinish={(values) =>
          operation.mutate({ ...values, action: 'install' })
        }
      >
        <Form.Item
          name="name"
          rules={[{ required: true, whitespace: true, message: '请输入包名' }]}
        >
          <Input
            aria-label="包名"
            disabled={missingManifest}
            placeholder={isNode ? '包名，例如 express' : '包名，例如 flask'}
            maxLength={isNode ? 214 : 128}
          />
        </Form.Item>
        <Form.Item name="version">
          <Input
            aria-label="版本"
            placeholder="版本（可选）"
            maxLength={64}
            disabled={missingManifest}
          />
        </Form.Item>
        <Form.Item>
          <Space wrap>
            <Button
              type="primary"
              htmlType="submit"
              loading={busy}
              disabled={missingManifest}
            >
              {isNode ? '安装模块' : '安装依赖'}
            </Button>
            <Button disabled={busy} onClick={() => setRegistryOpen(true)}>
              {isNode ? 'npm 源设置' : 'pip 源设置'}
            </Button>
          </Space>
        </Form.Item>
      </Form>
      {registryOpen && (
        <ProjectRegistryModal
          id={id}
          isNode={isNode}
          onClose={() => setRegistryOpen(false)}
        />
      )}
      {taskId && (
        <Alert
          showIcon
          type={
            result.isError || status === 'failed' || status === 'interrupted'
              ? 'error'
              : status === 'success'
                ? 'success'
                : 'info'
          }
          message={
            result.error?.message ||
            result.data?.errorMsg ||
            (status === 'success'
              ? '依赖操作已完成，运行中的项目可重启应用变更。'
              : '正在执行依赖操作')
          }
          action={
            <Button
              size="small"
              onClick={() => useMessageBox.getState().show(taskId)}
            >
              查看日志
            </Button>
          }
        />
      )}
      {(manifest.error || packages.error) && (
        <Alert
          type="error"
          message={(manifest.error || packages.error)?.message}
        />
      )}
      {isNode && manifest.data && !manifest.data.exists && (
        <Alert type="info" message="请先上传 package.json" />
      )}
      <ContentSizedTable<PythonPackage & { scope?: string }>
        fillColumn="name"
        rowKey={(row) => row.name + (row.scope ?? '')}
        size="small"
        loading={isNode ? manifest.isFetching : packages.isFetching}
        dataSource={rows}
        pagination={{ defaultPageSize: 10, showSizeChanger: true }}
        locale={{ emptyText: '暂无依赖' }}
        columns={[
          { title: '包名', dataIndex: 'name' },
          ...(isNode
            ? [
                { title: '版本声明', dataIndex: 'version' },
                { title: '类型', dataIndex: 'scope' },
              ]
            : [
                {
                  title: '项目要求',
                  dataIndex: 'requirement',
                  render: (value?: string) => (
                    <span style={{ whiteSpace: 'pre-wrap' }}>
                      {value || '—'}
                    </span>
                  ),
                },
                {
                  title: '已安装版本',
                  dataIndex: 'version',
                  render: (value: string) => value || '未安装',
                },
              ]),
          {
            title: '操作',
            key: 'actions',
            render: (_, row) => {
              const name = row.name.toLowerCase();
              const required =
                name === 'pip' ||
                (name === 'gunicorn' && runtimeLaunch === 'gunicorn') ||
                (name === 'pyuwsgi' && runtimeLaunch === 'uwsgi');
              return (
                <Space size={0}>
                  {isNode && (
                    <Button
                      type="link"
                      disabled={busy}
                      onClick={() =>
                        modal.confirm({
                          title: `更新 ${row.name}？`,
                          content:
                            '将安装软件仓库的最新版本，并更新项目的版本声明和锁文件。',
                          onOk: async () => {
                            await operation.mutateAsync({
                              action: 'update',
                              name: row.name,
                            });
                          },
                        })
                      }
                    >
                      更新
                    </Button>
                  )}
                  <Button
                    type="link"
                    danger
                    disabled={busy || (!isNode && (required || !row.version))}
                    onClick={() =>
                      modal.confirm({
                        title: `卸载 ${row.name}？`,
                        onOk: async () => {
                          await operation.mutateAsync({
                            action: 'uninstall',
                            name: row.name,
                          });
                        },
                      })
                    }
                  >
                    卸载
                  </Button>
                </Space>
              );
            },
          },
        ]}
      />
    </Space>
  );
}
