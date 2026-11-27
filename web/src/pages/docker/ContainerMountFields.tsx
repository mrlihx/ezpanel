import './docker-operation-form.css';
import { Button, Checkbox, Form, Input, Select, Flex } from 'antd';
import { DeleteOutlined } from '@ant-design/icons';

export default function ContainerMountFields() {
  return (
    <Form.List name="mounts">
      {(fields, { add, remove }) => (
        <Flex vertical align="start" gap={8}>
          {fields.map(({ key, name, ...rest }) => (
            <div key={key} className="docker-create-mount">
              <Form.Item {...rest} name={[name, 'type']}>
                <Select
                  aria-label="挂载类型"
                  options={[
                    { value: 'bind', label: '主机目录' },
                    { value: 'volume', label: '存储卷' },
                  ]}
                />
              </Form.Item>
              <Form.Item
                {...rest}
                name={[name, 'source']}
                rules={[{ required: true }]}
              >
                <Input placeholder="主机路径 / 卷名" />
              </Form.Item>
              <Form.Item
                {...rest}
                name={[name, 'target']}
                rules={[{ required: true }]}
              >
                <Input placeholder="容器路径" />
              </Form.Item>
              <Form.Item
                {...rest}
                name={[name, 'readOnly']}
                valuePropName="checked"
              >
                <Checkbox>只读</Checkbox>
              </Form.Item>
              <Button
                aria-label="移除挂载"
                icon={<DeleteOutlined />}
                onClick={() => remove(name)}
              />
            </div>
          ))}
          <Button onClick={() => add({ type: 'bind', readOnly: false })}>
            添加挂载
          </Button>
        </Flex>
      )}
    </Form.List>
  );
}
