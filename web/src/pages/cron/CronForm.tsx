import { lazy, Suspense, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Flex,
  Form,
  Input,
  InputNumber,
  Select,
  Skeleton,
  Space,
  theme,
} from 'antd';
import { FolderOpenOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import {
  cronTypes,
  backupStorages,
  saveCron,
  cronSites,
  type Cron,
  type CronInput,
} from '../../api/cron';
import { servers, databaseList } from '../../api/database';
import { LAYOUT, SPACING } from '../../styles/tokens';
import { panelSettings } from '../../api/settings';
import type { Website } from '../../api/website';
import DirectoryPicker from '../file/DirectoryPicker';
import URLFields from './URLFields';
import './cron.css';
const CodeEditor = lazy(() => import('../../components/CodeEditor'));
const allDatabasesValue = ':all';
const hourSteps = [2, 3, 4, 6, 8, 12];
const minuteSteps = [2, 3, 4, 5, 6, 10, 12, 15, 20, 30];
const projectLabels: Record<Website['projectType'], string> = {
  python: 'Python项目',
  go: 'Go项目',
  nodejs: 'Node.js项目',
  php: 'PHP项目',
  proxy: '反向代理',
};

function scheduleFields(expression?: string) {
  const fields = {
    cycle: expression ? 'custom' : 'daily',
    minute: 0,
    hour: 3,
    step: 2,
    week: 1,
    day: 1,
  };
  if (!expression) return fields;
  const parts = expression.trim().split(/\s+/);
  if (parts.length !== 5) return fields;
  const [minute, hour, day, month, week] = parts;
  const between = (value: string, min: number, max: number) =>
    /^\d+$/.test(value) && Number(value) >= min && Number(value) <= max;
  const stepOf = (value: string) =>
    value.startsWith('*/') ? Number(value.slice(2)) : 0;
  const minuteStep = stepOf(minute);
  const hourStep = stepOf(hour);
  if (month !== '*' || !between(minute, 0, 59)) {
    if (
      hour === '*' &&
      day === '*' &&
      month === '*' &&
      week === '*' &&
      minuteSteps.includes(minuteStep)
    ) {
      return { ...fields, cycle: 'minutes', step: minuteStep };
    }
    return fields;
  }
  fields.minute = Number(minute);
  if (day === '*' && week === '*') {
    if (hour === '*') return { ...fields, cycle: 'hourly' };
    if (hourSteps.includes(hourStep))
      return { ...fields, cycle: 'hours', step: hourStep };
  }
  if (!between(hour, 0, 23)) return fields;
  fields.hour = Number(hour);
  if (day === '*' && week === '*') return { ...fields, cycle: 'daily' };
  if (day === '*' && between(week, 0, 6))
    return { ...fields, cycle: 'weekly', week: Number(week) };
  if (week === '*' && between(day, 1, 31))
    return { ...fields, cycle: 'monthly', day: Number(day) };
  return fields;
}
export default function CronForm({
  initial,
  project,
  onSaved,
  onCancel,
}: {
  initial?: Cron;
  project?: Pick<Website, 'id' | 'name' | 'projectType'>;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const [form] = Form.useForm<CronInput>();
  const { token } = theme.useToken();
  const [savedCycle] = useState(() => scheduleFields(initial?.schedule));
  const [cycle, setCycle] = useState(savedCycle.cycle);
  const [hour, setHour] = useState(savedCycle.hour);
  const [minute, setMinute] = useState(savedCycle.minute);
  const [step, setStep] = useState(savedCycle.step);
  const [week, setWeek] = useState(savedCycle.week);
  const [day, setDay] = useState(savedCycle.day);
  const [custom, setCustom] = useState(initial?.schedule ?? '0 3 * * *');
  const [script, setScript] = useState(initial?.script ?? '#!/bin/bash\n');
  const [busy, setBusy] = useState(false);
  const [choosingDirectory, setChoosingDirectory] = useState<
    'directoryPath' | 'backupDirectory'
  >();
  const type =
    Form.useWatch('type', form) ??
    initial?.type ??
    (project ? 'restart_project' : 'shell');
  const target = Form.useWatch('targetId', form);
  const storage =
    Form.useWatch('backupStorageId', form) ??
    initial?.backupStorageId ??
    'local';
  const keepLocal =
    Form.useWatch('keepLocal', form) ?? initial?.keepLocal ?? false;
  const showLocalBackup = storage === 'local' || keepLocal;
  const isBackup = ['backup_site', 'backup_db', 'backup_directory'].includes(
    type,
  );
  const storages = useQuery({
    queryKey: ['backup-storages'],
    queryFn: backupStorages,
    enabled: isBackup,
  });
  const settings = useQuery({
    queryKey: ['panel-settings'],
    queryFn: panelSettings,
    enabled: isBackup && showLocalBackup,
  });
  const defaultBackupRoot = settings.data?.backupRoot;
  useEffect(() => {
    if (
      isBackup &&
      showLocalBackup &&
      defaultBackupRoot &&
      !form.getFieldValue('backupDirectory')
    )
      form.setFieldValue('backupDirectory', defaultBackupRoot);
  }, [isBackup, showLocalBackup, defaultBackupRoot, form]);
  const sites = useQuery({
    queryKey: ['cron-site-targets', type],
    queryFn: () => cronSites(type === 'restart_project'),
    enabled: !project && (type === 'backup_site' || type === 'restart_project'),
  });
  const dbServers = useQuery({
    queryKey: ['db-servers'],
    queryFn: servers,
    enabled: type === 'backup_db',
  });
  const dbs = useQuery({
    queryKey: ['cron-db-targets', target],
    queryFn: () => databaseList(target!),
    enabled: type === 'backup_db' && !!target,
  });
  const expression = useMemo(() => {
    switch (cycle) {
      case 'daily':
        return `${minute} ${hour} * * *`;
      case 'hourly':
        return `${minute} * * * *`;
      case 'hours':
        return `${minute} */${step} * * *`;
      case 'minutes':
        return `*/${step} * * * *`;
      case 'weekly':
        return `${minute} ${hour} * * ${week}`;
      case 'monthly':
        return `${minute} ${hour} ${day} * *`;
      default:
        return custom;
    }
  }, [cycle, minute, hour, step, week, day, custom]);
  const submit = async (values: CronInput) => {
    setBusy(true);
    try {
      const data: CronInput = {
        name: values.name,
        type: values.type,
        schedule: expression,
      };
      if (type === 'shell') {
        data.script = script;
        data.timeoutSec = values.timeoutSec;
      } else if (type === 'backup_site') {
        data.allSites = values.targetId === 'all';
        if (!data.allSites) data.targetId = values.targetId;
        data.keepCount = values.keepCount;
      } else if (type === 'backup_db') {
        data.targetId = values.targetId;
        data.allDatabases = values.targetName === allDatabasesValue;
        if (!data.allDatabases) data.targetName = values.targetName;
        data.keepCount = values.keepCount;
      } else if (type === 'backup_directory') {
        data.directoryPath = values.directoryPath;
        data.keepCount = values.keepCount;
      } else if (type === 'restart_project') {
        data.targetId = values.targetId;
      } else if (type === 'url') {
        data.url = values.url;
        data.urlMethod = values.urlMethod;
        data.userAgent = values.userAgent;
        if (values.urlMethod === 'POST') data.urlParams = values.urlParams;
      } else if (type === 'renew_certificate')
        data.renewBeforeDays = values.renewBeforeDays;
      if (isBackup) {
        data.backupStorageId = values.backupStorageId;
        data.keepLocal = values.backupStorageId !== 'local' && values.keepLocal;
        if (values.backupStorageId === 'local' || data.keepLocal)
          data.backupDirectory = values.backupDirectory;
        if (type !== 'backup_db') data.excludeRules = values.excludeRules;
      }
      await saveCron(initial?.id, data);
      onSaved();
    } finally {
      setBusy(false);
    }
  };
  return (
    <Form
      form={form}
      layout="horizontal"
      labelCol={{ flex: LAYOUT.formLabelWidth }}
      wrapperCol={{ flex: 1, style: { minWidth: 0 } }}
      labelWrap
      colon={false}
      scrollToFirstError={{ block: 'nearest' }}
      style={{
        display: 'flex',
        flexDirection: 'column',
        minHeight: 0,
        width: '100%',
      }}
      onFinish={(values) => void submit(values).catch(() => {})}
      initialValues={
        initial
          ? {
              ...initial,
              targetId: initial.allSites ? 'all' : initial.targetId,
              targetName: initial.allDatabases
                ? allDatabasesValue
                : initial.targetName,
              renewBeforeDays: initial.renewBeforeDays ?? 15,
              backupStorageId: initial.backupStorageId || 'local',
              urlMethod: initial.urlMethod || 'GET',
              urlParams: initial.urlParams ?? [],
            }
          : {
              type: project ? 'restart_project' : 'shell',
              name: project
                ? `重启项目[${project.name}]`.slice(0, 64)
                : undefined,
              targetId: project?.id,
              keepCount: 3,
              timeoutSec: 3600,
              renewBeforeDays: 15,
              backupStorageId: 'local',
              urlMethod: 'GET',
              urlParams: [],
            }
      }
    >
      <div
        style={{ overflowY: 'auto', minHeight: 0, paddingRight: SPACING.xs }}
      >
        <Form.Item name="type" label="任务类型" rules={[{ required: true }]}>
          <Select
            style={{ width: LAYOUT.cronTypeWidth, maxWidth: '100%' }}
            disabled={!!project}
            options={cronTypes}
            onChange={(value) => {
              // 切换类型后只保留通用字段，避免带入上一个任务的目标和校验错误。
              form.setFields([
                {
                  name: 'name',
                  value: cronTypes.find((item) => item.value === value)?.label,
                  errors: [],
                },
                { name: 'targetId', value: undefined, errors: [] },
                { name: 'targetName', value: undefined, errors: [] },
                { name: 'url', value: undefined, errors: [] },
                { name: 'urlMethod', value: 'GET', errors: [] },
                { name: 'userAgent', value: undefined, errors: [] },
                { name: 'urlParams', value: [], errors: [] },
                {
                  name: 'backupDirectory',
                  value: defaultBackupRoot,
                  errors: [],
                },
                { name: 'keepLocal', value: false, errors: [] },
                { name: 'excludeRules', value: undefined, errors: [] },
                { name: 'keepCount', value: 3, errors: [] },
                { name: 'timeoutSec', value: 3600, errors: [] },
                { name: 'renewBeforeDays', value: 15, errors: [] },
                { name: 'directoryPath', value: undefined, errors: [] },
                { name: 'backupStorageId', value: 'local', errors: [] },
              ]);
              setScript('#!/bin/bash\n');
            }}
          />
        </Form.Item>
        <Form.Item
          name="name"
          label="任务名称"
          rules={[{ required: true, max: 64 }]}
        >
          <Input placeholder="请输入任务名称" maxLength={64} />
        </Form.Item>
        <Form.Item label="执行周期">
          <Flex
            vertical
            gap={SPACING.sm}
            style={{ width: '100%', minWidth: 0 }}
          >
            <Flex gap={SPACING.sm} wrap align="center">
              <Select
                value={cycle}
                aria-label="执行周期"
                className="cron-cycle-select"
                options={[
                  { value: 'daily', label: '每天' },
                  { value: 'hourly', label: '每小时' },
                  { value: 'hours', label: '每 N 小时' },
                  { value: 'minutes', label: '每 N 分钟' },
                  { value: 'weekly', label: '每周' },
                  { value: 'monthly', label: '每月' },
                  { value: 'custom', label: '自定义' },
                ]}
                onChange={(value) => {
                  setCycle(value);
                  setStep(2);
                }}
              />
              {(cycle === 'hours' || cycle === 'minutes') && (
                <Select
                  value={step}
                  aria-label="执行间隔"
                  className="cron-cycle-select"
                  options={(cycle === 'hours' ? hourSteps : minuteSteps).map(
                    (value) => ({
                      value,
                      label: `每 ${value} ${cycle === 'hours' ? '小时' : '分钟'}`,
                    }),
                  )}
                  onChange={setStep}
                />
              )}
              {cycle === 'weekly' && (
                <Select
                  value={week}
                  aria-label="星期"
                  className="cron-cycle-number"
                  options={[0, 1, 2, 3, 4, 5, 6].map((value) => ({
                    value,
                    label: [
                      '周日',
                      '周一',
                      '周二',
                      '周三',
                      '周四',
                      '周五',
                      '周六',
                    ][value],
                  }))}
                  onChange={setWeek}
                />
              )}
              {cycle === 'monthly' && (
                <InputNumber
                  aria-label="每月日期"
                  className="cron-cycle-number"
                  min={1}
                  max={31}
                  value={day}
                  onChange={(value) => setDay(value ?? 1)}
                  suffix="日"
                />
              )}
              {['daily', 'weekly', 'monthly'].includes(cycle) && (
                <InputNumber
                  aria-label="小时"
                  className="cron-cycle-number"
                  min={0}
                  max={23}
                  value={hour}
                  onChange={(value) => setHour(value ?? 0)}
                  suffix="时"
                />
              )}
              {cycle !== 'custom' && cycle !== 'minutes' && (
                <InputNumber
                  aria-label="分钟"
                  className="cron-cycle-number"
                  min={0}
                  max={59}
                  value={minute}
                  onChange={(value) => setMinute(value ?? 0)}
                  suffix="分"
                />
              )}
              {cycle === 'custom' && (
                <Input
                  aria-label="Cron 表达式"
                  value={custom}
                  style={{ width: 240, maxWidth: '100%' }}
                  onChange={(e) => setCustom(e.target.value)}
                  placeholder="分 时 日 月 周"
                />
              )}
            </Flex>
            {cycle === 'monthly' && day >= 29 && (
              <Alert
                type="warning"
                message="29至31日在没有该日期的月份不会执行。"
              />
            )}
            {cycle === 'custom' && (
              <Alert
                type="info"
                message="日与周同时限制时，满足任一条件即可执行；保存时按服务器时区校验。"
              />
            )}
          </Flex>
        </Form.Item>
        {type === 'shell' && (
          <>
            <Form.Item label="Shell 脚本">
              <div
                className="cron-script-editor"
                style={{
                  height: LAYOUT.cronEditorHeight,
                  border: `1px solid ${token.colorBorder}`,
                  borderRadius: token.borderRadius,
                  overflow: 'hidden',
                }}
              >
                <Suspense fallback={<Skeleton active />}>
                  <CodeEditor
                    path={`cron-${initial?.id ?? 'new'}.sh`}
                    value={script}
                    onChange={setScript}
                  />
                </Suspense>
              </div>
            </Form.Item>
            <Form.Item name="timeoutSec" label="超时秒数">
              <InputNumber min={1} max={86400} />
            </Form.Item>
          </>
        )}
        {type === 'backup_site' && (
          <Form.Item
            name="targetId"
            label="静态网站"
            rules={[{ required: true }]}
          >
            <Select
              placeholder="请选择要备份的网站"
              loading={sites.isFetching}
              options={[
                { value: 'all', label: '所有静态网站' },
                ...(sites.data ?? []).map((site) => ({
                  value: site.id,
                  label: site.name,
                })),
              ]}
              onChange={(value) =>
                form.setFieldValue(
                  'name',
                  `备份网站[${value === 'all' ? '所有静态网站' : (sites.data?.find((s) => s.id === value)?.name ?? value)}]`,
                )
              }
            />
          </Form.Item>
        )}
        {type === 'restart_project' && (
          <Form.Item name="targetId" label="项目" rules={[{ required: true }]}>
            <Select
              placeholder="请选择要重启的项目"
              disabled={!!project}
              loading={sites.isFetching}
              options={(project ? [project] : (sites.data ?? [])).map(
                (site) => ({
                  value: site.id,
                  label: `[${projectLabels[site.projectType]}] ${site.name}`,
                }),
              )}
            />
          </Form.Item>
        )}
        {type === 'backup_db' && (
          <>
            <Form.Item
              name="targetId"
              label="数据库服务器"
              rules={[{ required: true }]}
            >
              <Select
                placeholder="请选择数据库服务器"
                loading={dbServers.isFetching}
                options={dbServers.data?.map((s) => ({
                  value: s.id,
                  label: s.name,
                }))}
                onChange={() =>
                  form.setFields([
                    { name: 'targetName', value: undefined, errors: [] },
                  ])
                }
              />
            </Form.Item>
            <Form.Item
              name="targetName"
              label="数据库"
              rules={[{ required: true }]}
            >
              <Select
                placeholder={target ? '请选择数据库' : '请先选择数据库服务器'}
                disabled={!target}
                loading={dbs.isFetching}
                options={[
                  { value: allDatabasesValue, label: '所有业务数据库' },
                  ...(dbs.data ?? [])
                    .filter((database) => !database.system)
                    .map((database) => ({ value: database.name })),
                ]}
                onChange={(value) =>
                  form.setFieldValue(
                    'name',
                    `备份数据库[${value === allDatabasesValue ? '所有业务数据库' : value}]`,
                  )
                }
              />
            </Form.Item>
          </>
        )}
        {(((type === 'backup_site' || type === 'restart_project') &&
          sites.error) ||
          (type === 'backup_db' && (dbServers.error || dbs.error))) && (
          <Form.Item label=" ">
            <Alert
              type="error"
              message="目标列表读取失败，请重新打开表单重试。"
            />
          </Form.Item>
        )}
        {type === 'backup_directory' && (
          <Form.Item label="备份目录" required htmlFor="directoryPath">
            <Space.Compact block>
              <Form.Item
                name="directoryPath"
                noStyle
                rules={[{ required: true, message: '请选择要备份的目录' }]}
              >
                <Input
                  readOnly
                  placeholder="请选择要备份的目录"
                  onClick={() => setChoosingDirectory('directoryPath')}
                />
              </Form.Item>
              <Button
                icon={<FolderOpenOutlined />}
                aria-label="选择备份目录"
                onClick={() => setChoosingDirectory('directoryPath')}
              />
            </Space.Compact>
          </Form.Item>
        )}
        {isBackup && (
          <Form.Item
            name="backupStorageId"
            label="备份位置"
            rules={[{ required: true, message: '请选择备份位置' }]}
            validateStatus={storages.isError ? 'error' : undefined}
            help={storages.error?.message}
          >
            <Select
              loading={storages.isFetching}
              options={
                storages.data?.map((storage) => ({
                  value: storage.id,
                  label: storage.name,
                })) ?? [{ value: 'local', label: '本地磁盘' }]
              }
            />
          </Form.Item>
        )}
        {isBackup && storage !== 'local' && (
          <Form.Item label="备份设置" name="keepLocal" valuePropName="checked">
            <Checkbox>同时保留本地备份（与远程保留份数一致）</Checkbox>
          </Form.Item>
        )}
        {isBackup && showLocalBackup && (
          <Form.Item label="本地备份" required htmlFor="backupDirectory">
            <Space.Compact block>
              <Form.Item
                name="backupDirectory"
                noStyle
                rules={[
                  {
                    required: true,
                    whitespace: true,
                    message: '请选择本地备份目录',
                  },
                ]}
              >
                <Input
                  readOnly
                  onClick={() => setChoosingDirectory('backupDirectory')}
                  placeholder={
                    settings.isFetching
                      ? '正在读取默认备份路径'
                      : '请选择本地备份目录'
                  }
                />
              </Form.Item>
              <Button
                icon={<FolderOpenOutlined />}
                aria-label="选择本地备份目录"
                onClick={() => setChoosingDirectory('backupDirectory')}
              />
            </Space.Compact>
          </Form.Item>
        )}
        {(type === 'backup_site' || type === 'backup_directory') && (
          <Form.Item name="excludeRules" label="排除规则">
            <Input.TextArea
              rows={4}
              maxLength={8192}
              placeholder={
                '每行一条相对路径或通配符，目录末尾不加斜杠\ndata/config.php\nstatic/upload\n*.log'
              }
            />
          </Form.Item>
        )}
        {isBackup && (
          <Form.Item
            name="keepCount"
            label="保留份数"
            extra="仅保留本计划、本目标最新备份份数"
            rules={[{ required: true, message: '请输入保留份数' }]}
          >
            <InputNumber min={1} max={100} precision={0} suffix="份" />
          </Form.Item>
        )}
        {type === 'backup_db' && (
          <Form.Item label=" ">
            <Alert
              type="info"
              message="数据库单事务备份不保证非事务表或备份期间DDL的一致性；不包含账号权限、存储过程与事件。"
            />
          </Form.Item>
        )}
        {type === 'url' && <URLFields />}
        {type === 'renew_certificate' && (
          <Form.Item
            name="renewBeforeDays"
            label="提前续签"
            rules={[
              {
                required: true,
                type: 'number',
                min: 1,
                max: 90,
                message: '请输入1至90天',
              },
            ]}
          >
            <InputNumber
              min={1}
              max={90}
              precision={0}
              suffix="天"
              style={{ width: LAYOUT.cronTypeWidth, maxWidth: '100%' }}
            />
          </Form.Item>
        )}
        {type === 'free_memory' && (
          <Form.Item label=" ">
            <Alert
              type="warning"
              message="执行 sync 并清理 PageCache、dentries 和 inodes 缓存，仅清理可回收缓存，不保证提高性能或降低进程内存，不建议默认周期执行。"
            />
          </Form.Item>
        )}
      </div>
      {choosingDirectory && (
        <DirectoryPicker
          initialPath={
            form.getFieldValue(choosingDirectory) ||
            (choosingDirectory === 'backupDirectory'
              ? defaultBackupRoot
              : settings.data?.siteRoot) ||
            '/'
          }
          onSelect={(path) => {
            form.setFieldValue(choosingDirectory, path);
            setChoosingDirectory(undefined);
          }}
          onClose={() => setChoosingDirectory(undefined)}
        />
      )}
      <Flex
        justify="flex-end"
        gap={SPACING.sm}
        style={{
          flexShrink: 0,
          marginTop: SPACING.lg,
          paddingTop: SPACING.lg,
          borderTop: `${token.lineWidth}px ${token.lineType} ${token.colorSplit}`,
        }}
      >
        <Button onClick={onCancel} disabled={busy}>
          取消
        </Button>
        <Button type="primary" htmlType="submit" loading={busy}>
          {initial?.id ? '保存修改' : '添加任务'}
        </Button>
      </Flex>
    </Form>
  );
}
