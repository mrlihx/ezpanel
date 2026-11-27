import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import { useState } from 'react';
import { Alert, App, Button, Form, Input, Modal, Skeleton, Space } from 'antd';
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
import RsyncSenderEditor from './RsyncSenderEditor';

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

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Button
        type="primary"
        onClick={() =>
          setSender({
            id: '',
            name: '',
            source: '',
            destination: '',
            mode: 'incremental',
            bandwidthKb: 1024,
            schedule: 'interval',
            checkMinutes: 0,
          })
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
          { title: '数据源目录', dataIndex: 'source' },
          { title: '目标', dataIndex: 'destination' },
          {
            title: '同步方式',
            dataIndex: 'mode',
            render: (value) => (value === 'full' ? '完全' : '增量'),
          },
          {
            title: '限速',
            dataIndex: 'bandwidthKb',
            render: (value: number) => (value ? `${value} KB/s` : '不限速'),
          },
          {
            title: '同步周期',
            render: (_, row) =>
              row.schedule === 'realtime'
                ? '实时同步'
                : row.checkMinutes
                  ? `每隔 ${row.checkMinutes} 分钟`
                  : '定时检查已关闭',
          },
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
                        row.mode === 'full'
                          ? '完全同步会删除目标端多余文件，排除文件会保留。'
                          : '同名文件将按数据源目录更新，目标中的额外文件会保留。',
                      onOk: () => run.mutateAsync(row.id),
                    })
                  }
                >
                  执行
                </Button>
                <Button type="link" size="small" onClick={() => setSender(row)}>
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
      {sender && (
        <RsyncSenderEditor
          key={sender.id || 'new'}
          sender={sender}
          rules={data.rules}
          saving={save.isPending}
          onClose={() => setSender(undefined)}
          onSave={(row) =>
            save.mutateAsync({
              ...data,
              senders: row.id
                ? data.senders.map((item) => (item.id === row.id ? row : item))
                : [...data.senders, row],
            })
          }
        />
      )}
    </Space>
  );
}
