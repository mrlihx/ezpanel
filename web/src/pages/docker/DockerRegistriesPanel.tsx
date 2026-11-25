import { Button, Space } from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import { type Registry } from '../../api/docker';
import { request } from '../../api/client';

import type { DockerPageState } from './useDockerPage';

export default function DockerRegistriesPanel({
  destroy,
  fixedActions,
  message,
  open,
  registryList,
  run,
}: Pick<
  DockerPageState,
  'destroy' | 'fixedActions' | 'message' | 'open' | 'registryList' | 'run'
>) {
  return (
    <>
      <Button
        type="primary"
        style={{ marginBottom: 16 }}
        onClick={() => open({ kind: 'registry' })}
      >
        添加仓库
      </Button>
      <ContentSizedTable
        fillColumn="address"
        growColumns={['name']}
        rowKey="id"
        loading={registryList.isFetching}
        dataSource={registryList.data}
        columns={[
          { title: '仓库名', dataIndex: 'name', width: 180 },
          {
            title: '地址',
            dataIndex: 'address',
            width: 320,
            ellipsis: true,
          },
          {
            title: '用户名',
            dataIndex: 'username',
            width: 160,
            ellipsis: true,
          },
          { title: '备注', dataIndex: 'remark', width: 300 },
          {
            title: '操作',
            fixed: fixedActions,
            width: 260,
            render: (_, r: Registry) => (
              <Space>
                <Button
                  type="link"
                  onClick={() =>
                    run(async () => {
                      await request(`/api/docker/registries/${r.id}/test`, {
                        method: 'POST',
                      });
                      message.success('仓库认证成功');
                    })
                  }
                >
                  测试连接
                </Button>
                <Button
                  type="link"
                  disabled={r.builtin}
                  onClick={() =>
                    open({
                      kind: 'registry',
                      id: r.id,
                      values: { ...r },
                    })
                  }
                >
                  编辑
                </Button>
                <Button
                  type="link"
                  danger
                  disabled={r.builtin}
                  onClick={() => destroy('registries', r.id, r.name)}
                >
                  删除
                </Button>
              </Space>
            ),
          },
        ]}
      />
    </>
  );
}
