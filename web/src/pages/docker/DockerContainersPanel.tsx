import { Button, Dropdown, Flex, Space, Tooltip, Typography } from 'antd';
import { ChevronDown, Pause, Play, Plus } from 'lucide-react';
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { request } from '../../api/client';
import ContentSizedTable from '../../components/ContentSizedTable';
import { inspectContainer, type Container } from '../../api/docker';
import { formatTime } from '../../utils/format';
import { useResourceActions } from '../../plugins/ResourceActions';

import { containerOwnership } from './containerOwnership';
import type { DockerPageState } from './useDockerPage';

function ContainerMoreActions({
  row,
  onSelect,
}: {
  row: Container;
  onSelect: (tab: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const pluginActions = useResourceActions('containers')(
    row.Id,
    row.Names?.[0]?.replace(/^\//, ''),
  );
  const owner = containerOwnership(row.Labels, row.Names?.[0] ?? '');
  const detail = useQuery({
    queryKey: ['docker-container-detail', row.Id],
    queryFn: ({ signal }) => inspectContainer(row.Id, signal),
    enabled: open && !owner && row.State !== 'paused',
  });
  const editable =
    !owner &&
    row.State !== 'paused' &&
    detail.data &&
    !detail.data.HostConfig.AutoRemove &&
    !detail.data.State.Paused;
  return (
    <Dropdown
      trigger={['click']}
      open={open}
      onOpenChange={setOpen}
      menu={{
        items: [
          ...(editable ? [{ key: 'edit', label: '编辑配置' }] : []),
          { key: 'restart', label: '资源与重启策略' },
          ...(!owner ? [{ key: 'rename', label: '重命名' }] : []),
          { key: 'logs', label: '日志' },
          { key: 'stats', label: '实时监控' },
          ...pluginActions,
        ],
        onClick: ({ key }) => {
          setOpen(false);
          if (!key.startsWith('plugin-')) onSelect(key);
        },
      }}
    >
      <Button type="link" style={{ padding: 0 }}>
        更多
      </Button>
    </Dropdown>
  );
}

export default function DockerContainersPanel({
  action,
  actions,
  busy,
  containerImageName,
  containerList,
  destroy,
  filter,
  fixedActions,
  itemName,
  match,
  modal,
  online,
  open,
  prune,
  run,
  selected,
  setDetail,
  setSelected,
  setTerminal,
  token,
}: Pick<
  DockerPageState,
  | 'action'
  | 'actions'
  | 'busy'
  | 'containerImageName'
  | 'containerList'
  | 'destroy'
  | 'filter'
  | 'fixedActions'
  | 'itemName'
  | 'match'
  | 'modal'
  | 'online'
  | 'open'
  | 'prune'
  | 'run'
  | 'selected'
  | 'setSelected'
  | 'setTerminal'
  | 'token'
> & {
  setDetail: (detail: { id: string; name: string; tab: string }) => void;
}) {
  return (
    <>
      <Flex justify="space-between" gap={12} wrap style={{ marginBottom: 16 }}>
        <Space wrap>
          <Button
            type="primary"
            icon={<Plus size={16} strokeWidth={2} />}
            disabled={!online}
            onClick={() => open({ kind: 'container' })}
          >
            创建容器
          </Button>
          <Dropdown
            disabled={!online || !selected.length || busy}
            menu={{
              items: [
                { key: 'start', label: '批量启动' },
                { key: 'stop', label: '批量停止' },
                { key: 'restart', label: '批量重启' },
              ],
              onClick: ({ key }) =>
                modal.confirm({
                  title: `对${selected.length}个容器执行${key}？`,
                  onOk: () =>
                    run(async () => {
                      const failures: {
                        id: (typeof selected)[number];
                        message: string;
                      }[] = [];
                      for (const id of selected) {
                        try {
                          await request(`/api/docker/containers/${id}/action`, {
                            method: 'POST',
                            data: { action: key },
                            silentError: true,
                          });
                        } catch (error) {
                          failures.push({
                            id,
                            message:
                              error instanceof Error
                                ? error.message
                                : '操作失败',
                          });
                        }
                      }
                      setSelected(failures.map(({ id }) => id));
                      if (failures.length) {
                        modal.warning({
                          title: `成功 ${selected.length - failures.length} 项，失败 ${failures.length} 项`,
                          content: (
                            <ul
                              style={{
                                maxHeight: 240,
                                overflow: 'auto',
                                overflowWrap: 'anywhere',
                              }}
                            >
                              {failures.map(({ id, message }) => {
                                const row = containerList.data?.find(
                                  (container) => container.Id === id,
                                );
                                return (
                                  <li key={id}>
                                    {row ? itemName(row) : String(id)}：
                                    {message}
                                  </li>
                                );
                              })}
                            </ul>
                          ),
                        });
                      }
                    }),
                }),
            }}
          >
            <Button>批量操作（{selected.length}）</Button>
          </Dropdown>
          <Button onClick={() => prune('containers')} disabled={!online}>
            清理已停止独立容器
          </Button>
        </Space>
        {filter}
      </Flex>
      <ContentSizedTable<Container>
        fillColumn="Names"
        growColumns={['Image']}
        rowKey="Id"
        loading={containerList.isFetching}
        rowSelection={{
          selectedRowKeys: selected,
          onChange: setSelected,
          getCheckboxProps: () => ({ disabled: busy }),
        }}
        dataSource={containerList.data?.filter((r) =>
          match(`${itemName(r)} ${r.Id} ${r.Image} ${containerImageName(r)}`),
        )}
        columns={[
          {
            title: '容器名称',
            key: 'Names',
            width: 220,
            ellipsis: true,
            render: (_, r) => (
              <Button
                type="link"
                title={itemName(r)}
                style={{ paddingInline: 0 }}
                onClick={() =>
                  setDetail({
                    id: r.Id,
                    name: itemName(r),
                    tab: 'status',
                  })
                }
              >
                {itemName(r)}
              </Button>
            ),
          },
          {
            title: '容器ID',
            width: 135,
            responsive: ['md'],
            render: (_, row) => (
              <Tooltip title={row.Id}>
                <Typography.Text copyable={{ text: row.Id }}>
                  {row.Id.slice(0, 12)}
                </Typography.Text>
              </Tooltip>
            ),
          },
          {
            title: '状态',
            width: 108,
            render: (_, row) => (
              <Dropdown
                trigger={['click']}
                menu={{
                  items: actions(row),
                  onClick: ({ key }) =>
                    modal.confirm({
                      title: `${({ start: '启动', stop: '停止', restart: '重启', kill: '强制停止', pause: '暂停', unpause: '恢复' } as Record<string, string>)[key]}容器 ${itemName(row)}？`,
                      okButtonProps: { danger: key === 'kill' },
                      onOk: () => action(row, key),
                    }),
                }}
              >
                <Tooltip title={row.Status}>
                  <Button
                    type="link"
                    style={{
                      padding: 0,
                      color:
                        row.State === 'running'
                          ? token.colorSuccess
                          : row.State === 'paused'
                            ? token.colorWarning
                            : token.colorError,
                    }}
                    iconPosition="end"
                    icon={
                      row.State === 'running' ? (
                        <Play size={12} strokeWidth={2.2} fill="currentColor" />
                      ) : row.State === 'paused' ? (
                        <Pause size={12} strokeWidth={2.2} />
                      ) : (
                        <ChevronDown size={13} strokeWidth={2} />
                      )
                    }
                  >
                    {(
                      {
                        running: '运行中',
                        paused: '已暂停',
                        restarting: '重启中',
                        created: '已创建',
                        exited: '已停止',
                        dead: '已停止',
                        removing: '删除中',
                      } as Record<string, string>
                    )[row.State] ?? row.State}
                  </Button>
                </Tooltip>
              </Dropdown>
            ),
          },
          {
            title: '镜像名称 / 标签',
            dataIndex: 'Image',
            width: 260,
            ellipsis: true,
            render: (_, row) => {
              const name = containerImageName(row);
              return <span title={name}>{name}</span>;
            },
          },
          {
            title: '端口',
            width: 192,
            ellipsis: true,
            render: (_, r) =>
              r.Ports?.map(
                (p) =>
                  `${p.IP || ''}${p.PublicPort ? ':' + p.PublicPort + ' → ' : ''}${p.PrivatePort}/${p.Type}`,
              ).join(', '),
          },
          {
            title: '创建时间',
            width: 160,
            responsive: ['lg'],
            render: (_, r) =>
              formatTime(new Date(r.Created * 1000).toISOString()),
          },
          {
            title: '操作',
            width: 240,
            fixed: fixedActions,
            render: (_, row) => (
              <Space size={12}>
                <Button
                  type="link"
                  style={{ padding: 0 }}
                  onClick={() =>
                    setDetail({
                      id: row.Id,
                      name: itemName(row),
                      tab: 'status',
                    })
                  }
                >
                  管理
                </Button>
                <Button
                  type="link"
                  style={{ padding: 0 }}
                  disabled={row.State !== 'running'}
                  onClick={() => setTerminal(row.Id)}
                >
                  终端
                </Button>
                <Button
                  type="link"
                  danger
                  style={{ padding: 0 }}
                  onClick={() =>
                    destroy(
                      'containers',
                      row.Id,
                      itemName(row),
                      containerOwnership(row.Labels, row.Names?.[0] ?? ''),
                    )
                  }
                >
                  删除
                </Button>
                <ContainerMoreActions
                  row={row}
                  onSelect={(tab) =>
                    setDetail({ id: row.Id, name: itemName(row), tab })
                  }
                />
              </Space>
            ),
          },
        ]}
      />
    </>
  );
}
