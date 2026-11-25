import { lazy, Suspense, useEffect, useState } from 'react';
import { Alert, App, Button, Grid, Spin, Tabs, type TabsProps } from 'antd';
import { useQuery } from '@tanstack/react-query';
import {
  siteCertificate,
  saveSSL,
  saveWebsite,
  siteConfig,
  phpVersions,
  runtimeLogs,
  type SiteInput,
} from '../../api/website';
import { LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';
import './SettingsModal.css';
import type { SettingsDialogState } from './projectDialogs';
import ProjectDependencies from './ProjectDependencies';
import ProjectLoad from './ProjectLoad';
import ProjectService from './ProjectService';
import SiteLogs from './SiteLogs';
import SiteWAF from './SiteWAF';
const SiteStatistics = lazy(() => import('./SiteStatistics'));
const errorText = (e: unknown) => (e instanceof Error ? e.message : '请求失败');
const SiteDomainsPanel = lazy(() => import('./SiteDomainsPanel'));
const SiteDirectoryPanel = lazy(() => import('./SiteDirectoryPanel'));
const SitePhpPanel = lazy(() => import('./SitePhpPanel'));
const SiteRuntimePanel = lazy(() => import('./SiteRuntimePanel'));
const SiteRuntimeLogsPanel = lazy(() => import('./SiteRuntimeLogsPanel'));
const SiteProxyPanel = lazy(() => import('./SiteProxyPanel'));
const SiteWsgiPanel = lazy(() => import('./SiteWsgiPanel'));
const SiteSslPanel = lazy(() => import('./SiteSslPanel'));
const SiteRewritePanel = lazy(() => import('./SiteRewritePanel'));
const SiteRedirectPanel = lazy(() => import('./SiteRedirectPanel'));
const SiteConfigPanel = lazy(() => import('./SiteConfigPanel'));
function useSiteSettingsContent({
  id,
  manageable,
  initialTab,
  onTabChange,
  onChanged,
  site,
  pending,
  setPending,
  sections,
}: SettingsDialogState & {
  id: string;
  manageable: boolean;
  initialTab?: string;
  onTabChange?: (tab: string, replace?: boolean) => void;
  onChanged: () => void;
  sections: ReadonlyArray<{ key: string; label?: string }>;
}) {
  const screens = Grid.useBreakpoint();
  const [newDomains, setNewDomains] = useState('');
  const { message, modal } = App.useApp();
  const [localTab, setLocalTab] = useState(sections[0].key);
  const tab = onTabChange ? (initialTab ?? sections[0].key) : localTab;
  function setTab(next: string) {
    if (onTabChange) onTabChange(next);
    else setLocalTab(next);
  }
  useEffect(() => {
    if (site.isSuccess && !initialTab && onTabChange)
      onTabChange(sections[0].key, true);
  }, [site.isSuccess, initialTab, onTabChange, sections]);
  const [error, setError] = useState('');
  const [cert, setCert] = useState<string | undefined>();
  const [rewrite, setRewrite] = useState<string | undefined>();
  const [privateKey, setKey] = useState<string | undefined>();
  const [certName, setCertName] = useState('');
  const [keyName, setKeyName] = useState('');
  const [force, setForce] = useState<boolean | undefined>();
  const projectType = site.data?.projectType;
  const [entryModule, runtimeCallable] = (site.data?.runtimeEntry ?? '').split(
    ':',
  );
  const runtime =
    site.data?.runtimeVersion &&
    projectType &&
    projectType !== 'php' &&
    projectType !== 'proxy'
      ? projectType
      : undefined;
  const canRewrite =
    site.data?.type === 'static' || runtime === 'go' || runtime === 'nodejs';
  const projectLogs = useQuery({
    queryKey: ['project-logs', id],
    queryFn: () => runtimeLogs(id),
    enabled: tab === 'runtime-logs',
  });
  const php = useQuery({
    queryKey: ['website-php-versions'],
    queryFn: phpVersions,
    enabled: tab === 'php',
  });
  const config = useQuery({
    queryKey: ['website-config', id],
    queryFn: () => siteConfig(id),
    enabled: tab === 'config',
  });
  const certificate = useQuery({
    queryKey: ['website-certificate', id],
    queryFn: () => siteCertificate(id),
    enabled: tab === 'ssl' && !!site.data?.sslEnabled,
  });
  const finish = async () => {
    onChanged();
    await site.refetch();
    if (tab === 'config') await config.refetch();
  };
  const save = async (patch: Partial<SiteInput>) => {
    if (!site.data) return false;
    const {
      name,
      domains,
      type,
      projectType,
      root,
      proxyRoutes,
      phpVersion,
      remark,
      runtimeVersion,
      runtimeEnvironment,
      runtimeLaunch,
      runtimeEntry,
      runtimeCommand,
      runtimePort,
      runtimeWorkers,
      runtimeThreads,
      runtimeTimeout,
      runtimeFile,
      runtimeAutoStart,
      runtimePackageManager,
      runtimeScript,
      runtimeEnv,
    } = site.data;
    const values: SiteInput = {
      name,
      domains,
      type,
      projectType,
      root,
      proxyRoutes,
      phpVersion,
      remark,
      runtimeVersion,
      runtimeEnvironment,
      runtimeLaunch,
      runtimeEntry,
      runtimeCommand,
      runtimePort,
      runtimeWorkers,
      runtimeThreads,
      runtimeTimeout,
      runtimeFile,
      runtimeAutoStart,
      runtimePackageManager,
      runtimeScript,
      runtimeEnv,
      ...patch,
    };
    if (values.type === 'proxy') {
      if (!runtime) values.root = '';
      values.phpVersion = '';
    }
    setPending(true);
    setError('');
    try {
      await saveWebsite(id, values);
      void message.success('站点配置已保存');
      await finish();
      return true;
    } catch (e) {
      setError(errorText(e));
      return false;
    } finally {
      setPending(false);
    }
  };
  const upload = async (file: File, privateKey: boolean) => {
    if (file.size > 2 * 1024 * 1024) {
      setError('单个 PEM 文件不能超过2MiB');
      return false;
    }
    const value = await file.text();
    if (privateKey) {
      setKey(value);
      setKeyName(file.name);
    } else {
      setCert(value);
      setCertName(file.name);
    }
    return false;
  };
  const submitSSL = async () => {
    setPending(true);
    setError('');
    try {
      await saveSSL(
        id,
        cert ?? certificate.data?.certificate ?? '',
        privateKey ?? certificate.data?.privateKey ?? '',
        force ?? site.data?.forceHttps ?? false,
      );
      setCert(undefined);
      await certificate.refetch();
      setKey(undefined);
      setCertName('');
      setKeyName('');
      void message.success('SSL 配置已保存');
      await finish();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setPending(false);
    }
  };
  const apply = async (action: () => Promise<unknown>) => {
    setPending(true);
    setError('');
    try {
      await action();
      await finish();
      void message.success('配置已保存');
    } catch (e) {
      setError(errorText(e));
    } finally {
      setPending(false);
    }
  };
  return {
    apply,
    canRewrite,
    cert,
    certName,
    certificate,
    config,
    entryModule,
    error,
    finish,
    force,
    id,
    privateKey,
    keyName,
    manageable,
    modal,
    newDomains,
    pending,
    php,
    projectLogs,
    projectType,
    rewrite,
    runtime,
    runtimeCallable,
    save,
    screens,
    setCert,
    setCertName,
    setError,
    setForce,
    setKey,
    setKeyName,
    setNewDomains,
    setPending,
    setRewrite,
    setTab,
    site,
    submitSSL,
    tab,
    upload,
  };
}
export type SiteSettingsState = ReturnType<typeof useSiteSettingsContent>;
export default function SiteSettingsContent(
  props: Parameters<typeof useSiteSettingsContent>[0],
) {
  const {
    apply,
    canRewrite,
    cert,
    certName,
    certificate,
    config,
    entryModule,
    error,
    finish,
    force,
    id,
    privateKey,
    keyName,
    manageable,
    modal,
    newDomains,
    pending,
    php,
    projectLogs,
    projectType,
    rewrite,
    runtime,
    runtimeCallable,
    save,
    screens,
    setCert,
    setCertName,
    setError,
    setForce,
    setKey,
    setKeyName,
    setNewDomains,
    setPending,
    setRewrite,
    setTab,
    site,
    submitSSL,
    tab,
    upload,
  } = useSiteSettingsContent(props);
  const { sections, initialTab } = props;
  const loadedSite = site.data;
  const panels: NonNullable<TabsProps['items']> = loadedSite
    ? [
        {
          key: 'domains',
          label: '域名',
          children: (
            <Suspense fallback={<Spin />}>
              <SiteDomainsPanel
                manageable={manageable}
                modal={modal}
                newDomains={newDomains}
                pending={pending}
                runtime={runtime}
                save={save}
                setNewDomains={setNewDomains}
                site={loadedSite}
              />
            </Suspense>
          ),
        },
        {
          key: 'directory',
          label: '目录',
          children: (
            <Suspense fallback={<Spin />}>
              <SiteDirectoryPanel
                manageable={manageable}
                pending={pending}
                runtime={runtime}
                save={save}
                site={loadedSite}
              />
            </Suspense>
          ),
        },
        {
          key: 'php',
          label: 'PHP',
          disabled: loadedSite.type !== 'static',
          children: (
            <Suspense fallback={<Spin />}>
              <SitePhpPanel
                manageable={manageable}
                pending={pending}
                php={php}
                save={save}
                site={loadedSite}
              />
            </Suspense>
          ),
        },
        ...(runtime
          ? [
              {
                key: 'runtime',
                label: '运行设置',
                children: (
                  <Suspense fallback={<Spin />}>
                    <SiteRuntimePanel
                      entryModule={entryModule}
                      id={id}
                      manageable={manageable}
                      pending={pending}
                      runtime={runtime}
                      runtimeCallable={runtimeCallable}
                      save={save}
                      site={loadedSite}
                    />
                  </Suspense>
                ),
              },
              {
                key: 'runtime-logs',
                label: '运行日志',
                children: (
                  <Suspense fallback={<Spin />}>
                    <SiteRuntimeLogsPanel projectLogs={projectLogs} tab={tab} />
                  </Suspense>
                ),
              },
            ]
          : [
              {
                key: 'proxy',
                label: '反向代理',
                children: (
                  <Suspense fallback={<Spin />}>
                    <SiteProxyPanel
                      error={error}
                      manageable={manageable}
                      pending={pending}
                      save={save}
                      site={loadedSite}
                    />
                  </Suspense>
                ),
              },
            ]),
        ...(projectType === 'python'
          ? [
              {
                key: 'wsgi',
                label: '',
                children: (
                  <Suspense fallback={<Spin />}>
                    <SiteWsgiPanel site={loadedSite} tab={tab} />
                  </Suspense>
                ),
              },
            ]
          : []),
        {
          key: 'waf',
          label: 'WAF防护',
          children:
            tab === 'waf' ? <SiteWAF id={id} manageable={manageable} /> : null,
        },
        {
          key: 'statistics',
          label: '网站统计',
          children:
            tab === 'statistics' ? (
              <Suspense fallback={<Spin />}>
                <SiteStatistics id={id} />
              </Suspense>
            ) : null,
        },
        ...(runtime === 'python' || runtime === 'nodejs'
          ? [
              {
                key: 'dependencies',
                label: '环境管理',
                children: (
                  <ProjectDependencies
                    key={loadedSite.runtimeEnvironment}
                    site={loadedSite}
                  />
                ),
              },
            ]
          : []),
        ...(runtime
          ? [
              {
                key: 'runtime-load',
                label: '负载状态',
                children:
                  tab === 'runtime-load' ? <ProjectLoad id={id} /> : null,
              },
              {
                key: 'runtime-service',
                label: '服务状态',
                children:
                  tab === 'runtime-service' ? (
                    <ProjectService
                      site={loadedSite}
                      manageable={manageable}
                      onChanged={finish}
                    />
                  ) : null,
              },
            ]
          : []),
        {
          key: 'ssl',
          label: 'SSL',
          children: (
            <Suspense fallback={<Spin />}>
              <SiteSslPanel
                cert={cert}
                certName={certName}
                certificate={certificate}
                finish={finish}
                force={force}
                id={id}
                privateKey={privateKey}
                keyName={keyName}
                manageable={manageable}
                modal={modal}
                pending={pending}
                setCert={setCert}
                setCertName={setCertName}
                setError={setError}
                setForce={setForce}
                setKey={setKey}
                setKeyName={setKeyName}
                setPending={setPending}
                site={loadedSite}
                submitSSL={submitSSL}
                upload={upload}
              />
            </Suspense>
          ),
        },
        {
          key: 'rewrite',
          label: '伪静态',
          children: (
            <Suspense fallback={<Spin />}>
              <SiteRewritePanel
                apply={apply}
                canRewrite={canRewrite}
                id={id}
                manageable={manageable}
                modal={modal}
                pending={pending}
                rewrite={rewrite}
                runtime={runtime}
                setRewrite={setRewrite}
                site={loadedSite}
              />
            </Suspense>
          ),
        },
        {
          key: 'redirect',
          label: '重定向',
          children: (
            <Suspense fallback={<Spin />}>
              <SiteRedirectPanel
                apply={apply}
                id={id}
                manageable={manageable}
                pending={pending}
                site={loadedSite}
              />
            </Suspense>
          ),
        },
        {
          key: 'logs',
          label: '日志',
          children:
            tab === 'logs' ? (
              <SiteLogs id={id} directory={loadedSite?.logDirectory} />
            ) : null,
        },
        {
          key: 'config',
          label: '配置文件',
          children: (
            <Suspense fallback={<Spin />}>
              <SiteConfigPanel config={config} id={id} />
            </Suspense>
          ),
        },
      ]
    : [];
  const tabs = [...sections, { key: 'waf' }, { key: 'statistics' }].flatMap(
    (section) => {
      const panel = panels.find(({ key }) => key === section.key);
      return panel ? [{ ...panel, ...section }] : [];
    },
  );

  return (
    <>
      {error && (
        <Alert
          type="error"
          showIcon
          message="保存失败"
          description={
            <pre
              style={{
                whiteSpace: 'pre-wrap',
                fontFamily: MONO_FONT,
                maxHeight: LAYOUT.errorMaxHeight,
                overflow: 'auto',
              }}
            >
              {error}
            </pre>
          }
          style={{ marginBottom: SPACING.lg }}
        />
      )}
      {site.isError && site.data && (
        <Alert
          type="warning"
          message="站点详情刷新失败，正在显示上次结果"
          description={errorText(site.error)}
          style={{ marginBottom: SPACING.lg }}
        />
      )}
      {tab === 'config' && config.isError && config.data && (
        <Alert
          type="warning"
          message="配置刷新失败，正在显示上次结果"
          description={errorText(config.error)}
          style={{ marginBottom: SPACING.lg }}
        />
      )}
      {site.isPending ? (
        <Spin />
      ) : site.isError && !site.data ? (
        <Alert
          type="error"
          message={errorText(site.error)}
          action={<Button onClick={() => void site.refetch()}>重试</Button>}
        />
      ) : (
        site.data && (
          <>
            {!site.data.effectiveRunning && (
              <Alert
                type="warning"
                showIcon
                message={
                  site.data.enabled
                    ? runtime
                      ? '项目或 OpenResty 未运行，可查看运行日志'
                      : '站点已启用，OpenResty 服务当前未运行'
                    : runtime
                      ? '项目已停止，可在网站列表启动'
                      : '站点配置已停用'
                }
                style={{ marginBottom: SPACING.lg }}
              />
            )}
            <Tabs
              className="website-settings-tabs"
              style={{ flex: 1, minHeight: 0 }}
              tabPosition={screens.md ? 'left' : 'top'}
              activeKey={
                initialTab || tabs.some(({ key }) => key === tab)
                  ? tab
                  : tabs[0]?.key
              }
              onChange={(value) => {
                setTab(value);
                setError('');
              }}
              items={
                initialTab && !tabs.some(({ key }) => key === tab)
                  ? [
                      ...tabs,
                      {
                        key: tab,
                        label: '页面不存在',
                        children: (
                          <Alert
                            showIcon
                            type="warning"
                            message="此项目没有该设置页面。"
                          />
                        ),
                      },
                    ]
                  : tabs
              }
            />
          </>
        )
      )}
    </>
  );
}
