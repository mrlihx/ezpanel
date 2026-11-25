import { useState, type ComponentProps } from 'react';
import { Segmented, Skeleton } from 'antd';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useRoutedTab } from '../../../hooks/useRoutedTab';
import RouteNotFound from '../../../router/RouteNotFound';
import ComposeTemplates from '../ComposeTemplates';
import ComposeCreateModal from './ComposeCreateModal';
import ComposeProjects from './ComposeProjects';

const base = '/docker/compose';

export default function ComposeWorkspace({
  active,
  online,
  onTask,
  onChanged,
  onContainer,
  onTerminal,
}: Pick<
  ComponentProps<typeof ComposeProjects>,
  'active' | 'online' | 'onTask' | 'onChanged' | 'onContainer' | 'onTerminal'
>) {
  const [params] = useSearchParams();
  const { section, view, resource, detailTab } = useParams();
  const navigate = useNavigate();
  const legacyProject = params.get('project');
  const shown = active && section === 'compose';
  const route = useRoutedTab(
    'dockerCompose',
    base,
    shown ? view : undefined,
    ['projects', 'templates'],
    {
      keys: ['project'],
      tab: legacyProject ? 'projects' : null,
      suffix: legacyProject
        ? `/${encodeURIComponent(legacyProject)}/editor`
        : undefined,
    },
    !shown,
  );
  const [templateId, setTemplateId] = useState<string>();
  if (
    shown &&
    (route.invalid ||
      (resource && view !== 'projects') ||
      (detailTab && !['editor', 'logs', 'env'].includes(detailTab)))
  )
    return <RouteNotFound back={base} />;
  if (shown && route.loading) return <Skeleton active />;
  const tab = route.tab;

  return (
    <>
      <Segmented
        className="compose-subnav"
        aria-label="容器编排视图"
        value={tab}
        onChange={(key) => void route.selectTab(key)}
        options={[
          { value: 'projects', label: '编排项目' },
          { value: 'templates', label: '编排模板' },
        ]}
      />
      <div hidden={tab !== 'projects'}>
        <ComposeProjects
          active={shown && tab === 'projects'}
          requestedProject={shown && view === 'projects' ? resource : undefined}
          projectTab={shown && view === 'projects' ? detailTab : undefined}
          onProjectTab={(name, nextTab) =>
            void navigate(
              `${base}/projects/${encodeURIComponent(name)}/${nextTab}`,
            )
          }
          online={online}
          onTask={onTask}
          onChanged={onChanged}
          onContainer={onContainer}
          onTerminal={onTerminal}
        />
      </div>
      <div hidden={tab !== 'templates'}>
        <ComposeTemplates
          active={shown && tab === 'templates'}
          online={online}
          onCreateFromTemplate={setTemplateId}
        />
      </div>
      {templateId && (
        <ComposeCreateModal
          key={templateId}
          initialTemplateId={templateId}
          onClose={() => setTemplateId(undefined)}
          onTask={(id) => {
            void route.selectTab('projects');
            onTask(id);
          }}
        />
      )}
    </>
  );
}
