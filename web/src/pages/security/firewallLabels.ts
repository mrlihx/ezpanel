export const firewallRuleNames = {
  port: '端口规则',
  ip: 'IP规则',
  forward: '端口转发',
  region: '地区规则',
};

export const firewallRuleOrigins: Record<string, string> = {
  builtin: '面板内置',
  system: '系统默认',
  website: '建站自动放行',
  manual: '手动添加',
  imported: '规则导入',
  external: '系统其他规则',
};

export function firewallRuleOriginLabel(origin?: string) {
  if (!origin) return '手动添加';
  if (origin.startsWith('database:')) return '数据库远程访问';
  return firewallRuleOrigins[origin] ?? origin;
}
