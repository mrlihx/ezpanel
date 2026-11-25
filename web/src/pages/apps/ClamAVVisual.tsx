import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Switch,
  Tag,
  Typography,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { request } from '../../api/client';
import type { TaskSummary } from '../../api/task';
import ContentSizedTable from '../../components/ContentSizedTable';
import { useMessageBox } from '../../stores/messageBox';
import { formatTime } from '../../utils/format';
import HostToolDirectory from './HostToolDirectory';

type Target = {
  id: string;
  name: string;
  path: string;
  policy: 'report' | 'quarantine';
  remark: string;
  scheduled: boolean;
};
type Config = {
  mode: 'normal' | 'quick';
  auto: boolean;
  hour: number;
  minute: number;
  targets: Target[];
};
type Visual = {
  settings: Config;
  status: { version: string; updaterRunning: boolean };
  siteRoot: string;
};
type Scan = TaskSummary & { infected?: boolean; quarantined?: number };
type Quarantine = { id: string; path: string; source: string; time: string };

const endpoint = '/api/software/clamav';
const queryKey = ['clamav-visual'];
const policyLabels = { report: '仅报告', quarantine: '移入隔离目录' };
const statusLabels = {
  running: '扫描中',
  success: '已完成',
  failed: '失败',
  interrupted: '已中断',
};

export function ClamAVSettings({ tab }: { tab: 'service' | 'scan' }) {
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const visual = useQuery({
    queryKey,
    queryFn: () => request<Visual>(`${endpoint}/visual`),
  });
  const [mode, setMode] = useState<Config['mode']>();
  const [target, setTarget] = useState<Partial<Target>>();
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [targetForm] = Form.useForm<Target>();
  const [scheduleForm] = Form.useForm<Config>();
  const save = useMutation({
    mutationFn: (data: Config) =>
      request<Visual>(`${endpoint}/visual`, { method: 'PUT', data }),
    onSuccess: (next) => {
      client.setQueryData(queryKey, next);
      void client.invalidateQueries({ queryKey: ['software-catalog'] });
      setMode(undefined);
      setTarget(undefined);
      setScheduleOpen(false);
      message.success('ClamAV 配置已保存');
    },
  });
  const run = useMutation({
    mutationFn: ({
      action,
      id,
    }: {
      action: 'scan' | 'signatures';
      id?: string;
    }) =>
      request<{ taskId: string }>(`${endpoint}/${action}`, {
        method: 'POST',
        params: { id },
      }),
    onSuccess: ({ taskId }) => {
      useMessageBox.getState().show(taskId);
      void client.invalidateQueries({ queryKey: ['clamav-history'] });
    },
  });
  if (visual.error)
    return <Alert type="error" message={visual.error.message} />;
  if (!visual.data) return null;
  const { settings, status, siteRoot } = visual.data;
  if (tab === 'service') {
    return (
      <ApplicationSettingsForm>
        <Form.Item label="病毒库">
          <Space wrap>
            <Tag color={status.version ? 'success' : 'default'}>
              {status.version || '未加载'}
            </Tag>
            <Typography.Text type="secondary">
              自动更新服务{status.updaterRunning ? '运行中' : '已停止'}
            </Typography.Text>
            <Button
              loading={run.isPending}
              onClick={() => run.mutate({ action: 'signatures' })}
            >
              检查更新
            </Button>
          </Space>
        </Form.Item>
        <Form.Item
          label="扫描策略"
          extra="普通扫描按需载入病毒库。快速扫描使用常驻服务。"
        >
          <Space wrap>
            <Select<Config['mode']>
              aria-label="扫描策略"
              value={mode ?? settings.mode}
              onChange={setMode}
              options={[
                { value: 'normal', label: '普通扫描' },
                { value: 'quick', label: '快速扫描' },
              ]}
            />
            <Button
              disabled={!mode || mode === settings.mode}
              loading={save.isPending}
              onClick={() => mode && save.mutate({ ...settings, mode })}
            >
              保存
            </Button>
          </Space>
        </Form.Item>
      </ApplicationSettingsForm>
    );
  }
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Space wrap>
        <Button
          type="primary"
          onClick={() =>
            setTarget({ path: siteRoot, policy: 'report', scheduled: false })
          }
        >
          添加检测目录
        </Button>
        <Button onClick={() => setScheduleOpen(true)}>自动检测配置</Button>
        <Button
          loading={run.isPending}
          onClick={() => run.mutate({ action: 'scan' })}
        >
          扫描默认建站目录
        </Button>
      </Space>
      <ContentSizedTable<Target>
        fillColumn="path"
        rowKey="id"
        dataSource={settings.targets}
        scroll={{ x: 'max-content' }}
        columns={[
          { title: '名称', dataIndex: 'name' },
          { title: '检测目录', dataIndex: 'path' },
          {
            title: '感染文件策略',
            render: (_, row) => policyLabels[row.policy],
          },
          { title: '备注', dataIndex: 'remark' },
          {
            title: '定时扫描',
            render: (_, row) =>
              row.scheduled ? <Tag color="success">启用</Tag> : '关闭',
          },
          {
            title: '操作',
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  size="small"
                  disabled={run.isPending}
                  onClick={() => {
                    if (row.policy === 'quarantine')
                      modal.confirm({
                        title: `扫描 ${row.name} 并隔离感染文件？`,
                        onOk: () =>
                          run.mutateAsync({ action: 'scan', id: row.id }),
                      });
                    else run.mutate({ action: 'scan', id: row.id });
                  }}
                >
                  扫描
                </Button>
                <Button type="link" size="small" onClick={() => setTarget(row)}>
                  编辑
                </Button>
                <Button
                  type="link"
                  size="small"
                  danger
                  onClick={() =>
                    modal.confirm({
                      title: `删除检测目录配置 ${row.name}？`,
                      onOk: () =>
                        save.mutateAsync({
                          ...settings,
                          targets: settings.targets.filter(
                            (item) => item.id !== row.id,
                          ),
                        }),
                    })
                  }
                >
                  删除配置
                </Button>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title={target?.id ? '编辑检测目录' : '添加检测目录'}
        open={!!target}
        destroyOnHidden
        onCancel={() => setTarget(undefined)}
        onOk={() => targetForm.submit()}
        confirmLoading={save.isPending}
      >
        <ApplicationSettingsForm
          form={targetForm}

          preserve={false}
          initialValues={target}
          onFinish={(fields) => {
            const row = { ...fields, id: target?.id ?? '' };
            save.mutate({
              ...settings,
              targets: row.id
                ? settings.targets.map((item) =>
                    item.id === row.id ? row : item,
                  )
                : [...settings.targets, row],
            });
          }}
        >
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input maxLength={120} />
          </Form.Item>
          <Form.Item name="path" label="检测目录" rules={[{ required: true }]}>
            <HostToolDirectory />
          </Form.Item>
          <Form.Item
            name="policy"
            label="感染文件策略"
            rules={[{ required: true }]}
          >
            <Select
              options={Object.entries(policyLabels).map(([value, label]) => ({
                value,
                label,
              }))}
            />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input maxLength={500} />
          </Form.Item>
          <Form.Item name="scheduled" label="定时扫描" valuePropName="checked">
            <Switch />
          </Form.Item>
        </ApplicationSettingsForm>
      </Modal>
      <Modal
        title="自动检测配置"
        open={scheduleOpen}
        destroyOnHidden
        onCancel={() => setScheduleOpen(false)}
        onOk={() => scheduleForm.submit()}
        confirmLoading={save.isPending}
      >
        <ApplicationSettingsForm
          form={scheduleForm}

          preserve={false}
          initialValues={settings}
          onFinish={(values) => save.mutate({ ...settings, ...values })}
        >
          <Form.Item name="auto" label="自动检测开关" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="hour" label="扫描小时" rules={[{ required: true }]}>
            <InputNumber min={0} max={23} precision={0} />
          </Form.Item>
          <Form.Item
            name="minute"
            label="扫描分钟"
            rules={[{ required: true }]}
            extra="按服务器时间每日扫描开启了定时扫描的目录。"
          >
            <InputNumber min={0} max={59} precision={0} />
          </Form.Item>
        </ApplicationSettingsForm>
      </Modal>
    </Space>
  );
}

export function ClamAVHistory() {
  const [page, setPage] = useState(1);
  const query = useQuery({
    queryKey: ['clamav-history', page],
    queryFn: () =>
      request<{ list: Scan[]; total: number }>(`${endpoint}/history`, {
        params: { page, page_size: 20 },
      }),
  });
  return (
    <Space direction="vertical" style={{ width: '100%' }}>
      <Button loading={query.isFetching} onClick={() => void query.refetch()}>
        刷新
      </Button>
      {query.error && <Alert type="error" message={query.error.message} />}
      <ContentSizedTable<Scan>
        fillColumn="target"
        rowKey="taskId"
        dataSource={query.data?.list}
        loading={query.isPending}
        scroll={{ x: 'max-content' }}
        pagination={{
          current: page,
          pageSize: 20,
          total: query.data?.total,
          onChange: setPage,
          showSizeChanger: false,
        }}
        columns={[
          { title: '扫描目标', dataIndex: 'target' },
          { title: '开始时间', render: (_, row) => formatTime(row.startedAt) },
          { title: '结果', render: (_, row) => statusLabels[row.status] },
          {
            title: '发现感染',
            dataIndex: 'infected',
            render: (infected?: boolean) =>
              infected === undefined ? '—' : infected ? '是' : '否',
          },
          { title: '已隔离', dataIndex: 'quarantined' },
          {
            title: '操作',
            render: (_, row) => (
              <Button
                type="link"
                size="small"
                onClick={() => useMessageBox.getState().show(row.taskId)}
              >
                日志
              </Button>
            ),
          },
        ]}
      />
    </Space>
  );
}

export function ClamAVQuarantine() {
  const { modal, message } = App.useApp();
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<string[]>([]);
  const query = useQuery({
    queryKey: ['clamav-quarantine', page],
    queryFn: () =>
      request<{ list: Quarantine[]; total: number }>(`${endpoint}/quarantine`, {
        params: { page, page_size: 20 },
      }),
  });
  function change(action: 'restore' | 'delete', ids: string[]) {
    modal.confirm({
      title:
        action === 'restore'
          ? `还原 ${ids.length} 个隔离文件？`
          : `永久删除 ${ids.length} 个隔离文件？`,
      content:
        action === 'restore'
          ? '文件将移回原路径，可能仍包含恶意内容。'
          : undefined,
      okButtonProps: { danger: true },
      onOk: async () => {
        const { failed } = await request<{ failed: Record<string, string> }>(
          `${endpoint}/quarantine`,
          { method: 'POST', data: { action, ids } },
        );
        const errors = Object.values(failed);
        if (errors.length)
          message.error(`${errors.length} 项处理失败：${errors[0]}`);
        else message.success('隔离文件已处理');
        setSelected([]);
        if (
          ids.length === query.data?.list.length &&
          !errors.length &&
          page > 1
        )
          setPage(page - 1);
        else await query.refetch();
      },
    });
  }
  return (
    <Space direction="vertical" style={{ width: '100%' }}>
      <Space wrap>
        <Button loading={query.isFetching} onClick={() => void query.refetch()}>
          刷新
        </Button>
        <Button
          disabled={!selected.length}
          onClick={() => change('restore', selected)}
        >
          还原所选
        </Button>
        <Button
          danger
          disabled={!selected.length}
          onClick={() => change('delete', selected)}
        >
          删除所选
        </Button>
      </Space>
      {query.error && <Alert type="error" message={query.error.message} />}
      <ContentSizedTable<Quarantine>
        fillColumn="path"
        rowKey="id"
        loading={query.isPending}
        dataSource={query.data?.list}
        scroll={{ x: 'max-content' }}
        rowSelection={{
          selectedRowKeys: selected,
          onChange: (keys) => setSelected(keys.map(String)),
        }}
        pagination={{
          current: page,
          pageSize: 20,
          total: query.data?.total,
          onChange: (next) => {
            setPage(next);
            setSelected([]);
          },
          showSizeChanger: false,
        }}
        columns={[
          { title: '原文件路径', dataIndex: 'path' },
          { title: '扫描目录', dataIndex: 'source' },
          { title: '隔离时间', render: (_, row) => formatTime(row.time) },
          {
            title: '操作',
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  size="small"
                  onClick={() => change('restore', [row.id])}
                >
                  还原
                </Button>
                <Button
                  type="link"
                  size="small"
                  danger
                  onClick={() => change('delete', [row.id])}
                >
                  删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
    </Space>
  );
}
