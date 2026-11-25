import { Button, Flex, Space } from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import { formatTime } from '../../utils/format';
import { request } from '../../api/client';

import type { DockerPageState } from './useDockerPage';

export default function DockerVolumesPanel({
  containerList,
  destroy,
  filter,
  fixedActions,
  itemName,
  match,
  online,
  open,
  prune,
  run,
  setJsonDetail,
  volumeList,
}: Pick<
  DockerPageState,
  | 'containerList'
  | 'destroy'
  | 'filter'
  | 'fixedActions'
  | 'itemName'
  | 'match'
  | 'online'
  | 'open'
  | 'prune'
  | 'run'
  | 'setJsonDetail'
  | 'volumeList'
>) {
  return (
    <>
      <Flex justify="space-between" gap={12} wrap style={{ marginBottom: 16 }}>
        <Space wrap>
          <Button
            type="primary"
            disabled={!online}
            onClick={() => open({ kind: 'volume' })}
          >
            添加存储卷
          </Button>
          <Button disabled={!online} onClick={() => prune('volumes')}>
            清理未使用存储卷
          </Button>
        </Space>
        {filter}
      </Flex>
      <ContentSizedTable
        fillColumn="Name"
        rowKey="Name"
        dataSource={volumeList.data?.Volumes?.filter((r) => match(r.Name))}
        loading={volumeList.isFetching}
        columns={[
          {
            title: '存储卷',
            dataIndex: 'Name',
            width: 200,
            ellipsis: true,
          },
          {
            title: '挂载点',
            dataIndex: 'Mountpoint',
            width: 184,
            ellipsis: true,
          },
          {
            title: '所属容器',
            width: 220,
            ellipsis: true,
            render: (_, row) =>
              containerList.data
                ?.filter((container) =>
                  container.Mounts?.some(
                    (mount) =>
                      mount.Type === 'volume' && mount.Name === row.Name,
                  ),
                )
                .map(itemName)
                .join(', ') ||
              (containerList.data
                ? '未使用'
                : containerList.isError
                  ? '读取失败'
                  : '加载中'),
          },
          { title: '驱动', dataIndex: 'Driver', width: 100 },
          {
            title: '创建时间',
            dataIndex: 'CreatedAt',
            width: 185,
            render: (value?: string) => (value ? formatTime(value) : '—'),
          },
          {
            title: '标签',
            width: 220,
            ellipsis: true,
            render: (_, row) =>
              Object.entries(row.Labels ?? {})
                .map(([key, value]) => `${key}=${value}`)
                .join(', ') || '—',
          },
          {
            title: '操作',
            fixed: fixedActions,
            width: 150,
            render: (_, row) => (
              <Space>
                <Button
                  type="link"
                  onClick={() =>
                    run(async () =>
                      setJsonDetail({
                        title: row.Name,
                        content: await request(
                          `/api/docker/volumes/${encodeURIComponent(row.Name)}`,
                        ),
                      }),
                    )
                  }
                >
                  详情
                </Button>
                <Button
                  type="link"
                  danger
                  onClick={() => destroy('volumes', row.Name, row.Name)}
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
