import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Typography,
} from 'antd';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import type { ContainerInspection } from '../../api/docker';
import ContainerMountFields from './ContainerMountFields';

export default function ContainerEditor({
  container,
  busy,
  onSubmit,
  onResource,
}: {
  container: ContainerInspection;
  busy: boolean;
  onSubmit: (data: object) => void;
  onResource: () => void;
}) {
  const [form] = Form.useForm();
  const publishAll = Form.useWatch('publishAll', form);
  const ports = Object.entries(container.HostConfig.PortBindings ?? {}).flatMap(
    ([port, bindings]) => {
      const [value, protocol] = port.split('/');
      return (bindings ?? []).map((binding) => ({
        container: Number(value),
        protocol,
        hostIp: binding.HostIp,
        host: Number(binding.HostPort),
      }));
    },
  );
  const mounts = (container.Mounts ?? [])
    .filter(
      (mount) =>
        mount.Type === 'bind' || (mount.Type === 'volume' && !!mount.Name),
    )
    .map((mount) => ({
      type: mount.Type,
      source: mount.Type === 'volume' ? mount.Name : mount.Source,
      target: mount.Destination,
      readOnly: !mount.RW,
    }));
  const inheritsMounts = !!container.HostConfig.VolumesFrom?.length;
  return (
    <Form
      form={form}
      layout="vertical"
      className="docker-container-form"
      initialValues={{
        name: container.Name.replace(/^\//, ''),
        image: container.Config.Image,
        ports,
        mounts,
        publishAll: container.HostConfig.PublishAllPorts,
        pull: false,
        env: (container.Config.Env ?? []).join('\n'),
        command: JSON.stringify(container.Config.Cmd ?? []),
      }}
      onFinish={(values) =>
        onSubmit({
          name: values.name,
          image:
            values.image.trim() === container.Config.Image && !values.pull
              ? ''
              : values.image,
          publishAll: values.publishAll,
          ports: form.isFieldsTouched(['ports', 'publishAll'])
            ? values.publishAll
              ? []
              : (values.ports ?? [])
            : undefined,
          mounts:
            !inheritsMounts &&
            JSON.stringify(values.mounts ?? []) !== JSON.stringify(mounts)
              ? (values.mounts ?? [])
              : undefined,
          pull: values.pull,
          env:
            values.env === (container.Config.Env ?? []).join('\n')
              ? undefined
              : values.env.split(/\r?\n/).filter(Boolean),
          command:
            values.command === JSON.stringify(container.Config.Cmd ?? [])
              ? undefined
              : JSON.parse(values.command),
        })
      }
    >
      <Form.Item
        name="name"
        label="容器名称"
        rules={[{ required: true, whitespace: true }]}
      >
        <Input />
      </Form.Item>
      <Form.Item
        name="image"
        label="镜像"
        rules={[{ required: true, whitespace: true }]}
      >
        <Input />
      </Form.Item>
      <Form.Item name="pull" valuePropName="checked">
        <Checkbox>保存前拉取镜像</Checkbox>
      </Form.Item>
      <Form.Item name="publishAll" valuePropName="checked">
        <Checkbox>发布镜像声明的全部端口</Checkbox>
      </Form.Item>
      {!publishAll && (
        <Form.List name="ports">
          {(fields, { add, remove }) => (
            <div className="docker-port-editor">
              <div className="docker-port-row docker-port-heading">
                <span>本地端口</span>
                <span>容器端口</span>
                <span>监听地址</span>
                <span>协议</span>
                <span />
              </div>
              {fields.map((field) => (
                <div className="docker-port-row" key={field.key}>
                  <Form.Item
                    name={[field.name, 'host']}
                    rules={[
                      { required: true, type: 'integer', min: 0, max: 65535 },
                    ]}
                  >
                    <InputNumber
                      aria-label="本地端口"
                      min={0}
                      max={65535}
                      placeholder="本地端口"
                    />
                  </Form.Item>
                  <Form.Item
                    name={[field.name, 'container']}
                    rules={[
                      { required: true, type: 'integer', min: 1, max: 65535 },
                    ]}
                  >
                    <InputNumber
                      aria-label="容器端口"
                      min={1}
                      max={65535}
                      placeholder="容器端口"
                    />
                  </Form.Item>
                  <Form.Item name={[field.name, 'hostIp']}>
                    <Input aria-label="监听 IP" placeholder="所有地址" />
                  </Form.Item>
                  <Form.Item
                    name={[field.name, 'protocol']}
                    rules={[{ required: true }]}
                  >
                    <Select
                      aria-label="协议"
                      options={['tcp', 'udp', 'sctp'].map((value) => ({
                        value,
                        label: value.toUpperCase(),
                      }))}
                    />
                  </Form.Item>
                  <Button
                    aria-label="移除此端口"
                    icon={<DeleteOutlined />}
                    onClick={() => remove(field.name)}
                  />
                </div>
              ))}
              <Button
                icon={<PlusOutlined />}
                onClick={() => add({ protocol: 'tcp', hostIp: '', host: 0 })}
              >
                添加端口
              </Button>
            </div>
          )}
        </Form.List>
      )}
      {['host', 'none'].includes(container.HostConfig.NetworkMode) && (
        <Alert
          type="info"
          message={`当前网络模式为 ${container.HostConfig.NetworkMode}，端口映射由 Docker 按网络模式处理。`}
        />
      )}
      <Collapse
        size="small"
        items={[
          {
            key: 'advanced',
            label: '更多设置',
            forceRender: true,
            children: (
              <>
                <Form.Item name="env" label="环境变量">
                  <Input.TextArea rows={4} placeholder="每行一个 NAME=value" />
                </Form.Item>
                <Form.Item
                  name="command"
                  label="启动命令（JSON 参数数组）"
                  rules={[
                    {
                      validator: (_, value: string) => {
                        try {
                          const args: unknown = JSON.parse(value);
                          if (
                            Array.isArray(args) &&
                            args.every((arg) => typeof arg === 'string')
                          )
                            return Promise.resolve();
                        } catch {
                          // 统一提示无效的 JSON 参数。
                        }
                        return Promise.reject(
                          new Error(
                            '请输入字符串数组，例如 ["sh", "-c", "echo ok"]',
                          ),
                        );
                      },
                    },
                  ]}
                >
                  <Input.TextArea rows={2} />
                </Form.Item>
                {inheritsMounts ? (
                  <Typography.Text type="secondary">
                    此容器的挂载来自 VolumesFrom，请在原配置中修改。
                  </Typography.Text>
                ) : (
                  <ContainerMountFields />
                )}
                <Button onClick={onResource}>调整资源限制</Button>
              </>
            ),
          },
        ]}
      />
      <Space style={{ marginTop: 16 }}>
        <Button type="primary" htmlType="submit" loading={busy}>
          保存容器配置
        </Button>
      </Space>
    </Form>
  );
}
