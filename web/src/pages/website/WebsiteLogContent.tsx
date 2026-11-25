import { useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { useLogScroll } from '../../hooks/useLogScroll';
import { Alert, Grid, theme } from 'antd';
import { Maximize2, Minimize2 } from 'lucide-react';
import { MONO_FONT, SPACING, TERMINAL_COLORS } from '../../styles/tokens';

export default function WebsiteLogContent({
  content,
  loading,
  children,
  dark = true,
  truncated,
  fill = false,
  height = '45dvh',
}: {
  content?: string;
  loading: boolean;
  children?: ReactNode;
  dark?: boolean;
  truncated?: boolean;
  fill?: boolean;
  height?: string | number;
}) {
  const { token } = theme.useToken();
  const { md } = Grid.useBreakpoint();
  const [fullscreen, setFullscreen] = useState(false);
  const fillPage = fill && !fullscreen;
  const { ref, onScroll } = useLogScroll<HTMLDivElement>(
    content,
    true,
    !fillPage,
  );
  const boxNode = (
    <div
      className={[
        'panel-log-box-host',
        fillPage && 'website-log-box-fill',
        fullscreen && 'panel-log-box-host--fullscreen',
      ]
        .filter(Boolean)
        .join(' ')}
    >
      {(!md || fullscreen) && (
        <button
          type="button"
          className="panel-log-fullscreen-btn"
          aria-label={fullscreen ? '退出日志全屏' : '全屏查看日志'}
          onClick={() => setFullscreen((v) => !v)}
        >
          {fullscreen ? (
            <Minimize2 size={14} strokeWidth={2} />
          ) : (
            <Maximize2 size={14} strokeWidth={2} />
          )}
          <span>{fullscreen ? '退出全屏' : '全屏'}</span>
        </button>
      )}
      <div
        ref={ref}
        role="region"
        aria-label="日志内容"
        aria-busy={loading}
        tabIndex={0}
        className="panel-scroll-box"
        onScroll={onScroll}
        style={{
          height: fullscreen ? '100%' : fillPage ? 0 : height,
          flex: fullscreen || fillPage ? 1 : undefined,
          minHeight: fullscreen ? 0 : fillPage ? 200 : undefined,
          overflow: 'auto',
          overscrollBehavior: fillPage ? 'auto' : 'contain',
          WebkitOverflowScrolling: 'touch',
          touchAction: 'pan-y',
          borderRadius: fullscreen ? 0 : 12,
          background:
            dark || fullscreen
              ? TERMINAL_COLORS.background
              : token.colorFillAlter,
          color: dark || fullscreen ? TERMINAL_COLORS.foreground : undefined,
          border: fullscreen ? 'none' : `1px solid ${token.colorBorder}`,
          padding: SPACING.md,
          paddingTop: fullscreen ? 52 : SPACING.md,
        }}
      >
        {content ? (
          <pre
            style={{
              margin: 0,
              fontFamily: MONO_FONT,
              whiteSpace: 'pre-wrap',
              overflowWrap: 'anywhere',
            }}
          >
            {content}
          </pre>
        ) : (
          children
        )}
      </div>
    </div>
  );
  return (
    <>
      {truncated && (
        <Alert
          type="info"
          showIcon
          message="仅显示最近一段日志，完整内容可下载。"
        />
      )}
      {fullscreen && typeof document !== 'undefined'
        ? createPortal(boxNode, document.body)
        : boxNode}
    </>
  );
}
