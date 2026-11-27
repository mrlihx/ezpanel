import {
  Alert,
  Collapse,
  Descriptions,
  Flex,
  Space,
  Switch,
  Tag,
  Typography,
} from 'antd';
import type {
  Manifest,
  PluginAccessItem,
  PluginAccessPreview,
} from '../../api/plugin';
import { SPACING } from '../../styles/tokens';

const wrap = { overflowWrap: 'anywhere' } as const;
const list = { margin: 0, paddingInlineStart: 20, ...wrap };
const riskRank = { high: 2, attention: 1 };

function compareRisk(first: PluginAccessItem, second: PluginAccessItem) {
  return (
    (second.risk ? riskRank[second.risk] : 0) -
    (first.risk ? riskRank[first.risk] : 0)
  );
}

function RiskTag({ risk }: Pick<PluginAccessItem, 'risk'>) {
  if (!risk) return null;
  return (
    <Tag color={risk === 'high' ? 'error' : 'warning'}>
      {risk === 'high' ? '高风险' : '中风险'}
    </Tag>
  );
}

export default function PluginAccessReview({
  manifest,
  preview,
  startCollapsed,
  onTogglePermission,
  togglingPermission,
}: {
  manifest: Manifest;
  preview: PluginAccessPreview;
  startCollapsed: boolean;
  onTogglePermission?: (code: string, enabled: boolean) => void | Promise<void>;
  togglingPermission?: string;
}) {
  const permissions = [...preview.permissions].sort(compareRisk);
  const enabledCount = permissions.filter(
    (item) => item.enabled !== false,
  ).length;
  const risks = [
    ...preview.runtimeRisks,
    ...permissions.filter((item) => item.risk),
  ].sort(compareRisk);
  const hasHighRisk = risks[0]?.risk === 'high';
  return (
    <Flex
      vertical
      gap={SPACING.lg}
      style={{ marginBlock: SPACING.lg, ...wrap }}
    >
      <Descriptions
        size="small"
        column={1}
        items={[
          {
            key: 'name',
            label: '插件',
            children: `${manifest.name} / ${manifest.id}`,
          },
          {
            key: 'version',
            label: '版本 / 作者',
            children: (
              <>
                {manifest.version} /{' '}
                {manifest.homepage &&
                /^https?:\/\//i.test(manifest.homepage) ? (
                  <a
                    href={manifest.homepage}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    {manifest.author || manifest.homepage}
                  </a>
                ) : (
                  manifest.author || '未提供'
                )}
              </>
            ),
          },
          {
            key: 'ui',
            label: '前端',
            children: manifest.uiMode === 'legacy' ? '原生嵌入' : '隔离页面',
          },
          {
            key: 'sandbox',
            label: '后端',
            children: manifest.sandbox
              ? `独立沙箱，CPU ${manifest.sandbox.cpuPercent || 100}%，内存 ${manifest.sandbox.memoryMiB || 256} MiB，任务数 ${manifest.sandbox.maxProcesses || 64}，${manifest.sandbox.network === 'outbound' ? '允许出站网络' : '仅本机回环'}`
              : manifest.type === 'process'
                ? '兼容进程'
                : '静态插件',
          },
        ]}
      />
      {risks.length > 0 ||
      manifest.execAllow?.length ||
      manifest.fsRoots?.length ? (
        <Alert
          type={hasHighRisk ? 'error' : 'warning'}
          showIcon
          message="请核对这些风险项"
          description={
            <Flex vertical gap={SPACING.sm}>
              <ul style={list}>
                {risks.map((item) => (
                  <li key={item.label} style={{ marginBlock: SPACING.xs }}>
                    <RiskTag risk={item.risk} />
                    <Typography.Text strong>{item.label}</Typography.Text>{' '}
                    {item.scope}
                  </li>
                ))}
              </ul>
              {!!manifest.execAllow?.length && (
                <div>
                  <Typography.Text strong>命令白名单</Typography.Text>{' '}
                  {manifest.execAllow.map((command) => (
                    <Typography.Text key={command} code style={wrap}>
                      {command}
                    </Typography.Text>
                  ))}
                </div>
              )}
              {!!manifest.fsRoots?.length && (
                <div>
                  <Typography.Text strong>文件访问目录</Typography.Text>{' '}
                  {manifest.fsRoots.map((root) => (
                    <Typography.Text key={root} code style={wrap}>
                      {root}
                    </Typography.Text>
                  ))}
                </div>
              )}
            </Flex>
          }
        />
      ) : null}
      <Collapse
        size="small"
        defaultActiveKey={startCollapsed ? [] : ['permissions', 'capabilities']}
        items={[
          {
            key: 'permissions',
            label: onTogglePermission
              ? `Host 权限（已开启 ${enabledCount} / ${permissions.length}）`
              : `Host 权限（${permissions.length}）`,
            children: (
              <Flex vertical gap={SPACING.sm}>
                {onTogglePermission && permissions.length > 0 && (
                  <Typography.Text type="secondary">
                    高风险权限安装后默认关闭；关闭权限后即时生效，对应「授权」项将同步禁用。
                  </Typography.Text>
                )}
                {permissions.map((item) => {
                  const enabled = item.enabled !== false;
                  return (
                    <Flex
                      key={item.code}
                      justify="space-between"
                      align="flex-start"
                      gap={SPACING.md}
                    >
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <Space wrap size={SPACING.xs}>
                          <Typography.Text strong>{item.label}</Typography.Text>
                          <Typography.Text code>{item.code}</Typography.Text>
                          <RiskTag risk={item.risk} />
                          {onTogglePermission && !enabled && <Tag>已关闭</Tag>}
                        </Space>
                        {(onTogglePermission || !item.risk) && (
                          <div>
                            <Typography.Text type="secondary">
                              {item.scope}
                            </Typography.Text>
                          </div>
                        )}
                      </div>
                      {onTogglePermission && (
                        <Switch
                          size="small"
                          checked={enabled}
                          loading={togglingPermission === item.code}
                          disabled={
                            !!togglingPermission &&
                            togglingPermission !== item.code
                          }
                          aria-label={`开关 ${item.label}（${item.code}）`}
                          onChange={(checked) =>
                            void onTogglePermission(item.code, checked)
                          }
                        />
                      )}
                    </Flex>
                  );
                })}
                {manifest.type === 'process' ? (
                  <Typography.Text type="secondary">
                    进程插件还可读取自身信息、使用自己的键值存储、写日志及查看自己的任务。
                  </Typography.Text>
                ) : permissions.length === 0 ? (
                  <Typography.Text type="secondary">
                    未声明 Host 权限
                  </Typography.Text>
                ) : null}
              </Flex>
            ),
          },
          {
            key: 'capabilities',
            label: `面板扩展能力（${preview.capabilities.length}）`,
            children: preview.capabilities.length ? (
              <Descriptions
                column={1}
                size="small"
                items={preview.capabilities.map((item) => ({
                  key: item.label,
                  label: item.label,
                  children: item.items.join('、'),
                }))}
              />
            ) : (
              <Typography.Text type="secondary">
                未声明面板扩展能力
              </Typography.Text>
            ),
          },
        ]}
      />
      <Typography.Text type="secondary">
        清单根据插件声明和运行模式生成。请仅安装可信来源的插件。高风险权限安装后默认关闭，资源权限可在插件设置的「授权」页按类别或指定资源配置。
      </Typography.Text>
    </Flex>
  );
}
