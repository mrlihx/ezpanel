import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  Modal,
  Segmented,
  Select,
  Skeleton,
  Space,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  rsyncWorkspaceQuery,
  runRsyncSender,
  saveRsyncWorkspace,
  type RsyncRule,
  type RsyncSender,
} from '../../api/rsync';
import ContentSizedTable from '../../components/ContentSizedTable';
import { useMessageBox } from '../../stores/messageBox';
import HostToolDirectory from './HostToolDirectory';

export default function RsyncWorkspace({
  section,
  busy,
}: {
  section: 'send' | 'rules';
  busy: boolean;
}) {
  const { message, modal } = App.useApp();
  const client = useQueryClient();
  const config = useQuery(rsyncWorkspaceQuery);
  const [sender, setSender] = useState<RsyncSender>();
  const [rule, setRule] = useState<RsyncRule>();
  const [mode, setMode] = useState<'remote' | 'local'>('remote');
  const [senderForm] = Form.useForm<RsyncSender>();
  const [ruleForm] = Form.useForm<RsyncRule>();
  const save = useMutation({
    mutationFn: saveRsyncWorkspace,
    onSuccess: (next) => {
      client.setQueryData(rsyncWorkspaceQuery.queryKey, next);
      setSender(undefined);
      setRule(undefined);
      message.success('配置已保存');
    },
  });
  const run = useMutation({
    mutationFn: runRsyncSender,
    onSuccess: ({ taskId }) => {
      useMessageBox.getState().show(taskId);
      void client.invalidateQueries({ queryKey: ['software-tasks'] });
    },
  });
  if (config.error)
    return <Alert type="error" message={config.error.message} />;
  if (!config.data) return <Skeleton active />;
  const data = config.data;

  if (section === 'rules') {
    return (
      <Space direction="vertical" style={{ width: '100%' }}>
        <Button
          type="primary"
          onClick={() => setRule({ id: '', name: '', patterns: '' })}
        >
          新增排除规则
        </Button>
        <ContentSizedTable<RsyncRule>
          dataSource={data.rules}
          rowKey="id"
          fillColumn="patterns"
          pagination={false}
          columns={[
            { title: '规则名称', dataIndex: 'name' },
            {
              title: '排除内容',
              dataIndex: 'patterns',
              render: (value: string) => (
                <span style={{ whiteSpace: 'pre-wrap' }}>{value}</span>
              ),
            },
            {
              title: '操作',
              render: (_, row) => (
                <Space>
                  <Button type="link" size="small" onClick={() => setRule(row)}>
                    编辑
                  </Button>
                  <Button
                    type="link"
                    size="small"
                    danger
                    onClick={() =>
                      modal.confirm({
                        title: `删除排除规则「${row.name}」？`,
                        onOk: () =>
                          save.mutateAsync({
                            ...data,
                            rules: data.rules.filter(
                              (item) => item.id !== row.id,
                            ),
                          }),
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
          title={rule?.id ? '编辑排除规则' : '新增排除规则'}
          open={!!rule}
          destroyOnHidden
          onCancel={() => setRule(undefined)}
          onOk={() => ruleForm.submit()}
          confirmLoading={save.isPending}
        >
          <ApplicationSettingsForm
            form={ruleForm}

            preserve={false}
            initialValues={rule}
            onFinish={(fields) => {
              const row = { ...fields, id: rule?.id ?? '' };
              save.mutate({
                ...data,
                rules: row.id
                  ? data.rules.map((item) => (item.id === row.id ? row : item))
                  : [...data.rules, row],
              });
            }}
          >
            <Form.Item
              name="name"
              label="规则名称"
              rules={[{ required: true }]}
            >
              <Input maxLength={256} />
            </Form.Item>
            <Form.Item
              name="patterns"
              label="排除内容"
              rules={[{ required: true }]}
              extra="每行一条，支持目录、文件名和通配符。"
            >
              <Input.TextArea
                rows={6}
                maxLength={8192}
                placeholder={'node_modules/\n*.log\n/cache/'}
              />
            </Form.Item>
          </ApplicationSettingsForm>
        </Modal>
      </Space>
    );
  }

  function openSender(row: RsyncSender) {
    setMode(row.destination.startsWith('/') ? 'local' : 'remote');
    setSender(row);
  }
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Button
        type="primary"
        onClick={() =>
          openSender({ id: '', name: '', source: '', destination: '' })
        }
      >
        创建发送任务
      </Button>
      <ContentSizedTable<RsyncSender>
        dataSource={data.senders}
        rowKey="id"
        fillColumn={['source', 'destination']}
        pagination={false}
        scroll={{ x: 'max-content' }}
        columns={[
          { title: '任务名称', dataIndex: 'name' },
          { title: '源目录', dataIndex: 'source' },
          { title: '目标', dataIndex: 'destination' },
          {
            title: '排除规则',
            render: (_, row) =>
              data.rules.find((item) => item.id === row.ruleId)?.name || '—',
          },
          {
            title: '操作',
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  size="small"
                  disabled={busy || run.isPending}
                  onClick={() =>
                    modal.confirm({
                      title: `执行发送任务「${row.name}」？`,
                      content:
                        '同名文件将按源目录更新，目标中的额外文件会保留。',
                      onOk: () => run.mutateAsync(row.id),
                    })
                  }
                >
                  执行
                </Button>
                <Button
                  type="link"
                  size="small"
                  onClick={() => openSender(row)}
                >
                  编辑
                </Button>
                <Button
                  type="link"
                  size="small"
                  danger
                  onClick={() =>
                    modal.confirm({
                      title: `删除发送任务「${row.name}」？`,
                      onOk: () =>
                        save.mutateAsync({
                          ...data,
                          senders: data.senders.filter(
                            (item) => item.id !== row.id,
                          ),
                        }),
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
        title={sender?.id ? '编辑发送任务' : '创建发送任务'}
        open={!!sender}
        destroyOnHidden
        onCancel={() => setSender(undefined)}
        onOk={() => senderForm.submit()}
        confirmLoading={save.isPending}
      >
        <ApplicationSettingsForm
          form={senderForm}

          preserve={false}
          initialValues={sender}
          autoComplete="off"
          onFinish={(fields) => {
            const row = { ...fields, id: sender?.id ?? '' };
            save.mutate({
              ...data,
              senders: row.id
                ? data.senders.map((item) => (item.id === row.id ? row : item))
                : [...data.senders, row],
            });
          }}
        >
          <Form.Item name="name" label="任务名称" rules={[{ required: true }]}>
            <Input maxLength={256} />
          </Form.Item>
          <Form.Item name="source" label="源目录" rules={[{ required: true }]}>
            <HostToolDirectory />
          </Form.Item>
          <Form.Item label="目标类型">
            <Segmented<'remote' | 'local'>
              value={mode}
              options={[
                { value: 'remote', label: '远程 Rsync' },
                { value: 'local', label: '本机目录' },
              ]}
              onChange={(next) => {
                setMode(next);
                senderForm.setFieldsValue({
                  destination: '',
                  username: '',
                  password: '',
                });
              }}
            />
          </Form.Item>
          <Form.Item
            name="destination"
            label={mode === 'remote' ? '接收地址' : '目标目录'}
            rules={[{ required: true }]}
          >
            {mode === 'remote' ? (
              <Input placeholder="rsync://服务器:873/模块名称" />
            ) : (
              <HostToolDirectory />
            )}
          </Form.Item>
          {mode === 'remote' && (
            <>
              <Form.Item name="username" label="接收用户名">
                <Input autoComplete="off" />
              </Form.Item>
              <Form.Item name="password" label="接收密码">
                <Input.Password
                  autoComplete="new-password"
                  placeholder={
                    sender?.id
                      ? '地址和用户名未变时，留空保留原密码'
                      : '按接收端设置填写'
                  }
                />
              </Form.Item>
            </>
          )}
          <Form.Item name="ruleId" label="排除规则">
            <Select
              allowClear
              placeholder="选择排除规则"
              options={data.rules.map((item) => ({
                value: item.id,
                label: item.name,
              }))}
            />
          </Form.Item>
        </ApplicationSettingsForm>
      </Modal>
    </Space>
  );
}
