import { useEffect, useState, type ComponentPropsWithoutRef } from 'react';
import { createPortal } from 'react-dom';
import { Grid } from 'antd';
import { Maximize2, Minimize2 } from 'lucide-react';
import { useLogScroll } from '../hooks/useLogScroll';
import { MONO_FONT, TERMINAL_COLORS } from '../styles/tokens';

export default function LogText({
  children,
  follow = true,
  className,
  style,
  ...props
}: ComponentPropsWithoutRef<'pre'> & {
  follow?: boolean;
}) {
  const { ref, onScroll } = useLogScroll<HTMLPreElement>(children, follow);
  const { md } = Grid.useBreakpoint();
  const [fullscreen, setFullscreen] = useState(false);

  useEffect(() => {
    if (!fullscreen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setFullscreen(false);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [fullscreen]);

  const node = (
    <div
      className={[
        'panel-log-box-host',
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
      <pre
        aria-label="日志内容"
        tabIndex={0}
        className={['panel-scroll-box', className].filter(Boolean).join(' ')}
        style={{
          fontFamily: MONO_FONT,
          background: TERMINAL_COLORS.background,
          color: TERMINAL_COLORS.foreground,
          padding: 16,
          borderRadius: 14,
          margin: 0,
          overflow: 'auto',
          whiteSpace: 'pre-wrap',
          overflowWrap: 'anywhere',
          overscrollBehavior: 'contain',
          WebkitOverflowScrolling: 'touch',
          touchAction: 'pan-y',
          ...style,
          ...(fullscreen
            ? {
                height: '100%',
                maxHeight: 'none',
                minHeight: 0,
                flex: 1,
                borderRadius: 0,
                margin: 0,
                padding: 16,
                paddingTop: 52,
                fontFamily: MONO_FONT,
                background: TERMINAL_COLORS.background,
                color: TERMINAL_COLORS.foreground,
              }
            : null),
        }}
        {...props}
        ref={ref}
        onScroll={onScroll}
      >
        {children}
      </pre>
    </div>
  );

  if (fullscreen && typeof document !== 'undefined') {
    return createPortal(node, document.body);
  }
  return node;
}
