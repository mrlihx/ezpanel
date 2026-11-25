import { lazy, Suspense, useState } from 'react';
import { Alert, App, Button, Form, Input, Skeleton, Space, Tabs } from 'antd';
import { useMutation, useQuery } from '@tanstack/react-query';
import { request } from '../../api/client';
import {
  applicationSettings,
  saveApplicationSettings,
} from '../../api/software';

const CodeEditor = lazy(() => import('../../components/CodeEditor'));

type BasicConfig = { mirrors: string; insecureRegistries: string };
type Draft = { revision: string } & (BasicConfig | { content: string });

function readBasicConfig(content: string): BasicConfig | undefined {
  try {
    const document = JSON.parse(content);
    if (!document || typeof document !== 'object' || Array.isArray(document))
      return;
    return {
      mirrors: (document['registry-mirrors'] ?? []).join('\n'),
      insecureRegistries: (document['insecure-registries'] ?? []).join('\n'),
    };
  } catch {
    return;
  }
}

function addresses(text: string) {
  return [
    ...new Set(
      text
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean),
    ),
  ];
}

export default function DockerSettings({
  installed,
  busy,
  run,
  tab,
  setTab,
}: {
  installed: boolean;
  tab: string;
  setTab: (next: string) => void;
  busy: boolean;
  run: (job: () => Promise<unknown>) => Promise<void>;
}) {
  const { message, modal } = App.useApp();
  const [draft, setDraft] = useState<Draft>();
  const config = useQuery({
    queryKey: ['app-settings', 'docker'],
    queryFn: () => applicationSettings('docker'),
    enabled: installed,
  });
  const save = useMutation({
    mutationFn: (next: Draft) =>
      run(async () => {
        if ('content' in next) {
          await saveApplicationSettings('docker', next.content, next.revision);
        } else {
          await request('/api/software/docker/settings', {
            method: 'PATCH',
            data: {
              revision: next.revision,
              mirrors: addresses(next.mirrors),
              insecureRegistries: addresses(next.insecureRegistries),
            },
          });
        }
        await config.refetch();
        setDraft(undefined);
        message.success(
          'content' in next
            ? '配置已保存。部分选项需要重启 Docker 才能生效。'
            : '配置已保存，重载 Docker 配置后生效',
        );
      }),
  });

  if (!installed) return null;
  if (config.isPending) return <Skeleton active />;
  if (!config.data)
    return (
      <Alert
        type="error"
        message={config.error?.message}
        action={<Button onClick={() => void config.refetch()}>重试</Button>}
      />
    );

  const { content, revision } = config.data;
  const basic = draft && 'mirrors' in draft ? draft : readBasicConfig(content);
  const disabled = busy || save.isPending;
  const changeTab = (next: string) => {
    if (disabled) return;
    if (draft) {
      modal.confirm({
        title: '放弃当前未保存的修改？',
        okText: '放弃修改',
        cancelText: '继续编辑',
        onOk: () => {
          setDraft(undefined);
          setTab(next);
        },
      });
    } else setTab(next);
  };

  return (
    <>
      <Tabs
        type="card"
        size="small"
        activeKey={tab}
        onChange={changeTab}
        items={[
          {
            key: 'basic',
            label: '基础配置',
            children: basic ? (
              <Form
                layout="vertical"
                style={{ maxWidth: 560 }}
                disabled={disabled}
              >
                <Form.Item
                  label="镜像加速地址"
                  htmlFor="docker-mirrors"
                  extra="每行一个 HTTP 或 HTTPS 地址，留空取消镜像加速。"
                >
                  <Input.TextArea
                    id="docker-mirrors"
                    placeholder="https://mirror.example.com"
                    autoSize={{ minRows: 3, maxRows: 8 }}
                    value={basic.mirrors}
                    onChange={(event) =>
                      setDraft({
                        ...basic,
                        revision: draft?.revision ?? revision,
                        mirrors: event.target.value,
                      })
                    }
                  />
                </Form.Item>
                <Form.Item
                  label="私有仓库地址"
                  htmlFor="docker-insecure-registries"
                  extra="每行一个主机名、主机名:端口或网段，用于允许 HTTP 或自签证书仓库。仓库账号可在「仓库」中设置。"
                >
                  <Input.TextArea
                    id="docker-insecure-registries"
                    placeholder="registry.example.com:5000"
                    autoSize={{ minRows: 3, maxRows: 8 }}
                    value={basic.insecureRegistries}
                    onChange={(event) =>
                      setDraft({
                        ...basic,
                        revision: draft?.revision ?? revision,
                        insecureRegistries: event.target.value,
                      })
                    }
                  />
                </Form.Item>
              </Form>
            ) : (
              <Alert
                type="warning"
                message="配置无法读取，请在「全部配置」中修正 daemon.json。"
              />
            ),
          },
          {
            key: 'full',
            label: '全部配置',
            children: (
              <div
                style={{ height: 400, background: '#1e1e1e', marginBottom: 16 }}
              >
                <Suspense fallback={<Skeleton active />}>
                  <CodeEditor
                    path="daemon.json"
                    editorTheme="vs-dark"
                    value={
                      draft && 'content' in draft ? draft.content : content
                    }
                    readOnly={disabled}
                    onChange={(content) =>
                      setDraft({
                        revision: draft?.revision ?? revision,
                        content,
                      })
                    }
                  />
                </Suspense>
              </div>
            ),
          },
        ]}
      />
      <Space wrap>
        <Button
          type="primary"
          disabled={!draft || disabled}
          loading={save.isPending}
          onClick={() => draft && save.mutate(draft)}
        >
          保存配置
        </Button>
        <Button
          disabled={disabled}
          loading={config.isFetching}
          onClick={() => {
            setDraft(undefined);
            void config.refetch();
          }}
        >
          重新加载
        </Button>
      </Space>
      {config.isError && (
        <Alert
          type="error"
          message={config.error.message}
          style={{ marginTop: 12 }}
        />
      )}
    </>
  );
}
