export function containerOwnership(
  labels: Record<string, string> | null | undefined,
  name: string,
) {
  if (labels?.['ezpanel.managed'] === 'true') {
    if (labels['ezpanel.project'] === 'true') {
      return {
        label: '网站项目',
        link: '/websites',
        action: '前往网站项目设置',
        removal: '请优先在网站项目中管理或删除，直接删除容器会保留项目记录。',
      };
    }
    const app =
      labels['ezpanel.database'] || /^\/?ezpanel-(.+)$/.exec(name)?.[1];
    const instance = labels['ezpanel.database']
      ? name.replace(/^\//, '').slice(`ezpanel-${app}-`.length)
      : '';
    const params = app ? new URLSearchParams({ settings: app }) : undefined;
    if (params && instance) params.set('instance', instance);
    return {
      label: '应用商店',
      link: params ? `/apps?${params}` : '/apps',
      action: '前往应用商店设置',
      removal: '请优先在应用商店卸载。直接删除会保留应用记录和配置。',
    };
  }
  const project = labels?.['com.docker.compose.project'];
  if (project)
    return {
      label: 'Compose',
      link: `/docker/compose/projects/${encodeURIComponent(project)}/editor`,
      action: '前往编排项目编辑',
      removal: '建议在编排项目中下线清理。直接删除只移除此容器。',
    };
  if (labels?.['com.docker.swarm.service.name'])
    return {
      label: 'Swarm',
      link: undefined,
      action: undefined,
      removal: '请在 Swarm 服务中管理，直接删除后容器可能被自动重建。',
    };
}
