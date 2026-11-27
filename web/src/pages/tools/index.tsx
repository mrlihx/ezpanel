import { lazy, Suspense, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { useRoutedTab } from '../../hooks/useRoutedTab';
import RouteNotFound from '../../router/RouteNotFound';
import { useQuery } from '@tanstack/react-query';
import { Alert, App, Button, Form, Card, Skeleton, Space } from 'antd';
import SectionTabs from '../../components/SectionTabs';
import {
  toolboxData,
  saveToolbox,
  type DNSResult,
  type MirrorResult,
} from '../../api/toolbox';
import { LAYOUT, SPACING } from '../../styles/tokens';
import TaskDrawer from '../../components/TaskDrawer';
import './tools.css';
const TerminalModal = lazy(() => import('../file/TerminalModal'));
const sections = [
  ['ip', 'IP 设置'],
  ['swap', 'Swap/虚拟内存'],
  ['time', '时区设置'],
  ['password', '系统密码'],
  ['disks', '磁盘挂载'],
  ['hosts', 'hosts'],
  ['mirrors', '镜像源'],
  ['commands', '常用命令'],
  ['audit', '命令审计'],
] as const;
const ToolIpPanel = lazy(() => import('./ToolIpPanel'));
const ToolSwapPanel = lazy(() => import('./ToolSwapPanel'));
const ToolTimePanel = lazy(() => import('./ToolTimePanel'));
const ToolPasswordPanel = lazy(() => import('./ToolPasswordPanel'));
const ToolDisksPanel = lazy(() => import('./ToolDisksPanel'));
const ToolHostsPanel = lazy(() => import('./ToolHostsPanel'));
const ToolMirrorsPanel = lazy(() => import('./ToolMirrorsPanel'));
const ToolCommandsPanel = lazy(() => import('./ToolCommandsPanel'));
const ToolAuditPanel = lazy(() => import('./ToolAuditPanel'));
function useToolboxPage() {
  const { message, modal } = App.useApp();
  const [params] = useSearchParams();
  const { section: requested } = useParams();
  const route = useRoutedTab(
    'tools',
    '/tools',
    requested,
    sections.map(([key]) => key),
    {
      keys: ['tab'],
      tab: params.get('tab'),
    },
  );
  const section = route.tab;
  const [busy, setBusy] = useState(false);
  const [dnsForm] = Form.useForm();
  const [passwordForm] = Form.useForm();
  const [dnsRows, setDNSRows] = useState<DNSResult[]>();
  const [mirrorKind, setMirrorKind] = useState('apt');
  const [mirrorRows, setMirrorRows] = useState<MirrorResult[]>();
  const [mirrorNotice, setMirrorNotice] = useState('');
  const [hostsDraft, setHostsDraft] = useState<string>();
  const [auditPage, setAuditPage] = useState(1);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [terminal, setTerminal] = useState<string>();
  const [editingCommand, setEditingCommand] = useState<number | 'new'>();
  const dns = useQuery({
    queryKey: ['toolbox', 'dns'],
    queryFn: () => toolboxData('dns'),
    enabled: section === 'ip',
  });
  const ip = useQuery({
    queryKey: ['toolbox', 'ip'],
    queryFn: () => toolboxData('ip'),
    enabled: section === 'ip',
    refetchInterval: (query) => (query.state.data?.trial ? 3000 : false),
  });
  const swap = useQuery({
    queryKey: ['toolbox', 'swap'],
    queryFn: () => toolboxData('swap'),
    enabled: section === 'swap',
  });
  const time = useQuery({
    queryKey: ['toolbox', 'time'],
    queryFn: () => toolboxData('time'),
    enabled: section === 'time',
  });
  const password = useQuery({
    queryKey: ['toolbox', 'password'],
    queryFn: () => toolboxData('password'),
    enabled: section === 'password',
  });
  const disks = useQuery({
    queryKey: ['toolbox', 'disks'],
    queryFn: () => toolboxData('disks'),
    enabled: section === 'disks',
  });
  const hosts = useQuery({
    queryKey: ['toolbox', 'hosts'],
    queryFn: () => toolboxData('hosts'),
    enabled: section === 'hosts',
  });
  const mirrors = useQuery({
    queryKey: ['toolbox', 'mirrors'],
    queryFn: () => toolboxData('mirrors'),
    enabled: section === 'mirrors',
  });
  const currentMirror =
    mirrorKind === 'apt'
      ? mirrors.data?.systemProvider
      : mirrors.data?.providers.find(
          (provider) =>
            provider.pip.replace(/\/+$/, '') ===
            mirrors.data?.pipIndex.replace(/\/+$/, ''),
        )?.id;
  const mirrorAddresses =
    mirrorKind === 'apt'
      ? mirrors.data?.systemSources
      : [mirrors.data?.pipIndex].filter(Boolean);
  const selectedMirror =
    currentMirror || (mirrorAddresses?.length ? 'current' : undefined);
  const commands = useQuery({
    queryKey: ['toolbox', 'commands'],
    queryFn: () => toolboxData('commands'),
    enabled: section === 'commands',
  });
  const audit = useQuery({
    queryKey: ['toolbox', 'audit', auditPage],
    queryFn: () => toolboxData('audit', auditPage),
    enabled: section === 'audit',
  });
  const queries: Record<
    string,
    { error: Error | null; isPending: boolean; refetch: () => Promise<unknown> }
  > = {
    dns,
    ip,
    swap,
    time,
    password,
    disks,
    hosts,
    mirrors,
    commands,
    audit,
  };
  const formProps = {
    layout: 'horizontal' as const,
    labelCol: { flex: LAYOUT.formLabelWidth },
    wrapperCol: { flex: 1 },
    colon: false,
  };
  async function save(key: Parameters<typeof saveToolbox>[0], data: unknown) {
    setBusy(true);
    try {
      await saveToolbox(key, data);
      message.success('设置已保存');
      if (key === 'hosts') setHostsDraft(undefined);
      await queries[key]?.refetch();
    } finally {
      setBusy(false);
    }
  }
  function confirm(
    title: string,
    content: string,
    run: () => Promise<unknown>,
  ) {
    modal.confirm({ title, content, onOk: run });
  }
  return {
    audit,
    auditPage,
    busy,
    commands,
    confirm,
    currentMirror,
    disks,
    dns,
    dnsForm,
    dnsRows,
    editingCommand,
    formProps,
    hosts,
    hostsDraft,
    ip,
    route,
    message,
    mirrorAddresses,
    mirrorKind,
    mirrorNotice,
    mirrorRows,
    mirrors,
    modal,
    password,
    passwordForm,
    queries,
    save,
    section,
    selectedMirror,
    setAuditPage,
    setBusy,
    setDNSRows,
    setEditingCommand,
    setHostsDraft,
    setMirrorKind,
    setMirrorNotice,
    setMirrorRows,

    setTaskId,
    setTerminal,
    swap,
    taskId,
    terminal,
    time,
  };
}
export type ToolboxPageState = ReturnType<typeof useToolboxPage>;
export default function ToolsPage() {
  const {
    audit,
    auditPage,
    busy,
    commands,
    confirm,
    currentMirror,
    disks,
    dns,
    dnsForm,
    dnsRows,
    editingCommand,
    formProps,
    hosts,
    hostsDraft,
    ip,
    route,
    message,
    mirrorAddresses,
    mirrorKind,
    mirrorNotice,
    mirrorRows,
    mirrors,
    modal,
    password,
    passwordForm,
    queries,
    save,
    section,
    selectedMirror,
    setAuditPage,
    setBusy,
    setDNSRows,
    setEditingCommand,
    setHostsDraft,
    setMirrorKind,
    setMirrorNotice,
    setMirrorRows,

    setTaskId,
    setTerminal,
    swap,
    taskId,
    terminal,
    time,
  } = useToolboxPage();
  if (route.invalid) return <RouteNotFound back="/tools" />;
  if (route.loading) return <Skeleton active />;
  const panels = {
    ip: (
      <ToolIpPanel
        busy={busy}
        confirm={confirm}
        dns={dns}
        dnsForm={dnsForm}
        dnsRows={dnsRows}
        formProps={formProps}
        ip={ip}
        modal={modal}
        save={save}
        setBusy={setBusy}
        setDNSRows={setDNSRows}
      />
    ),
    swap: (
      <ToolSwapPanel
        busy={busy}
        confirm={confirm}
        formProps={formProps}
        save={save}
        swap={swap}
      />
    ),
    time: (
      <ToolTimePanel
        busy={busy}
        formProps={formProps}
        message={message}
        save={save}
        setBusy={setBusy}
        time={time}
      />
    ),
    password: (
      <ToolPasswordPanel
        busy={busy}
        confirm={confirm}
        formProps={formProps}
        password={password}
        passwordForm={passwordForm}
        save={save}
      />
    ),
    disks: <ToolDisksPanel disks={disks} />,
    hosts: (
      <ToolHostsPanel
        busy={busy}
        confirm={confirm}
        hosts={hosts}
        hostsDraft={hostsDraft}
        save={save}
        setHostsDraft={setHostsDraft}
      />
    ),
    mirrors: (
      <ToolMirrorsPanel
        busy={busy}
        confirm={confirm}
        currentMirror={currentMirror}
        mirrorAddresses={mirrorAddresses}
        mirrorKind={mirrorKind}
        mirrorNotice={mirrorNotice}
        mirrorRows={mirrorRows}
        mirrors={mirrors}
        save={save}
        selectedMirror={selectedMirror}
        setBusy={setBusy}
        setMirrorKind={setMirrorKind}
        setMirrorNotice={setMirrorNotice}
        setMirrorRows={setMirrorRows}
      />
    ),
    commands: (
      <ToolCommandsPanel
        busy={busy}
        commands={commands}
        confirm={confirm}
        editingCommand={editingCommand}
        save={save}
        setEditingCommand={setEditingCommand}
        setTerminal={setTerminal}
      />
    ),
    audit: (
      <ToolAuditPanel
        audit={audit}
        auditPage={auditPage}
        busy={busy}
        confirm={confirm}
        save={save}
        setAuditPage={setAuditPage}
        setTaskId={setTaskId}
      />
    ),
  };
  return (
    <div
      className={
        section === 'audit' ? 'tools-page tools-page-audit' : 'tools-page'
      }
    >
      <SectionTabs
        activeKey={section}
        onChange={route.selectTab}
        items={sections.map(([key, label]) => {
          const current = queries[key];
          const inner = (
            <>
              {current.error && (
                <Alert
                  type="error"
                  message={current.error.message}
                  action={
                    <Button onClick={() => void current.refetch()}>重试</Button>
                  }
                />
              )}
              {current.isPending ? (
                <Skeleton active />
              ) : (
                <Suspense fallback={<Skeleton active />}>
                  {panels[key]}
                </Suspense>
              )}
            </>
          );
          const content =
            key === 'audit' ? (
              <div className="tools-audit-body">{inner}</div>
            ) : (
              <Space
                direction="vertical"
                size={SPACING.lg}
                style={{ width: '100%' }}
              >
                {inner}
              </Space>
            );
          return {
            key,
            label,
            children: key === 'ip' ? content : <Card>{content}</Card>,
          };
        })}
      />
      <TaskDrawer
        taskId={taskId}
        open={!!taskId}
        title="安装命令审计组件"
        onClose={() => setTaskId(null)}
        onSettled={() => void audit.refetch()}
      />
      {terminal !== undefined && (
        <Suspense fallback={null}>
          <TerminalModal
            cwd="/root"
            initialInput={terminal}
            onClose={() => setTerminal(undefined)}
          />
        </Suspense>
      )}
    </div>
  );
}
