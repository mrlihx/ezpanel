import { Button, Space, Switch, Typography } from 'antd';
import { applicationService, saveApplicationGuard } from '../../api/software';
import DockerSettings from './DockerSettings';

import type { DockerPageState } from './useDockerPage';

export default function DockerSettingsPanel({
  busy,
  guard,
  message,
  modal,
  online,
  overview,
  run,
  settingsRoute,
}: Pick<
  DockerPageState,
  | 'busy'
  | 'guard'
  | 'message'
  | 'modal'
  | 'online'
  | 'overview'
  | 'run'
  | 'settingsRoute'
>) {
  return (
    <Space direction="vertical" size="large" style={{ width: '100%' }}>
      <Space wrap>
        <Button
          disabled={!overview.data?.installed || online || busy}
          onClick={() => run(() => applicationService('docker', 'start'))}
        >
          启动
        </Button>
        <Button
          disabled={!online || busy}
          onClick={() =>
            modal.confirm({
              title: '停止Docker服务？',
              content: '依赖Docker的服务可能中断。',
              onOk: () => run(() => applicationService('docker', 'stop')),
            })
          }
        >
          停止
        </Button>
        <Button
          disabled={!overview.data?.installed || busy}
          onClick={() =>
            modal.confirm({
              title: '重启Docker服务？',
              content: '未配置live-restore的容器可能中断。',
              onOk: () => run(() => applicationService('docker', 'restart')),
            })
          }
        >
          重启
        </Button>
        <Button
          disabled={!online || busy}
          onClick={() =>
            run(async () => {
              await applicationService('docker', 'reload');
              message.success('已发起 Docker 配置重载');
            })
          }
        >
          重载配置
        </Button>
        <Typography.Text>进程守护</Typography.Text>
        <Switch
          disabled={!guard.data || busy}
          checked={guard.data?.enabled}
          onChange={(enabled) =>
            run(() =>
              saveApplicationGuard(
                'docker',
                enabled,
                guard.data!.restartSeconds,
              ),
            )
          }
        />
      </Space>
      <DockerSettings
        tab={settingsRoute.tab}
        setTab={settingsRoute.selectTab}
        installed={!!overview.data?.installed}
        busy={busy}
        run={run}
      />
    </Space>
  );
}
