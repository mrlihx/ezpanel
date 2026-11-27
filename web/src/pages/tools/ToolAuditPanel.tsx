import { useState } from 'react';
import { Alert, Button, Space, Switch, Tabs } from 'antd';
import { request } from '../../api/client';
import ContentSizedTable from '../../components/ContentSizedTable';
import { formatTime } from '../../utils/format';
import LogText from '../../components/LogText';
import type { ToolboxPageState } from './index';

export default function ToolAuditPanel({
  audit,
  auditPage,
  busy,
  confirm,
  save,
  setAuditPage,
  setTaskId,
}: Pick<
  ToolboxPageState,
  | 'audit'
  | 'auditPage'
  | 'busy'
  | 'confirm'
  | 'save'
  | 'setAuditPage'
  | 'setTaskId'
>) {
  const [installing, setInstalling] = useState(false);
  return (
    <div className="tool-audit-panel">
      {audit.data && (
        <>
          <Alert
            type="info"
            message="面板操作审计记录终端会话与工具箱操作；系统命令明细需要安装 auditd 并启用 execve 审计，可能包含命令行中的敏感参数。"
          />
          <Space wrap>
            {!audit.data.installed ? (
              <Button
                loading={installing}
                disabled={busy || installing}
                onClick={async () => {
                  setInstalling(true);
                  try {
                    const r = await request<{ taskId: string }>(
                      '/api/toolbox/audit/setup',
                      { method: 'POST' },
                    );
                    setTaskId(r.taskId);
                  } finally {
                    setInstalling(false);
                  }
                }}
              >
                安装 auditd
              </Button>
            ) : (
              <Space>
                系统命令审计
                <Switch
                  checked={audit.data.enabled}
                  loading={busy}
                  onChange={(enabled) =>
                    confirm(
                      enabled ? '启用命令审计？' : '关闭命令审计？',
                      enabled
                        ? '将记录后续64位进程启动命令及其参数。'
                        : '仅移除面板添加的审计规则，已有日志保留。',
                      () => save('audit', { enabled }),
                    )
                  }
                />
              </Space>
            )}
            <Button onClick={() => void audit.refetch()}>刷新</Button>
          </Space>
          <Tabs
            className="tool-audit-tabs"
            items={[
              {
                key: 'panel',
                label: '面板操作',
                children: (
                  <ContentSizedTable
                    fillColumn="target"
                    rowKey="id"
                    size="small"
                    dataSource={audit.data.list}
                    pagination={{
                      current: auditPage,
                      pageSize: 20,
                      total: audit.data.total,
                      onChange: setAuditPage,
                    }}
                    columns={[
                      {
                        title: '时间',
                        dataIndex: 'createdAt',
                        render: formatTime,
                      },
                      { title: '用户', dataIndex: 'username' },
                      { title: '来源IP', dataIndex: 'ip' },
                      { title: '操作', dataIndex: 'action' },
                      { title: '目标', dataIndex: 'target' },
                      {
                        title: '结果',
                        render: (_, r) =>
                          r.success ? '成功' : r.errorMsg || '失败',
                      },
                    ]}
                  />
                ),
              },
              {
                key: 'system',
                label: '系统命令',
                children: (
                  <LogText className="tool-audit-output">
                    {audit.data.content ||
                      '暂无系统命令记录；启用后从新的进程启动开始采集。'}
                  </LogText>
                ),
              },
            ]}
          />
        </>
      )}
    </div>
  );
}
