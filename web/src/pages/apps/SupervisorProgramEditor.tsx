import { lazy, Suspense, useState } from 'react';
import {
  Alert,
  App,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Skeleton,
  Switch,
  Typography,
} from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import ApplicationSettingsForm from '../../components/ApplicationSettingsForm';
import {
  saveSupervisorProgram,
  type SupervisorProgram,
  type SupervisorValues,
} from '../../api/supervisor';
import HostToolDirectory from './HostToolDirectory';

const CodeEditor = lazy(() => import('../../components/CodeEditor'));

export default function SupervisorProgramEditor({
  program,
  users,
  raw,
  onClose,
}: {
  program?: SupervisorProgram;
  users: string[];
  raw: boolean;
  onClose: () => void;
}) {
  const { modal, message } = App.useApp();
  const client = useQueryClient();
  const [form] = Form.useForm<SupervisorValues & { name: string }>();
  const [content, setContent] = useState(program?.content ?? '');
  const [touched, setTouched] = useState(false);
  const save = useMutation({
    mutationFn: saveSupervisorProgram,
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['supervisor-programs'] });
      void client.invalidateQueries({
        queryKey: ['application-log-sources', 'supervisor'],
      });
      message.success('进程配置已保存并应用');
      onClose();
    },
  });
  const dirty = raw ? content !== program?.content : touched;
  const close = () => {
    if (save.isPending) return;
    if (dirty) modal.confirm({ title: '放弃尚未保存的修改？', onOk: onClose });
    else onClose();
  };
  const submit = (values?: SupervisorValues & { name: string }) => {
    const apply = () =>
      save.mutateAsync(
        raw
          ? { name: program!.name, revision: program!.revision, content }
          : { name: values!.name, revision: program?.revision, values },
      );
    if (
      program?.processes.some((row) =>
        ['RUNNING', 'STARTING', 'BACKOFF'].includes(row.state),
      )
    ) {
      modal.confirm({
        title: '保存并应用进程配置？',
        content: '配置发生变化时，将重启当前进程组。',
        onOk: apply,
      });
    } else void apply();
  };
  return (
    <Modal
      open
      centered
      title={
        raw
          ? `配置文件 · ${program!.name}`
          : program
            ? `修改进程 · ${program.name}`
            : '添加守护进程'
      }
      width={raw ? 900 : 720}
      onCancel={close}
      maskClosable={false}
      destroyOnHidden
      confirmLoading={save.isPending}
      okText="保存并应用"
      cancelButtonProps={{ disabled: save.isPending }}
      okButtonProps={{ disabled: raw && !dirty }}
      onOk={() => (raw ? submit() : form.submit())}
    >
      {raw ? (
        <>
          <Typography.Paragraph code copyable>
            {program!.path}
          </Typography.Paragraph>
          <Alert
            type="info"
            showIcon
            message="编辑当前进程的配置段，保存后自动应用。"
            style={{ marginBottom: 12 }}
          />
          <div style={{ height: 'min(54dvh, 480px)', minHeight: 280 }}>
            <Suspense fallback={<Skeleton active />}>
              <CodeEditor
                path={`${program!.path}#${program!.name}`}
                value={content}
                onChange={setContent}
              />
            </Suspense>
          </div>
        </>
      ) : (
        <ApplicationSettingsForm
          form={form}
          onValuesChange={() => setTouched(true)}
          initialValues={
            program ?? {
              user: 'root',
              numprocs: 1,
              priority: 999,
              autostart: true,
              autorestart: 'unexpected',
              remark: '',
              directory: '',
              command: '',
            }
          }
          onFinish={submit}
          disabled={save.isPending}
        >
          <Form.Item
            name="name"
            label="名称"
            rules={[
              { required: true },
              {
                pattern: /^[A-Za-z0-9_-]{1,80}$/,
                message: '请输入字母、数字、下划线或短横线，最多 80 位',
              },
            ]}
          >
            <Input disabled={!!program} placeholder="例如 worker" />
          </Form.Item>
          <Form.Item name="user" label="启动用户" rules={[{ required: true }]}>
            <Select
              showSearch
              options={users.map((value) => ({ value, label: value }))}
            />
          </Form.Item>
          <Form.Item
            name="directory"
            label="运行目录"
            rules={[{ required: true }]}
          >
            <HostToolDirectory />
          </Form.Item>
          <Form.Item
            name="command"
            label="启动命令"
            rules={[{ required: true, whitespace: true }]}
            extra="进程需以前台方式运行，命令中的文件请使用绝对路径。"
          >
            <Input placeholder="/usr/bin/python3 /www/worker/main.py" />
          </Form.Item>
          <Form.Item
            name="numprocs"
            label="进程数量"
            rules={[{ required: true }]}
          >
            <InputNumber min={1} max={100} precision={0} />
          </Form.Item>
          <Form.Item
            name="priority"
            label="优先级"
            rules={[{ required: true }]}
          >
            <InputNumber min={0} max={9999} precision={0} />
          </Form.Item>
          <Form.Item name="autostart" label="自动启动" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item
            name="autorestart"
            label="自动重启"
            rules={[{ required: true }]}
          >
            <Select
              options={[
                { value: 'unexpected', label: '异常退出时重启' },
                { value: 'true', label: '退出后始终重启' },
                { value: 'false', label: '关闭' },
              ]}
            />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input maxLength={500} />
          </Form.Item>
        </ApplicationSettingsForm>
      )}
    </Modal>
  );
}
