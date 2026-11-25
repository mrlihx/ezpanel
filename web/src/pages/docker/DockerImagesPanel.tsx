import { Button, Dropdown, Flex, Input, Space, Tabs, Tag } from 'antd';
import ContentSizedTable from '../../components/ContentSizedTable';
import { type Image } from '../../api/docker';
import { formatSize, formatTime } from '../../utils/format';

import type { DockerPageState } from './useDockerPage';

export default function DockerImagesPanel({
  containerList,
  containersByImage,
  destroy,
  filter,
  imageList,
  imageView,
  itemName,
  match,
  online,
  open,
  prune,
  search,
  setImageTask,
  imageRoute,
  setTerm,
}: Pick<
  DockerPageState,
  | 'containerList'
  | 'containersByImage'
  | 'destroy'
  | 'filter'
  | 'imageList'
  | 'imageView'
  | 'itemName'
  | 'match'
  | 'online'
  | 'open'
  | 'prune'
  | 'search'
  | 'setImageTask'
  | 'imageRoute'
  | 'setTerm'
>) {
  return (
    <Tabs
      size="small"
      activeKey={imageView}
      onChange={imageRoute.selectTab}
      items={[
        {
          key: 'local',
          label: '本地镜像',
          children: (
            <>
              <Flex
                justify="space-between"
                gap={12}
                wrap
                style={{ marginBottom: 16 }}
              >
                <Space wrap>
                  <Button
                    type="primary"
                    disabled={!online}
                    onClick={() => open({ kind: 'pull' })}
                  >
                    拉取镜像
                  </Button>
                  <Button
                    disabled={!online}
                    onClick={() => setImageTask('load')}
                  >
                    导入镜像
                  </Button>
                  <Button
                    disabled={!online}
                    onClick={() => setImageTask('build')}
                  >
                    构建镜像
                  </Button>
                  <Button disabled={!online} onClick={() => prune('images')}>
                    清理悬空镜像
                  </Button>
                </Space>
                {filter}
              </Flex>
              <ContentSizedTable<Image>
                fillColumn="镜像名称 / 标签"
                rowKey="Id"
                dataSource={imageList.data?.filter((r) =>
                  match(`${(r.RepoTags ?? []).join(' ')} ${r.Id}`),
                )}
                loading={imageList.isFetching}
                columns={[
                  {
                    title: '镜像ID',
                    render: (_, r) => r.Id.slice(7, 19),
                  },
                  {
                    title: '镜像名称 / 标签',
                    render: (_, r) => r.RepoTags?.join(', ') || '未标记镜像',
                  },
                  {
                    title: '使用镜像的容器',
                    key: 'containers',
                    width: 245,
                    render: (_, r) => {
                      const usage = containersByImage.get(r.Id);
                      return usage?.length ? (
                        <Space size={[4, 4]} wrap>
                          {usage.map((row) => (
                            <Tag
                              key={row.Id}
                              color={
                                row.State === 'running' ? 'success' : 'default'
                              }
                            >
                              {itemName(row)}
                            </Tag>
                          ))}
                        </Space>
                      ) : containerList.isError ? (
                        '读取失败'
                      ) : containerList.isPending ? (
                        '读取中…'
                      ) : (
                        '—'
                      );
                    },
                  },
                  {
                    title: '大小',
                    dataIndex: 'Size',
                    render: formatSize,
                  },
                  {
                    title: '创建时间',
                    render: (_, r) =>
                      formatTime(new Date(r.Created * 1000).toISOString()),
                  },
                  {
                    title: '操作',
                    render: (_, r) => (
                      <Space>
                        <Button
                          type="link"
                          onClick={() =>
                            open({
                              kind: 'container',
                              values: {
                                image: r.RepoTags?.[0] ?? r.Id,
                              },
                            })
                          }
                        >
                          创建容器
                        </Button>
                        <Dropdown
                          trigger={['click']}
                          menu={{
                            items: [
                              { key: 'tag', label: '管理标签' },
                              { key: 'push', label: '推送镜像' },
                              {
                                key: 'delete',
                                label: '删除镜像',
                                danger: true,
                              },
                            ],
                            onClick: ({ key }) => {
                              if (key === 'tag')
                                open({ kind: 'tag', id: r.Id });
                              else if (key === 'push')
                                open({
                                  kind: 'push',
                                  values: {
                                    image: r.RepoTags?.[0] ?? '',
                                  },
                                });
                              else
                                destroy(
                                  'images',
                                  r.Id,
                                  r.RepoTags?.[0] ?? r.Id,
                                );
                            },
                          }}
                        >
                          <Button type="link">更多</Button>
                        </Dropdown>
                      </Space>
                    ),
                  },
                ]}
              />
            </>
          ),
        },
        {
          key: 'search',
          label: '搜索镜像',
          children: (
            <>
              <Space wrap style={{ marginBottom: 16 }}>
                <Input.Search
                  style={{ width: 360, maxWidth: '100%' }}
                  placeholder="搜索 Docker Hub 镜像，如 nginx"
                  onSearch={setTerm}
                />
                <Button
                  type="primary"
                  disabled={!online}
                  onClick={() => open({ kind: 'pull' })}
                >
                  指定地址拉取
                </Button>
              </Space>
              <ContentSizedTable
                fillColumn="description"
                rowKey="name"
                loading={search.isFetching}
                dataSource={search.data}
                locale={{
                  emptyText: '输入关键词搜索，或使用完整仓库地址拉取',
                }}
                columns={[
                  { title: '镜像名称', dataIndex: 'name' },
                  { title: '说明', dataIndex: 'description' },
                  { title: '星标', dataIndex: 'star_count' },
                  {
                    title: '官方',
                    render: (_, r) => (r.is_official ? '是' : '—'),
                  },
                  {
                    title: '操作',
                    render: (_, r) => (
                      <Space>
                        <Button
                          type="link"
                          onClick={() =>
                            open({
                              kind: 'pull',
                              values: {
                                image: r.name + ':latest',
                              },
                            })
                          }
                        >
                          选择版本 / 拉取
                        </Button>
                        <Button
                          type="link"
                          onClick={() =>
                            open({
                              kind: 'container',
                              values: {
                                image: r.name + ':latest',
                              },
                            })
                          }
                        >
                          创建容器
                        </Button>
                      </Space>
                    ),
                  },
                ]}
              />
            </>
          ),
        },
      ]}
    />
  );
}
