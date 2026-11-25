import type { FileEntry } from '../../api/file';

type BadgeSpec = {
  label: string;
  accent: string;
  textColor?: string;
  fontSize?: number;
};

function PageBadge({ label, accent, textColor = '#fff', fontSize }: BadgeSpec) {
  const size =
    fontSize ?? (label.length >= 4 ? 5.1 : label.length === 3 ? 6 : 7);
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      className="file-type-svg"
      aria-hidden="true"
    >
      <path
        d="M6 2.75h7.6L19.25 8.4v12.85A1.75 1.75 0 0 1 17.5 23h-11A1.75 1.75 0 0 1 4.75 21.25V4.5A1.75 1.75 0 0 1 6.5 2.75Z"
        fill="var(--ant-color-bg-container, #fff)"
        stroke={accent}
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <path
        d="M13.5 2.85V8.5h5.65"
        stroke={accent}
        strokeWidth="1.35"
        strokeLinejoin="round"
      />
      <rect
        x="2.25"
        y="11.5"
        width="19.5"
        height="8.5"
        rx="2.25"
        fill={accent}
      />
      <text
        x="12"
        y="17.55"
        textAnchor="middle"
        fill={textColor}
        fontSize={size}
        fontWeight="700"
        fontFamily="ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
        letterSpacing="-0.2"
      >
        {label}
      </text>
    </svg>
  );
}

function FolderIcon({ isLink }: { isLink?: boolean }) {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      className="file-type-svg"
      aria-hidden="true"
    >
      <path
        d="M3 6.5A2.25 2.25 0 0 1 5.25 4.25h4.3c.55 0 1.07.25 1.41.68l1.1 1.37c.19.24.48.38.79.38h5.9A2.25 2.25 0 0 1 21 8.93v8.82A2.25 2.25 0 0 1 18.75 20H5.25A2.25 2.25 0 0 1 3 17.75V6.5Z"
        fill="#F5A623"
        fillOpacity="0.92"
      />
      <path
        d="M3 9.25h18v8.5A2.25 2.25 0 0 1 18.75 20H5.25A2.25 2.25 0 0 1 3 17.75v-8.5Z"
        fill="#F7B733"
      />
      {isLink && (
        <>
          <circle cx="18.2" cy="17.8" r="4" fill="#1677FF" />
          <path
            d="M16.8 19.2l2.8-2.8m0 0h-2.1m2.1 0v2.1"
            stroke="#fff"
            strokeWidth="1.4"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </>
      )}
    </svg>
  );
}

function ArchiveIcon({ label }: { label: string }) {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      className="file-type-svg"
      aria-hidden="true"
    >
      <rect
        x="3.5"
        y="3"
        width="17"
        height="18"
        rx="2.5"
        fill="#FFF7ED"
        stroke="#EA580C"
        strokeWidth="1.4"
      />
      <path
        d="M11 3.5h2v2h-2v-2Zm0 3h2v2h-2v-2Zm0 3h2v2h-2v-2Z"
        fill="#EA580C"
      />
      <rect x="2.25" y="12" width="19.5" height="8" rx="2" fill="#EA580C" />
      <text
        x="12"
        y="17.8"
        textAnchor="middle"
        fill="#fff"
        fontSize={label.length >= 4 ? 5.2 : 6.2}
        fontWeight="700"
        fontFamily="ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
      >
        {label}
      </text>
    </svg>
  );
}

function ShellIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      className="file-type-svg"
      aria-hidden="true"
    >
      <rect
        x="2.75"
        y="3.75"
        width="18.5"
        height="16.5"
        rx="3"
        fill="#0F172A"
        stroke="#10B981"
        strokeWidth="1.4"
      />
      <path
        d="M7 9.5l3.2 2.5L7 14.5"
        stroke="#34D399"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="M12.5 14.5h4.2"
        stroke="#E2E8F0"
        strokeWidth="1.8"
        strokeLinecap="round"
      />
    </svg>
  );
}

function ImageIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      className="file-type-svg"
      aria-hidden="true"
    >
      <rect
        x="3"
        y="3.5"
        width="18"
        height="17"
        rx="2.6"
        fill="#F5F3FF"
        stroke="#8B5CF6"
        strokeWidth="1.4"
      />
      <circle cx="8.6" cy="8.8" r="1.7" fill="#F59E0B" />
      <path
        d="M4.5 18.2l4.6-4.9a1.2 1.2 0 0 1 1.75 0l2.15 2.3 2.55-2.75a1.2 1.2 0 0 1 1.76 0L19.5 15.2v2.8a1.2 1.2 0 0 1-1.2 1.2H5.7a1.2 1.2 0 0 1-1.2-1Z"
        fill="#8B5CF6"
        fillOpacity="0.82"
      />
    </svg>
  );
}

function MediaIcon({ kind }: { kind: 'video' | 'audio' }) {
  const accent = kind === 'video' ? '#E11D48' : '#0EA5E9';
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      className="file-type-svg"
      aria-hidden="true"
    >
      <rect
        x="3"
        y="3.5"
        width="18"
        height="17"
        rx="3"
        fill={accent}
        fillOpacity="0.14"
        stroke={accent}
        strokeWidth="1.4"
      />
      {kind === 'video' ? (
        <path d="M10 8.8l6 3.2-6 3.2V8.8Z" fill={accent} />
      ) : (
        <path
          d="M10.5 15.5a1.8 1.8 0 1 1-1.8-1.8c.35 0 .68.1.95.27V8.5l6-1.2v6.4a1.8 1.8 0 1 1-1.8-1.8c.35 0 .68.1.95.27V9.3l-4.3.85v5.35Z"
          fill={accent}
        />
      )}
    </svg>
  );
}

function DatabaseFileIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      className="file-type-svg"
      aria-hidden="true"
    >
      <ellipse
        cx="12"
        cy="6.5"
        rx="7"
        ry="2.8"
        fill="#6366F1"
        fillOpacity="0.2"
        stroke="#4F46E5"
        strokeWidth="1.4"
      />
      <path
        d="M5 6.5v11c0 1.55 3.13 2.8 7 2.8s7-1.25 7-2.8v-11"
        stroke="#4F46E5"
        strokeWidth="1.4"
      />
      <path
        d="M5 12c0 1.55 3.13 2.8 7 2.8s7-1.25 7-2.8"
        stroke="#4F46E5"
        strokeWidth="1.35"
      />
    </svg>
  );
}

function KeyCertIcon({ isKey }: { isKey: boolean }) {
  const accent = isKey ? '#D97706' : '#059669';
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      className="file-type-svg"
      aria-hidden="true"
    >
      <path
        d="M12 2.8l7.5 3v5.4c0 4.6-3.1 8.4-7.5 9.8-4.4-1.4-7.5-5.2-7.5-9.8V5.8l7.5-3Z"
        fill={accent}
        fillOpacity="0.14"
        stroke={accent}
        strokeWidth="1.45"
        strokeLinejoin="round"
      />
      {isKey ? (
        <path
          d="M10.8 11.2a1.8 1.8 0 1 1 2.4 0l.4 3.5h-3.2l.4-3.5Z"
          fill={accent}
        />
      ) : (
        <path
          d="M9 12.2l2 2 4.2-4.2"
          stroke={accent}
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      )}
    </svg>
  );
}

function DefaultFileIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      className="file-type-svg"
      aria-hidden="true"
    >
      <path
        d="M6.5 2.75h7.1L19.25 8.4v12.85A1.75 1.75 0 0 1 17.5 23h-11a1.75 1.75 0 0 1-1.75-1.75V4.5A1.75 1.75 0 0 1 6.5 2.75Z"
        fill="var(--ant-color-bg-container, #fff)"
        stroke="#94A3B8"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <path
        d="M13.5 2.85V8.5h5.65"
        stroke="#94A3B8"
        strokeWidth="1.35"
        strokeLinejoin="round"
      />
      <path
        d="M8 12.5h8M8 16h5.5"
        stroke="#94A3B8"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}

const BADGE_BY_EXT: Record<string, BadgeSpec> = {
  go: { label: 'GO', accent: '#00ADD8' },
  php: { label: 'PHP', accent: '#777BB4' },
  phtml: { label: 'PHP', accent: '#777BB4' },
  js: { label: 'JS', accent: '#EAB308', textColor: '#1E293B' },
  mjs: { label: 'JS', accent: '#EAB308', textColor: '#1E293B' },
  cjs: { label: 'JS', accent: '#EAB308', textColor: '#1E293B' },
  jsx: { label: 'JSX', accent: '#06B6D4' },
  ts: { label: 'TS', accent: '#3178C6' },
  mts: { label: 'TS', accent: '#3178C6' },
  cts: { label: 'TS', accent: '#3178C6' },
  tsx: { label: 'TSX', accent: '#2563EB' },
  css: { label: 'CSS', accent: '#2563EB' },
  scss: { label: 'SCSS', accent: '#DB2777' },
  sass: { label: 'SASS', accent: '#DB2777' },
  less: { label: 'LESS', accent: '#1D4ED8' },
  html: { label: 'HTML', accent: '#EA580C' },
  htm: { label: 'HTML', accent: '#EA580C' },
  vue: { label: 'VUE', accent: '#10B981' },
  svelte: { label: 'SVLT', accent: '#FF3E00' },
  py: { label: 'PY', accent: '#3776AB' },
  pyw: { label: 'PY', accent: '#3776AB' },
  json: { label: 'JSON', accent: '#D97706' },
  yaml: { label: 'YML', accent: '#E11D48' },
  yml: { label: 'YML', accent: '#E11D48' },
  toml: { label: 'TOML', accent: '#64748B' },
  xml: { label: 'XML', accent: '#F97316' },
  ini: { label: 'INI', accent: '#64748B' },
  conf: { label: 'CONF', accent: '#475569' },
  cfg: { label: 'CFG', accent: '#475569' },
  cnf: { label: 'CNF', accent: '#475569' },
  env: { label: 'ENV', accent: '#0D9488' },
  md: { label: 'MD', accent: '#3B82F6' },
  mdx: { label: 'MDX', accent: '#3B82F6' },
  pdf: { label: 'PDF', accent: '#DC2626' },
  log: { label: 'LOG', accent: '#6B7280' },
  txt: { label: 'TXT', accent: '#64748B' },
  csv: { label: 'CSV', accent: '#16A34A' },
  java: { label: 'JAVA', accent: '#DC2626' },
  c: { label: 'C', accent: '#2563EB' },
  cpp: { label: 'C++', accent: '#1D4ED8' },
  h: { label: 'H', accent: '#7C3AED' },
  rs: { label: 'RS', accent: '#C2410C' },
  rb: { label: 'RB', accent: '#DC2626' },
};

const ARCHIVE_EXTS: Record<string, string> = {
  zip: 'ZIP',
  tar: 'TAR',
  gz: 'GZ',
  tgz: 'TGZ',
  bz2: 'BZ2',
  xz: 'XZ',
  '7z': '7Z',
  rar: 'RAR',
  zst: 'ZST',
};

const SHELL_EXTS = new Set(['sh', 'bash', 'zsh', 'fish']);
const IMAGE_EXTS = new Set([
  'png',
  'jpg',
  'jpeg',
  'gif',
  'webp',
  'svg',
  'ico',
  'bmp',
  'avif',
]);
const VIDEO_EXTS = new Set(['mp4', 'mkv', 'mov', 'webm', 'avi', 'flv', 'm4v']);
const AUDIO_EXTS = new Set(['mp3', 'wav', 'flac', 'ogg', 'aac', 'm4a']);
const DB_EXTS = new Set(['sql', 'sqlite', 'sqlite3', 'db']);
const CERT_EXTS = new Set(['pem', 'crt', 'cer', 'p12', 'pfx']);
const KEY_EXTS = new Set(['key', 'pub']);

export default function FileIcon({ entry }: { entry: FileEntry }) {
  if (entry.isDir || entry.linkIsDir) {
    return <FolderIcon isLink={entry.isLink} />;
  }
  const lower = entry.name.toLowerCase();

  // 特殊文件名匹配
  if (lower === 'dockerfile' || lower.startsWith('dockerfile.')) {
    return <PageBadge label="DKR" accent="#2496ED" />;
  }
  if (
    lower === 'compose.yaml' ||
    lower === 'compose.yml' ||
    lower.startsWith('docker-compose')
  ) {
    return <PageBadge label="DKR" accent="#2496ED" />;
  }
  if (lower === 'makefile' || lower === 'gnumakefile') {
    return <PageBadge label="MAKE" accent="#0D9488" />;
  }
  if (lower === 'go.mod' || lower === 'go.sum') {
    return <PageBadge label="GO" accent="#00ADD8" />;
  }
  if (lower === '.env' || lower.startsWith('.env.')) {
    return <PageBadge label="ENV" accent="#0D9488" />;
  }
  if (lower.endsWith('.tar.gz')) {
    return <ArchiveIcon label="TGZ" />;
  }
  if (lower.endsWith('.tar.bz2') || lower.endsWith('.tar.xz')) {
    return <ArchiveIcon label="TAR" />;
  }

  const dot = lower.lastIndexOf('.');
  const ext = dot > 0 ? lower.slice(dot + 1) : '';

  if (ext in ARCHIVE_EXTS) {
    return <ArchiveIcon label={ARCHIVE_EXTS[ext]} />;
  }
  if (SHELL_EXTS.has(ext)) {
    return <ShellIcon />;
  }
  if (IMAGE_EXTS.has(ext)) {
    return <ImageIcon />;
  }
  if (VIDEO_EXTS.has(ext)) {
    return <MediaIcon kind="video" />;
  }
  if (AUDIO_EXTS.has(ext)) {
    return <MediaIcon kind="audio" />;
  }
  if (DB_EXTS.has(ext)) {
    return <DatabaseFileIcon />;
  }
  if (CERT_EXTS.has(ext)) {
    return <KeyCertIcon isKey={false} />;
  }
  if (KEY_EXTS.has(ext)) {
    return <KeyCertIcon isKey />;
  }
  if (ext in BADGE_BY_EXT) {
    return <PageBadge {...BADGE_BY_EXT[ext]} />;
  }

  return <DefaultFileIcon />;
}
