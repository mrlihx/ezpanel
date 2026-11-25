import {
  Button,
  Checkbox,
  Flex,
  Form,
  Input,
  Select,
  Space,
  Typography,
} from 'antd';
import { DeleteOutlined } from '@ant-design/icons';

export default function ContainerMountFields() {
  return (
    <Form.List name="mounts">
      {(fields, { add, remove }) => (
        <Space direction="vertical" style={{ width: '100%', marginBlock: 16 }}>
          <Typography.Text>存储挂载</Typography.Text>
          {fields.map(({ key, name, ...rest }) => (
            <Flex key={key} gap={8} wrap>
              <Form.Item {...rest} name={[name, 'type']}>
                <Select
                  style={{ width: 100 }}
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
            </Flex>
          ))}
          <Button onClick={() => add({ type: 'bind', readOnly: false })}>
            添加挂载
          </Button>
        </Space>
      )}
    </Form.List>
  );
}
