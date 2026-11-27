import { useEffect, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Empty,
  Input,
  Menu,
  Pagination,
  Segmented,
  Select,
  Tabs,
  Tooltip,
  Typography,
} from 'antd';
import { RotateCw } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router-dom';
import ContentSizedTable from '../../components/ContentSizedTable';
import { request } from '../../api/client';
import { listCrons, type Run } from '../../api/cron';
import {
  applicationLog,
  applicationLogTargets,
  applicationSources,
  downloadLogFile,
  runtimeLog,
  saveLogFile,
  sshLogs,
  type SSHEntry,
  type TextLog,
} from '../../api/oplog';
import { catalog } from '../../api/software';
import { formatTime } from '../../utils/format';
import { LogOutput } from './shared';

export function RuntimeLogs() {
  const { modal } = App.useApp();
  const [keyword, setKeyword] = useState('');
  const query = useQuery({
    queryKey: ['panel-runtime-log'],
    queryFn: runtimeLog,
  });
  return (
    <div className="logs-runtime">
      <div className="logs-toolbar">
        <div className="logs-controls">
          <Button
            onClick={() => void query.refetch()}
            loading={query.isFetching}
          >
            刷新日志
          </Button>
          <Button
            onClick={() =>
              modal.confirm({
                title: '清空面板运行日志？',
                content: '清空当前及上一份运行日志，操作后无法恢复。',
                okButtonProps: { danger: true },
                onOk: async () => {
                  await request('/api/logs/runtime', { method: 'DELETE' });
                  void query.refetch();
                },
              })
            }
          >
            清空日志
          </Button>
        </div>
        <Input.Search
          className="logs-search"
          placeholder="请输入搜索关键字"
          allowClear
          onSearch={setKeyword}
        />
      </div>
      <LogOutput
        fill
        data={query.data}
        loading={query.isFetching}
        error={query.error}
        keyword={keyword}
      />
    </div>
  );
}

export function CronLogs() {
  const { modal } = App.useApp();
  const [keyword, setKeyword] = useState(''),
    [page, setPage] = useState(1);
  const [selected, setSelected] = useState<string>();
  const crons = useQuery({
    queryKey: ['log-crons', page, keyword],
    queryFn: () => listCrons(page, 20, keyword),
  });
  const job = selected ?? crons.data?.list[0]?.id;
  const text = useQuery({
    queryKey: ['log-cron-output', job],
    queryFn: ({ signal }) =>
      request<{ text: TextLog; run: Run } | null>(`/api/logs/crons/${job}`, {
        signal,
      }),
    enabled: !!job,
  });
  return (
    <div className="logs-split">
      <aside className="logs-sidebar">
        <Input.Search
          placeholder="请输入日志名称"
          allowClear
          onSearch={(value) => {
            setKeyword(value);
            setPage(1);
            setSelected(undefined);
          }}
        />
        {crons.error && <Alert type="error" message={crons.error.message} />}
        <Menu
          selectedKeys={job ? [job] : []}
          items={crons.data?.list.map((row) => ({
            key: row.id,
            label: row.name,
          }))}
          onClick={({ key }) => setSelected(key)}
        />
        <Pagination
          size="small"
          simple
          current={page}
          pageSize={20}
          total={crons.data?.total || 0}
          onChange={(value) => {
            setPage(value);
            setSelected(undefined);
          }}
        />
      </aside>
      <section>
        <div className="logs-toolbar">
          <div className="logs-controls">
            <Button
              disabled={!job}
              onClick={() =>
                modal.confirm({
                  title: '清理此计划任务的日志？',
                  content: '清理已结束的执行记录与日志，运行中的任务会保留。',
                  okButtonProps: { danger: true },
                  onOk: async () => {
                    await request(`/api/crons/${job}/logs`, {
                      method: 'DELETE',
                    });
                    void text.refetch();
                  },
                })
              }
            >
              清理日志
            </Button>
          </div>
          <Button
            icon={<RotateCw size={16} strokeWidth={1.9} />}
            aria-label="刷新计划任务日志"
            disabled={!job}
            loading={text.isFetching}
            onClick={() => void text.refetch()}
          />
        </div>
        {!job ? (
          <Empty description="请选择要查看的日志" />
        ) : text.data === null && !text.isFetching ? (
          <Empty description="暂无执行日志" />
        ) : (
          <>
            {text.data && (
              <Typography.Text type="secondary">
                {text.data.run.status}
                {text.data.run.exitCode !== null &&
                  ` · 退出码 ${text.data.run.exitCode}`}
              </Typography.Text>
            )}
            {text.data?.run.errorMsg && (
              <Alert type="error" message={text.data.run.errorMsg} />
            )}
            <LogOutput
              data={text.data?.text}
              loading={text.isFetching}
              error={text.error}
            />
          </>
        )}
      </section>
    </div>
  );
}

export function SSHLogs({
  initialStatus = 'all',
  tableClassName,
}: {
  initialStatus?: string;
  tableClassName?: string;
}) {
  const [status, setStatus] = useState(initialStatus),
    [keyword, setKeyword] = useState(''),
    [page, setPage] = useState(1),
    [size, setSize] = useState(20);
  const query = useQuery({
    queryKey: ['ssh-login-logs'],
    queryFn: sshLogs,
    staleTime: 60000,
  });
  const filtered = query.data?.list.filter(
    (row) =>
      (status === 'all' || String(row.success) === status) &&
      `${row.address} ${row.username}`.includes(keyword),
  );
  return (
    <>
      <div className="logs-toolbar">
        <Button onClick={() => void query.refetch()} loading={query.isFetching}>
          刷新列表
        </Button>
        <div className="logs-controls">
          <Segmented
            value={status}
            options={[
              { value: 'all', label: '全部' },
              { value: 'true', label: '登录成功' },
              { value: 'false', label: '登录失败' },
            ]}
            onChange={(value) => {
              setStatus(String(value));
              setPage(1);
            }}
          />
          <Input.Search
            className="logs-search"
            placeholder="请输入登录IP/用户名"
            allowClear
            onSearch={(value) => {
              setKeyword(value);
              setPage(1);
            }}
          />
        </div>
      </div>
      {query.error && <Alert type="error" message={query.error.message} />}
      {query.data && (
        <Typography.Paragraph type="secondary">
          {query.data.scope}
          {query.data.truncated ? '（达到读取上限）' : ''}
          ；公网IP归属地未配置时显示“—”。
        </Typography.Paragraph>
      )}
      <ContentSizedTable<SSHEntry>
        className={tableClassName}
        fillColumn="address"
        rowKey="id"
        size="middle"
        dataSource={filtered}
        loading={query.isFetching}
        pagination={{
          current: page,
          pageSize: size,
          showSizeChanger: true,
          showTotal: (total) => `共 ${total} 条`,
          onChange: (p, s) => {
            setPage(p);
            setSize(s);
          },
        }}
        columns={[
          { title: 'IP地址:端口', dataIndex: 'address', width: 240 },
          { title: '归属地', dataIndex: 'location', width: 130 },
          { title: '用户', dataIndex: 'username', ellipsis: true },
          {
            title: '状态',
            dataIndex: 'success',
            width: 130,
            render: (value) => (
              <Typography.Text type={value ? 'success' : 'danger'}>
                {value ? '登录成功' : '登录失败'}
              </Typography.Text>
            ),
          },
          {
            title: '操作时间',
            dataIndex: 'createdAt',
            width: 190,
            render: formatTime,
          },
        ]}
      />
    </>
  );
}

export const logApps = {
  frp: 'Frp服务',
  openresty: 'OpenResty',
  mysql: 'MySQL',
  mariadb: 'MariaDB',
  redis: 'Redis',
  php: 'PHP',
  mongodb: 'MongoDB',
  memcached: 'Memcached',
  docker: 'Docker',
  ftp: 'Pure-FTPd',
  rsync: 'Rsync',
  clamav: 'ClamAV',
  fail2ban: 'Fail2ban',
  supervisor: 'Supervisor',
};
const instanceLogApps = new Set([
  'mysql',
  'mariadb',
  'redis',
  'mongodb',
  'memcached',
]);

type LogSourceRoute = {
  source?: string;
  onSourceChange: (source: string, replace?: boolean) => void;
};

export function ApplicationLogs({
  app,
  onChange,
  ...route
}: LogSourceRoute & {
  app: string;
  onChange: (app: string) => void;
}) {
  return (
    <div className="logs-split">
      <aside className="logs-sidebar">
        <Menu
          selectedKeys={[app]}
          items={Object.entries(logApps).map(([key, label]) => ({
            key,
            label,
          }))}
          onClick={({ key }) => onChange(key)}
        />
      </aside>
      <ApplicationLogView key={app} app={app} {...route} />
    </div>
  );
}

function ApplicationLogView({
  app,
  ...route
}: LogSourceRoute & { app: string }) {
  const [params, setParams] = useSearchParams();
  const php = app === 'php';
  const instances = instanceLogApps.has(app);
  const apps = useQuery({
    queryKey: ['software-catalog'],
    queryFn: ({ signal }) => catalog(signal),
    staleTime: 60000,
    enabled: php,
  });
  const targets = useQuery({
    queryKey: ['application-log-targets', app],
    queryFn: () => applicationLogTargets(app),
    enabled: instances,
  });
  const choices = php
    ? apps.data?.list
        .filter((row) => row.id.startsWith('php-') && row.installed)
        .map((row) => ({
          id: row.id,
          name: row.version ? `PHP ${row.version}` : row.name,
        }))
    : targets.data;
  const key = php ? 'version' : 'instance';
  const requested = params.get(key);
  const targetKey = (id: string) => (php ? id.replace(/^php-/, '') : id);
  const target =
    requested === null
      ? choices?.[0]?.id
      : choices?.find((row) => targetKey(row.id) === requested)?.id;
  const urlTarget = target ? targetKey(target) : undefined;
  const missing = requested !== null && !!choices && !target;
  useEffect(() => {
    if (!urlTarget || requested !== null) return;
    // 来源路由就绪后再补写默认目标，避免两次导航互相覆盖。
    if (!route.source) return;
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.set(key, urlTarget);
        return next;
      },
      { replace: true },
    );
  }, [key, requested, route.source, setParams, urlTarget]);
  return (
    <>
      {missing && (
        <Alert
          type="warning"
          showIcon
          message="链接指定的版本或实例不存在，请重新选择。"
        />
      )}
      <ApplicationLogOutput
        key={target ?? app}
        app={app}
        target={target}
        {...route}
        targetLoading={php ? apps.isFetching : targets.isFetching}
        targetError={php ? apps.error : targets.error}
        enabled={!(php || instances) || !!target}
        selector={
          php || instances ? (
            <Select
              aria-label={php ? 'PHP 版本' : '实例或容器'}
              placeholder={php ? '选择 PHP 版本' : '选择实例或容器'}
              loading={php ? apps.isFetching : targets.isFetching}
              disabled={!choices?.length}
              style={{ minWidth: 160, maxWidth: '100%' }}
              options={choices?.map((row) => ({
                value: row.id,
                label: row.name,
              }))}
              value={target}
              onChange={(id) =>
                setParams((previous) => {
                  const next = new URLSearchParams(previous);
                  next.set(key, targetKey(id));
                  return next;
                })
              }
            />
          ) : undefined
        }
      />
    </>
  );
}

function ApplicationLogOutput({
  app,
  target,
  source: requested,
  onSourceChange,
  enabled,
  selector,
  targetLoading,
  targetError,
}: LogSourceRoute & {
  app: string;
  target?: string;
  enabled: boolean;
  selector?: React.ReactNode;
  targetLoading: boolean;
  targetError: Error | null;
}) {
  const { modal, message } = App.useApp();
  const [keyword, setKeyword] = useState('');
  const sources = useQuery({
    queryKey: ['application-log-sources', app, target],
    queryFn: () => applicationSources(app, target),
    enabled,
  });
  const source = requested
    ? sources.data?.find((row) => row.id === requested)?.id
    : sources.data?.[0]?.id;
  useEffect(() => {
    if (source && !requested) onSourceChange(source, true);
  }, [onSourceChange, requested, source]);
  const text = useQuery({
    queryKey: ['application-log', app, target, source],
    queryFn: () => applicationLog(app, source!, target),
    enabled: enabled && !!source,
  });
  const logName = [app, target, source].filter(Boolean).join('-');
  return (
    <section>
      {targetError && <Alert type="error" message={targetError.message} />}
      {sources.error && <Alert type="error" message={sources.error.message} />}
      {requested && sources.data && !source && (
        <Alert
          type="warning"
          showIcon
          message="当前实例没有此日志来源，请选择可用来源。"
        />
      )}
      {!!sources.data?.length && (
        <Tabs
          type="card"
          activeKey={source}
          items={sources.data.map((row) => ({
            key: row.id,
            label: row.name,
          }))}
          onChange={(key) => {
            onSourceChange(key);
            setKeyword('');
          }}
        />
      )}
      <div className="logs-toolbar">
        <div className="logs-controls">
          <Tooltip
            title={
              text.data && !text.data.clearable
                ? '当前日志来源不支持单独清空'
                : undefined
            }
          >
            <Button
              disabled={!text.data?.clearable}
              onClick={() =>
                modal.confirm({
                  title: '清空当前应用日志？',
                  content: '清空后无法恢复，不会重启服务。',
                  okButtonProps: { danger: true },
                  onOk: async () => {
                    await request(`/api/logs/apps/${app}/${source}`, {
                      method: 'DELETE',
                      params: { target },
                    });
                    void text.refetch();
                  },
                })
              }
            >
              清空日志
            </Button>
          </Tooltip>
          <Button
            disabled={!source}
            onClick={() =>
              void downloadLogFile(
                `/api/logs/apps/${app}/${source}/download`,
                `${logName}.log`,
                { target },
              ).catch((error) => message.error(error.message))
            }
          >
            导出日志
          </Button>
          {selector}
        </div>
        <div className="logs-controls">
          <Input.Search
            key={`${app}-${source}`}
            className="logs-search"
            placeholder="请输入搜索关键字"
            allowClear
            onSearch={setKeyword}
          />
          <Button
            icon={<RotateCw size={16} strokeWidth={1.9} />}
            aria-label="刷新应用日志"
            loading={targetLoading || sources.isFetching || text.isFetching}
            onClick={() => {
              if (enabled) void sources.refetch();
              if (source) void text.refetch();
            }}
          />
        </div>
      </div>
      {(!enabled && !targetLoading && !targetError) ||
      sources.data?.length === 0 ? (
        <Empty
          description={
            <span>
              应用未安装或暂无可读取日志，<Link to="/apps">前往应用商店</Link>
            </span>
          }
        />
      ) : (
        <LogOutput
          key={source}
          data={text.data}
          loading={targetLoading || sources.isFetching || text.isFetching}
          error={text.error}
          keyword={keyword}
        />
      )}
      {text.data?.truncated && (
        <Button
          type="link"
          onClick={() =>
            saveLogFile(text.data!.content, `${logName}-recent.log`)
          }
        >
          保存当前日志片段
        </Button>
      )}
    </section>
  );
}
