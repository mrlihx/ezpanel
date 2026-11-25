import { firewallRuleNames, firewallRuleOriginLabel } from './firewallLabels';
import { lazy, Suspense, useState } from 'react';
import {
  Alert,
  Skeleton,
  App,
  Button,
  Card,
  Divider,
  Form,
  Input,
  Space,
  Switch,
  Tabs,
  Tooltip,
  Typography,
  Grid,
} from 'antd';
import { RotateCw } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import dayjs from 'dayjs';
import {
  firewall,
  saveFirewall,
  updateFirewallIPPools,
  type FirewallRule,
} from '../../api/security';
import { countryLabel } from './countries';
import { saveBlob } from '../../utils/download';
function download(name: string, content: string) {
  saveBlob(content, name, 'application/json');
}
const FirewallRuleTable = lazy(() => import('./FirewallRuleTable'));
const FirewallRuleEditor = lazy(() => import('./FirewallRuleEditor'));
const FirewallRuleImport = lazy(() => import('./FirewallRuleImport'));
const FirewallPortDetail = lazy(() => import('./FirewallPortDetail'));
function useFirewall() {
  const fixedActions = Grid.useBreakpoint().lg ? ('right' as const) : undefined;
  const { message, modal } = App.useApp();
  const query = useQuery({
    queryKey: ['security-firewall'],
    queryFn: firewall,
  });
  const [kind, setKind] = useState<FirewallRule['kind']>('port');
  const [keyword, setKeyword] = useState('');
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [batchAction, setBatchAction] = useState('delete');
  const [editing, setEditing] = useState<FirewallRule | 'new'>();
  const [working, setWorking] = useState(false);
  const [importing, setImporting] = useState(false);
  const [json, setJSON] = useState('');
  const [detail, setDetail] = useState<FirewallRule>();
  const [form] = Form.useForm();
  const peerMode = Form.useWatch('peerMode', form);
  const portMode = Form.useWatch('portMode', form);
  const [updatingPool, setUpdatingPool] = useState(false);
  const config = query.data?.config;
  const refresh = () => void query.refetch();
  const change = async (
    values: Partial<{
      enabled: boolean;
      blockPing: boolean;
      scanProtection: boolean;
    }>,
  ) => {
    if (!config) return;
    setWorking(true);
    try {
      await saveFirewall({ ...config, ...values });
      await query.refetch();
      message.success('设置已保存');
    } finally {
      setWorking(false);
    }
  };
  const open = (row: FirewallRule | 'new') => {
    setEditing(row);
    form.resetFields();
    form.setFieldsValue(
      row === 'new'
        ? {
            enabled: true,
            protocol: 'tcp',
            policy: kind === 'port' ? 'allow' : 'deny',
            direction: kind === 'region' ? 'both' : 'in',
            peerMode: kind === 'ip' ? 'specified' : 'all',
            peer: '',
            countries: [],
            portMode: 'all',
            targetPort: 80,
          }
        : {
            ...row,
            peerMode: row.peer === 'any' ? 'all' : 'specified',
            peer: row.peer === 'any' ? '' : row.peer,
            countries: row.country.split(',').filter(Boolean),
            portMode: !row.ports || row.ports === 'all' ? 'all' : 'specified',
            ports: !row.ports || row.ports === 'all' ? '' : row.ports,
            expiresAt: row.expiresAt ? dayjs(row.expiresAt) : null,
          },
    );
  };
  const displayedRules = query.data?.rules ?? [];
  const rows = displayedRules
    .map((row, index) => ({ row, index }))
    .filter(
      ({ row }) =>
        row.kind === kind &&
        `${row.ports} ${row.peer} ${row.country} ${row.kind === 'region' ? countryLabel(row.country) : ''} ${row.remark} ${row.targetIP} ${row.origin ? firewallRuleOriginLabel(row.origin) : ''}`
          .toLowerCase()
          .includes(keyword.toLowerCase()),
    )
    .sort((a, b) => {
      const timeDiff =
        (Date.parse(b.row.createdAt || '') || 0) -
        (Date.parse(a.row.createdAt || '') || 0);
      return timeDiff !== 0 ? timeDiff : b.index - a.index;
    })
    .map(({ row }) => row);
  const expired = (row: FirewallRule) =>
    !!row.expiresAt && Date.parse(row.expiresAt) <= Date.now();
  const listening = (row: FirewallRule) =>
    query.data?.listeners.filter(
      (listener) =>
        (row.protocol === 'both' || listener.protocol === row.protocol) &&
        (row.ports === 'all' ||
          row.ports.split(',').some((part) => {
            const [a, b] = part.split('-').map(Number);
            return listener.port >= a && listener.port <= (b ?? a);
          })),
    );

  return {
    batchAction,
    change,
    config,
    detail,
    displayedRules,
    editing,
    expired,
    fixedActions,
    form,
    importing,
    json,
    kind,
    listening,
    message,
    modal,
    open,
    peerMode,
    portMode,
    query,
    refresh,
    rows,
    selected,
    setBatchAction,
    setDetail,
    setEditing,
    setImporting,
    setJSON,
    setKeyword,
    setKind,
    setSelected,
    setUpdatingPool,
    setWorking,
    updatingPool,
    working,
  };
}
export type FirewallPageState = ReturnType<typeof useFirewall>;
export default function Firewall() {
  const {
    batchAction,
    change,
    config,
    detail,
    displayedRules,
    editing,
    expired,
    fixedActions,
    form,
    importing,
    json,
    kind,
    listening,
    message,
    modal,
    open,
    peerMode,
    portMode,
    query,
    refresh,
    rows,
    selected,
    setBatchAction,
    setDetail,
    setEditing,
    setImporting,
    setJSON,
    setKeyword,
    setKind,
    setSelected,
    setUpdatingPool,
    setWorking,
    updatingPool,
    working,
  } = useFirewall();
  return (
    <Card className="security-table">
      {query.error && (
        <Alert
          type="error"
          message={query.error.message}
          action={<Button onClick={refresh}>重试</Button>}
        />
      )}
      {query.data && !query.data.available && (
        <Alert
          type="warning"
          message="未安装nftables，请先通过发行版包管理器安装nftables。"
        />
      )}
      {!!query.data?.externalFirewalls.length && (
        <Alert
          type="info"
          message={`检测到${query.data.externalFirewalls.join('、')}正在管理其他规则。此处仅管理面板规则，放行仍受其他防火墙限制。`}
        />
      )}
      <div className="security-toolbar security-status-strip">
        <Space>
          防火墙开关
          <Switch
            checked={query.data?.active ?? false}
            disabled={!query.data?.available}
            loading={working}
            onChange={(enabled) =>
              modal.confirm({
                title: enabled ? '启用系统防火墙？' : '关闭系统防火墙？',
                content: enabled
                  ? '启用后入站默认拒绝，出站默认放行。SSH和面板管理端口自动保留；网站等其他业务端口请先添加放行规则。'
                  : '关闭面板受管规则，系统其他防火墙保持原状。',
                onOk: () => change({ enabled }),
              })
            }
          />
        </Space>
        <Divider type="vertical" />
        <Space>
          禁ping
          <Switch
            checked={config?.blockPing ?? false}
            loading={working}
            disabled={!config}
            onChange={(blockPing) => void change({ blockPing })}
          />
        </Space>
        <Divider type="vertical" />
        <Tooltip title="重新读取规则和运行状态">
          <Button onClick={refresh} loading={query.isFetching}>
            清理缓存
          </Button>
        </Tooltip>
        <Tooltip title="刷新">
          <Button
            style={{ marginLeft: 'auto' }}
            icon={<RotateCw size={16} strokeWidth={1.9} />}
            aria-label="刷新防火墙"
            loading={query.isFetching}
            onClick={refresh}
          />
        </Tooltip>
      </div>
      <Typography.Paragraph type="secondary">
        管理端口：SSH {query.data?.sshPorts.join('、') ?? '—'}；面板{' '}
        {query.data?.panelPort ?? '—'}
        。当前管理IP对这些端口自动放行。规则只在防火墙启用后生效。
      </Typography.Paragraph>
      <Divider />
      <Tabs
        type="card"
        activeKey={kind}
        onChange={(key) => {
          setKind(key as FirewallRule['kind']);
          setSelected([]);
          setKeyword('');
        }}
        items={(Object.keys(firewallRuleNames) as FirewallRule['kind'][]).map(
          (key) => ({
            key,
            label: `${firewallRuleNames[key]}：${displayedRules.filter((row) => row.kind === key).length}`,
          }),
        )}
      />
      <div className="security-toolbar">
        <Button
          type="primary"
          disabled={!query.data?.available}
          onClick={() => open('new')}
        >
          添加{firewallRuleNames[kind]}
        </Button>
        <Button
          onClick={() => {
            setJSON('');
            setImporting(true);
          }}
        >
          导入规则
        </Button>
        <Button
          onClick={() =>
            download(
              `firewall-${kind}.json`,
              JSON.stringify(
                config?.rules.filter((row) => row.kind === kind) ?? [],
                null,
                2,
              ),
            )
          }
        >
          导出规则
        </Button>
        {kind === 'region' && (
          <Button
            loading={updatingPool}
            disabled={!config}
            onClick={async () => {
              setUpdatingPool(true);
              try {
                const result = await updateFirewallIPPools();
                await query.refetch();
                message.success(
                  result.count
                    ? `已更新${result.count}个国家/地区的IP池`
                    : '暂无地区规则需要更新',
                );
              } finally {
                setUpdatingPool(false);
              }
            }}
          >
            更新IP池
          </Button>
        )}
        {kind === 'port' && (
          <Button
            disabled={!config}
            onClick={() => {
              let scanProtection = config?.scanProtection ?? false;
              modal.confirm({
                title: '端口防扫描',
                content: (
                  <>
                    <Typography.Paragraph>
                      过滤NULL、XMAS等异常TCP扫描报文，不影响正常TCP握手。
                    </Typography.Paragraph>
                    <Switch
                      defaultChecked={config?.scanProtection}
                      onChange={(value) => {
                        scanProtection = value;
                      }}
                    />
                  </>
                ),
                onOk: () =>
                  change({
                    scanProtection,
                  }),
              });
            }}
          >
            端口防扫描
          </Button>
        )}
        <Input.Search
          className="security-search"
          key={kind}
          placeholder={
            kind === 'ip'
              ? '请输入IP地址/备注'
              : kind === 'region'
                ? '请输入国家代码/备注'
                : kind === 'port'
                  ? '搜索端口、来源或备注'
                  : '搜索端口、目标 IP 或备注'
          }
          allowClear
          onSearch={setKeyword}
        />
      </div>
      {kind === 'region' && (
        <Alert
          type="info"
          style={{ marginBottom: 16 }}
          message="地区规则采用IPdeny公布的国家/地区CIDR数据，按需获取并缓存，不能替代精确地理位置识别。"
        />
      )}
      <Suspense fallback={<Skeleton active />}>
        <FirewallRuleTable
          batchAction={batchAction}
          expired={expired}
          fixedActions={fixedActions}
          kind={kind}
          listening={listening}
          message={message}
          modal={modal}
          open={open}
          query={query}
          rows={rows}
          selected={selected}
          setBatchAction={setBatchAction}
          setDetail={setDetail}
          setSelected={setSelected}
          setWorking={setWorking}
          working={working}
        />
      </Suspense>
      {!!editing && (
        <Suspense fallback={null}>
          <FirewallRuleEditor
            editing={editing}
            form={form}
            kind={kind}
            message={message}
            peerMode={peerMode}
            portMode={portMode}
            query={query}
            setEditing={setEditing}
            setWorking={setWorking}
            working={working}
          />
        </Suspense>
      )}
      {importing && (
        <Suspense fallback={null}>
          <FirewallRuleImport
            importing={importing}
            json={json}
            kind={kind}
            message={message}
            query={query}
            setImporting={setImporting}
            setJSON={setJSON}
            setWorking={setWorking}
            working={working}
          />
        </Suspense>
      )}
      {!!detail && (
        <Suspense fallback={null}>
          <FirewallPortDetail
            detail={detail}
            listening={listening}
            setDetail={setDetail}
          />
        </Suspense>
      )}
    </Card>
  );
}
