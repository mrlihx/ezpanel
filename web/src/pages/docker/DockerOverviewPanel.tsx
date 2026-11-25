import {
  Card,
  Col,
  Descriptions,
  Empty,
  Row,
  Skeleton,
  Statistic,
  Typography,
} from 'antd';
import { formatSize } from '../../utils/format';

import type { DockerPageState } from './useDockerPage';

export default function DockerOverviewPanel({
  containerBytes,
  networkList,
  online,
  overview,
  space,
  usage,
  volumeBytes,
}: Pick<
  DockerPageState,
  | 'containerBytes'
  | 'networkList'
  | 'online'
  | 'overview'
  | 'space'
  | 'usage'
  | 'volumeBytes'
>) {
  return overview.isPending ? (
    <Skeleton active />
  ) : online && overview.data?.info ? (
    <>
      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        {[
          {
            label: '容器数量',
            value: overview.data.info.Containers,
            bytes: containerBytes,
            caption: '可写层占用',
          },
          {
            label: '运行中',
            value: overview.data.info.ContainersRunning,
          },
          {
            label: '已停止',
            value: overview.data.info.ContainersStopped,
          },
          {
            label: '镜像数量',
            value: overview.data.info.Images,
            bytes: usage?.LayersSize,
            caption: '镜像层占用',
          },
          {
            label: '网络数量',
            value: networkList.data?.length ?? '—',
          },
          {
            label: '存储卷数量',
            value: usage?.Volumes?.length ?? '—',
            bytes: volumeBytes,
            caption: '数据占用',
          },
        ].map((item) => (
          <Col xs={12} md={8} xl={4} key={item.label}>
            <Card size="small" style={{ height: '100%' }}>
              <Statistic title={item.label} value={item.value} />
              <Typography.Text
                type="secondary"
                style={{
                  display: 'block',
                  minHeight: 22,
                  marginTop: 8,
                }}
              >
                {item.caption ? `${item.caption}：${space(item.bytes)}` : ' '}
              </Typography.Text>
            </Card>
          </Col>
        ))}
      </Row>
      <Descriptions
        bordered
        column={{ xs: 1, md: 2 }}
        items={[
          {
            key: 'dockerVersion',
            label: 'Docker版本',
            children:
              overview.data.version?.Version ??
              overview.data.info.ServerVersion,
          },
          {
            key: 'apiVersion',
            label: 'API版本',
            children: overview.data.version?.ApiVersion ?? '—',
          },
          {
            key: 'name',
            label: '主机',
            children: overview.data.info.Name,
          },
          {
            key: 'os',
            label: '操作系统',
            children: overview.data.info.OperatingSystem,
          },
          {
            key: 'cpu',
            label: 'CPU',
            children: overview.data.info.NCPU,
          },
          {
            key: 'mem',
            label: '内存',
            children: formatSize(overview.data.info.MemTotal),
          },
          {
            key: 'root',
            label: '数据目录',
            children: overview.data.info.DockerRootDir,
          },
          {
            key: 'driver',
            label: '存储驱动',
            children: overview.data.info.Driver,
          },
          {
            key: 'arch',
            label: '架构',
            children: overview.data.info.Architecture,
          },
          {
            key: 'size',
            label: '镜像层占用',
            children: formatSize(overview.data.usage?.LayersSize ?? 0),
          },
        ]}
      />
    </>
  ) : (
    <Empty description="安装并启动Docker后查看总览" />
  );
}
