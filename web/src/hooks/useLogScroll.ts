import { useEffect, useLayoutEffect, useRef, type UIEvent } from 'react';

export function useLogScroll<T extends HTMLElement>(
  content: unknown,
  enabled = true,
  lockTouch = true,
) {
  const ref = useRef<T>(null);
  const follow = useRef(true);
  const lastYRef = useRef(0);
  const boundElRef = useRef<T | null>(null);
  const cleanupRef = useRef<(() => void) | null>(null);

  useLayoutEffect(() => {
    const view = ref.current;
    if (enabled && view && follow.current) view.scrollTop = view.scrollHeight;
  }, [content, enabled]);

  useEffect(() => {
    const el = lockTouch ? ref.current : null;
    if (boundElRef.current === el) return;
    cleanupRef.current?.();
    boundElRef.current = el;
    cleanupRef.current = null;
    if (!el) return;

    const onTouchStart = (e: TouchEvent) => {
      if (e.touches.length === 1) lastYRef.current = e.touches[0].clientY;
    };
    const onTouchMove = (e: TouchEvent) => {
      if (e.touches.length !== 1) return;
      const currentY = e.touches[0].clientY;
      const deltaY = lastYRef.current - currentY;
      lastYRef.current = currentY;
      const canScrollVertically = el.scrollHeight > el.clientHeight + 1;
      e.stopPropagation();
      if (!canScrollVertically) {
        if (e.cancelable) e.preventDefault();
        return;
      }
      const atTop = el.scrollTop <= 0 && deltaY < 0;
      const atBottom =
        el.scrollTop + el.clientHeight >= el.scrollHeight - 1 && deltaY > 0;
      if ((atTop || atBottom) && e.cancelable) {
        e.preventDefault();
      }
    };
    el.addEventListener('touchstart', onTouchStart, { passive: true });
    el.addEventListener('touchmove', onTouchMove, { passive: false });
    cleanupRef.current = () => {
      el.removeEventListener('touchstart', onTouchStart);
      el.removeEventListener('touchmove', onTouchMove);
    };
  });

  useEffect(() => {
    return () => {
      cleanupRef.current?.();
      cleanupRef.current = null;
      boundElRef.current = null;
    };
  }, []);

  return {
    ref,
    reset: () => {
      follow.current = true;
    },
    onScroll: ({ currentTarget }: UIEvent<T>) => {
      follow.current =
        currentTarget.scrollHeight -
          currentTarget.scrollTop -
          currentTarget.clientHeight <=
        16;
    },
  };
}
