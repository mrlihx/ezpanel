import ContainerMountFields from './ContainerMountFields';
import {
  Alert,
  AutoComplete,
  Button,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Switch,
  type FormInstance,
} from 'antd';
import { DeleteOutlined } from '@ant-design/icons';
import type { Container, Image, Network } from '../../api/docker';
import { LAYOUT } from '../../styles/tokens';
import './docker-operation-form.css';
export type Operation = {
  kind:
    | 'container'
    | 'network'
    | 'volume'
    | 'registry'
    | 'tag'
    | 'pull'
    | 'push'
    | 'connect'
    | 'rename'
    | 'exec';
  id?: string;
  values?: Record<string, unknown>;
};
const restartOptions = [
  { value: 'no', label: '不自动重启' },
  { value: 'unless-stopped', label: '始终重启（手动停止除外）' },
  { value: 'always', label: '始终重启' },
  { value: 'on-failure', label: '异常退出重启' },
];
export default function DockerOperationForm({
  operation,
  form,
  busy,
  imageList,
  networkList,
  containerList,
  registryOptions,
  onClose,
  onSubmit,
}: {
  operation?: Operation;
  form: FormInstance;
  busy: boolean;
  imageList?: Image[];
  networkList?: Network[];
  containerList?: Container[];
  registryOptions?: { value: string; label: string }[];
  onClose: () => void;
  onSubmit: () => void;
}) {
  const itemName = (row: Container) =>
    row.Names?.map((name) => name.replace(/^\//, '')).join(', ') ||
    row.Id.slice(0, 12);
  const creatingContainer = operation?.kind === 'container';
  return (
    <Modal
      open={!!operation}
      width={creatingContainer ? 800 : LAYOUT.databaseModalWidth}
      title={
        operation
          ? {
              container: '创建容器',
              network: '添加网络',
              volume: '添加存储卷',
              registry: '仓库设置',
              tag: '设置镜像标签',
              pull: '拉取镜像',
              push: '推送镜像',
              connect: '连接 / 断开容器',
              rename: '容器重命名',
              exec: '执行容器命令',
            }[operation.kind]
          : ''
      }
      onCancel={onClose}
      onOk={onSubmit}
      confirmLoading={busy}
      destroyOnHidden
      styles={{ body: { maxHeight: '68dvh', overflowY: 'auto' } }}
    >
      <Form
        form={form}
        className={creatingContainer ? 'docker-create-form' : undefined}
        layout="horizontal"
        labelCol={{ flex: LAYOUT.formLabelWidth }}
        wrapperCol={{ flex: creatingContainer ? '1 1 0' : 1 }}
        colon={false}
      >
        {['container', 'network', 'volume', 'registry', 'rename'].includes(
          operation?.kind ?? '',
        ) && (
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
        )}
        {['container', 'pull', 'push'].includes(operation?.kind ?? '') && (
          <Form.Item
            name="image"
            label="镜像:版本"
            rules={[{ required: true }]}
          >
            <AutoComplete
              options={imageList?.flatMap((r) =>
                (r.RepoTags ?? []).map((value) => ({ value })),
              )}
              filterOption={(input, option) =>
                String(option?.value).includes(input)
              }
              placeholder="nginx:latest 或 registry.example.com/app:1.0"
            />
          </Form.Item>
        )}
        {['pull', 'push'].includes(operation?.kind ?? '') && (
          <Form.Item name="registryId" label="仓库账号">
            <Select options={registryOptions} />
          </Form.Item>
        )}
        {operation?.kind === 'registry' && (
          <>
            <Form.Item
              name="address"
              label="仓库地址"
              extra="HTTP私有仓库还需在Docker设置中配置insecure-registries。"
              rules={[{ required: true, type: 'url' }]}
            >
              <Input placeholder="https://registry.example.com" />
            </Form.Item>
            <Form.Item name="username" label="用户名">
              <Input />
            </Form.Item>
            <Form.Item name="password" label="密码">
              <Input.Password
                placeholder={operation.id ? '留空保留原密码' : ''}
              />
            </Form.Item>
            <Form.Item name="remark" label="备注">
              <Input.TextArea
                autoSize={{ minRows: 2, maxRows: 5 }}
                maxLength={512}
              />
            </Form.Item>
          </>
        )}
        {operation?.kind === 'network' && (
          <>
            <Form.Item name="driver" label="网络驱动">
              <Select options={[{ value: 'bridge', label: 'bridge' }]} />
            </Form.Item>
            <Form.Item name="subnet" label="子网">
              <Input placeholder="172.20.0.0/16（可选）" />
            </Form.Item>
            <Form.Item name="gateway" label="网关">
              <Input placeholder="172.20.0.1（可选）" />
            </Form.Item>
            <Form.Item name="internal" label="内部网络" valuePropName="checked">
              <Switch />
            </Form.Item>
          </>
        )}
        {operation?.kind === 'tag' && (
          <>
            <Form.Item
              name="repository"
              label="仓库 / 镜像名"
              rules={[{ required: true }]}
            >
              <Input />
            </Form.Item>
            <Form.Item name="tag" label="标签" rules={[{ required: true }]}>
              <Input />
            </Form.Item>
          </>
        )}
        {operation?.kind === 'connect' && (
          <>
            <Form.Item
              name="container"
              label="容器"
              rules={[{ required: true }]}
            >
              <Select
                options={containerList?.map((r) => ({
                  value: r.Id,
                  label: itemName(r),
                }))}
              />
            </Form.Item>
            <Form.Item name="disconnect" label="操作">
              <Select
                options={[
                  { value: false, label: '连接' },
                  { value: true, label: '断开' },
                ]}
              />
            </Form.Item>
          </>
        )}
        {creatingContainer && (
          <>
            <Form.Item name="restart" label="重启策略">
              <Select options={restartOptions} />
            </Form.Item>
            <div className="docker-create-limits">
              <Form.Item name="cpus" label="CPU 核数">
                <InputNumber min={0} max={1024} step={0.1} addonAfter="0不限" />
              </Form.Item>
              <Form.Item
                name="memoryMB"
                label="内存限制（MB）"
                labelCol={{ flex: 'none' }}
              >
                <InputNumber min={0} max={1048576} addonAfter="0不限" />
              </Form.Item>
            </div>
          </>
        )}
        {creatingContainer && (
          <>
            <Form.Item name="network" label="网络">
              <Select options={networkList?.map((r) => ({ value: r.Name }))} />
            </Form.Item>
            <Form.Item name="env" label="环境变量">
              <Input.TextArea rows={4} placeholder={'每行一个 KEY=VALUE'} />
            </Form.Item>
            <Form.Item
              name="command"
              label="启动命令"
              rules={[
                {
                  validator: (_, v) => {
                    if (!v) return Promise.resolve();
                    try {
                      const a = JSON.parse(v);
                      return Array.isArray(a) &&
                        a.every((x) => typeof x === 'string')
                        ? Promise.resolve()
                        : Promise.reject(new Error('请输入字符串参数数组'));
                    } catch {
                      return Promise.reject(new Error('请输入JSON参数数组'));
                    }
                  },
                },
              ]}
            >
              <Input placeholder='["nginx", "-g", "daemon off;"]' />
            </Form.Item>
            <Form.Item label="端口映射" className="docker-create-list">
              <Form.List name="ports">
                {(fields, { add, remove }) => (
                  <Flex vertical gap={8}>
                    {fields.map(({ key, name, ...rest }) => (
                      <div key={key} className="docker-create-port">
                        <Form.Item {...rest} name={[name, 'hostIp']}>
                          <Input placeholder="主机IP（默认全部）" />
                        </Form.Item>
                        <Form.Item
                          {...rest}
                          name={[name, 'host']}
                          rules={[{ required: true }]}
                        >
                          <InputNumber
                            min={1}
                            max={65535}
                            placeholder="主机端口"
                          />
                        </Form.Item>
                        <Form.Item
                          {...rest}
                          name={[name, 'container']}
                          rules={[{ required: true }]}
                        >
                          <InputNumber
                            min={1}
                            max={65535}
                            placeholder="容器端口"
                          />
                        </Form.Item>
                        <Form.Item {...rest} name={[name, 'protocol']}>
                          <Select
                            aria-label="协议"
                            options={[{ value: 'tcp' }, { value: 'udp' }]}
                          />
                        </Form.Item>
                        <Button
                          aria-label="移除端口映射"
                          icon={<DeleteOutlined />}
                          onClick={() => remove(name)}
                        />
                      </div>
                    ))}
                    <div>
                      <Button
                        onClick={() => add({ protocol: 'tcp', hostIp: '' })}
                      >
                        添加端口
                      </Button>
                    </div>
                  </Flex>
                )}
              </Form.List>
            </Form.Item>
            <Form.Item label="存储挂载" className="docker-create-list">
              <ContainerMountFields />
            </Form.Item>
            <Form.Item
              name="autoStart"
              label="创建后启动"
              valuePropName="checked"
            >
              <Switch />
            </Form.Item>
          </>
        )}
        {operation?.kind === 'exec' && (
          <>
            <Alert
              type="info"
              message="执行一次性命令，输出最多2MiB，最长等待1分钟。"
            />
            <Form.Item
              name="command"
              label="命令参数JSON"
              rules={[
                { required: true },
                {
                  validator: (_, v) => {
                    try {
                      const a = JSON.parse(v);
                      return Array.isArray(a) &&
                        a.length &&
                        a.every((x) => typeof x === 'string')
                        ? Promise.resolve()
                        : Promise.reject(new Error('请输入字符串参数数组'));
                    } catch {
                      return Promise.reject(new Error('JSON无效'));
                    }
                  },
                },
              ]}
            >
              <Input.TextArea rows={5} />
            </Form.Item>
          </>
        )}
      </Form>
    </Modal>
  );
}
