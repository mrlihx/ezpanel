import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type HTMLAttributes,
} from 'react';

export function useFloatingWindow(options: {
  width: number;
  height: number;
  minWidth: number;
  minHeight: number;
}) {
  const [full, setFull] = useState(
    () => typeof window !== 'undefined' && window.innerWidth <= 767,
  );
  const [rect, setRect] = useState(() => {
    const safeTop = window.innerWidth <= 767 ? 12 : 0;
    const width = Math.min(options.width, window.innerWidth - 16);
    const height = Math.min(
      options.height,
      window.innerHeight - safeTop * 2 - 16,
    );
    return {
      width,
      height,
      x: Math.max(8, (window.innerWidth - width) / 2),
      y: Math.max(safeTop, (window.innerHeight - height) / 2),
    };
  });
  const drag = useRef<{
    x: number;
    y: number;
    mouseX: number;
    mouseY: number;
  } | null>(null);
  const observer = useRef<ResizeObserver>();
  const observeWindow = useCallback((node: HTMLDivElement | null) => {
    observer.current?.disconnect();
    if (!node) return;
    observer.current = new ResizeObserver(([entry]) => {
      if (node.dataset.maximized === 'true') return;
      const { width, height } = entry.contentRect;
      if (!width || !height) return;
      setRect((current) =>
        current.width === width && current.height === height
          ? current
          : { ...current, width, height },
      );
    });
    observer.current.observe(node);
  }, []);
  useEffect(() => {
    const fit = () =>
      setRect((current) => {
        const width = Math.min(current.width, window.innerWidth);
        const height = Math.min(current.height, window.innerHeight);
        return {
          width,
          height,
          x: Math.max(0, Math.min(current.x, window.innerWidth - width)),
          y: Math.max(0, Math.min(current.y, window.innerHeight - height)),
        };
      });
    window.addEventListener('resize', fit);
    return () => {
      window.removeEventListener('resize', fit);
    };
  }, []);
  const titleProps: HTMLAttributes<HTMLDivElement> = {
    style: {
      cursor: full ? 'default' : 'move',
      touchAction: 'none',
      userSelect: 'none',
    },
    onPointerDown: (event) => {
      if (
        full ||
        event.button !== 0 ||
        (event.target as HTMLElement).closest('button')
      )
        return;
      drag.current = {
        x: rect.x,
        y: rect.y,
        mouseX: event.clientX,
        mouseY: event.clientY,
      };
      event.currentTarget.setPointerCapture(event.pointerId);
      event.preventDefault();
    },
    onPointerMove: (event) => {
      const start = drag.current;
      if (!start) return;
      setRect((current) => ({
        ...current,
        x: Math.max(
          0,
          Math.min(
            window.innerWidth - current.width,
            start.x + event.clientX - start.mouseX,
          ),
        ),
        y: Math.max(
          0,
          Math.min(
            window.innerHeight - current.height,
            start.y + event.clientY - start.mouseY,
          ),
        ),
      }));
    },
    onPointerUp: (event) => {
      drag.current = null;
      if (event.currentTarget.hasPointerCapture(event.pointerId))
        event.currentTarget.releasePointerCapture(event.pointerId);
    },
    onLostPointerCapture: () => {
      drag.current = null;
    },
    onDoubleClick: (event) => {
      if (!(event.target as HTMLElement).closest('button'))
        setFull((current) => !current);
    },
  };
  const frameStyle: CSSProperties = {
    width: full ? '100vw' : rect.width,
    height: full ? '100dvh' : rect.height,
    resize: full ? 'none' : 'both',
    pointerEvents: 'auto',
    overflow: 'hidden',
    minWidth: full
      ? undefined
      : Math.min(options.minWidth, window.innerWidth - rect.x),
    minHeight: full
      ? undefined
      : Math.min(options.minHeight, window.innerHeight - rect.y),
    maxWidth: `calc(100vw - ${full ? 0 : rect.x}px)`,
    maxHeight: `calc(100dvh - ${full ? 0 : rect.y}px)`,
  };
  const modalStyle: CSSProperties = {
    top: full ? 0 : rect.y,
    left: full ? 0 : rect.x,
    margin: 0,
    maxWidth: 'none',
    paddingBottom: 0,
  };
  return {
    full,
    toggleFull: () => setFull((current) => !current),
    titleProps,
    width: full ? '100vw' : rect.width,
    modalStyle,
    frameProps: {
      ref: observeWindow,
      'data-maximized': String(full),
      style: frameStyle,
    },
  };
}
