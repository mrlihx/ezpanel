import { defineConfig } from 'vite';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const target = process.env.PANEL_DEV_TARGET;
const shared = [
  'react',
  'react/jsx-runtime',
  'react/jsx-dev-runtime',
  'react-dom',
  'react-dom/client',
  'antd',
  '@ant-design/icons',
];
const imports = Object.fromEntries(
  shared.map((name, i) => [name, `/shared/v1/${i}.js`]),
);

function bridge(name: string) {
  const keys = Object.keys(require(name)).filter(
    (key) => key !== 'default' && /^[A-Za-z_$][\w$]*$/.test(key),
  );
  return `const m=globalThis.__PANEL_SHARED__?.[${JSON.stringify(name)}];if(!m)throw new Error("面板共享运行时尚未就绪");\nexport default m;\n${keys.map((key) => `export const ${key}=m.${key};`).join('\n')}`;
}

export default defineConfig({
  plugins: [
    {
      name: 'panel-shared-esm',
      transformIndexHtml() {
        return [
          {
            tag: 'script',
            attrs: { type: 'importmap' },
            children: JSON.stringify({ imports }),
            injectTo: 'head-prepend',
          },
        ];
      },
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          if (req.url === '/assets/plugin-frame.js') {
            req.url = '/src/plugins/frame.tsx';
            return next();
          }
          const match = /^\/shared\/v1\/(\d+)\.js$/.exec(req.url ?? '');
          const name = match && shared[Number(match[1])];
          if (!name) return next();
          if (req.headers.origin === 'null')
            res.setHeader('Access-Control-Allow-Origin', 'null');
          res.setHeader('Content-Type', 'text/javascript');
          res.end(bridge(name));
        });
      },
      generateBundle() {
        for (const [i, name] of shared.entries()) {
          const source = bridge(name);
          this.emitFile({
            type: 'asset',
            fileName: `shared/v1/${i}.js`,
            source,
          });
        }
      },
    },
  ],
  // 纯客户端构建，依赖的 use client 标记不涉及服务端组件边界。
  build: {
    rolldownOptions: {
      input: { main: 'index.html', 'plugin-frame': 'plugin-frame.html' },
      checks: { moduleLevelDirective: false },
      output: {
        entryFileNames: (chunk) =>
          chunk.name === 'plugin-frame'
            ? 'assets/plugin-frame.js'
            : 'assets/[name]-[hash].js',
        codeSplitting: {
          groups: [
            {
              name: 'vendor-react',
              test: /node_modules\/(react|react-dom|react-router|scheduler)\//,
              priority: 20,
            },
          ],
        },
      },
    },
  },
  server: {
    host: '127.0.0.1',
    cors: { origin: ['null', /^https?:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/] },
    port: 5173,
    strictPort: true,
    proxy: {
      '/plugin': {
        target,
        ws: true,
        changeOrigin: true,
      },
      '/plugin-frame': {
        target,
        changeOrigin: true,
      },
      '/api': {
        target,
        changeOrigin: true,
      },
      '/ws': {
        target,
        ws: true,
        changeOrigin: true,
      },
    },
  },
});
