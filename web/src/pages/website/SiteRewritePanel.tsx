import { Button, Select, Space, Spin, Typography } from 'antd';
import { lazy, Suspense } from 'react';
import { saveRewrite } from '../../api/website';

import type { SiteSettingsState } from './SiteSettingsContent';
const CodeEditor = lazy(() => import('../../components/CodeEditor'));

export default function SiteRewritePanel({
  apply,
  canRewrite,
  id,
  manageable,
  modal,
  pending,
  rewrite,
  runtime,
  setRewrite,
  site,
}: Pick<
  SiteSettingsState,
  | 'apply'
  | 'canRewrite'
  | 'id'
  | 'manageable'
  | 'modal'
  | 'pending'
  | 'rewrite'
  | 'runtime'
  | 'setRewrite'
> & {
  site: NonNullable<SiteSettingsState['site']['data']>;
}) {
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Typography.Text type="secondary">
        {runtime
          ? '填写代理 location / 内的规则，支持 rewrite、if、return 等指令。'
          : '支持完整 Nginx 伪静态规则（含多个 location 块、rewrite、try_files、if、deny/allow、return 等，保存时自动校验语法）。'}
      </Typography.Text>
      {site.type === 'static' && (
        <Select
          aria-label="伪静态模板"
          placeholder="选择常用规则"
          value={null}
          style={{ width: 240 }}
          disabled={site.type !== 'static'}
          onChange={(value: string | null) => {
            if (!value) return;
            const presets: Record<string, string> = {
              wordpress: 'try_files $uri $uri/ /index.php?$args;',
              laravel: 'try_files $uri $uri/ /index.php?$query_string;',
              thinkphp:
                'if (!-e $request_filename) {\n    rewrite ^(.*)$ /index.php?s=$1 last;\n}',
              spa: 'try_files $uri $uri/ /index.html;',
              clear: '',
            };
            modal.confirm({
              title: '替换当前伪静态规则？',
              content: '选择的模板会替换编辑框内容，点击保存后才生效。',
              onOk: () => setRewrite(presets[value]),
            });
          }}
          options={[
            { value: 'wordpress', label: 'WordPress' },
            { value: 'laravel', label: 'Laravel' },
            { value: 'thinkphp', label: 'ThinkPHP' },
            { value: 'spa', label: '单页应用（SPA）' },
            { value: 'clear', label: '清空规则' },
          ]}
        />
      )}
      <div style={{ height: 350 }}>
        <Suspense fallback={<Spin />}>
          <CodeEditor
            path={`rewrite-${id}.conf`}
            value={rewrite ?? site.rewriteRule}
            onChange={setRewrite}
            readOnly={!manageable || !canRewrite}
          />
        </Suspense>
      </div>
      <Button
        type="primary"
        loading={pending}
        disabled={!manageable || !canRewrite}
        onClick={() =>
          void apply(() => saveRewrite(id, rewrite ?? site!.rewriteRule))
        }
      >
        保存
      </Button>
    </Space>
  );
}
