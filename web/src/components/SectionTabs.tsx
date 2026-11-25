import { Card, Tabs, theme, type TabsProps } from 'antd';
import { useEffect, useRef, type CSSProperties } from 'react';
import './SectionTabs.css';

export default function SectionTabs({
  cardContent = false,
  items,
  rootClassName,
  tabBarGutter = 4,
  style,
  activeKey,
  onTabClick,
  ...props
}: TabsProps & { cardContent?: boolean }) {
  const { token } = theme.useToken();
  const containerRef = useRef<HTMLDivElement>(null);
  const savedScrollLeftRef = useRef(0);
  const hasContent = items?.some((item) => item.children != null);

  const scrollActiveTabIntoCenter = (targetEl?: HTMLElement | null) => {
    const root = containerRef.current;
    if (!root) return;
    const navWrap = root.querySelector<HTMLElement>(
      '.section-tabs > .ant-tabs-nav .ant-tabs-nav-wrap',
    );
    const activeTab =
      targetEl ??
      navWrap?.querySelector<HTMLElement>('.ant-tabs-tab.ant-tabs-tab-active');
    if (!navWrap || !activeTab) return;
    if (navWrap.scrollWidth <= navWrap.clientWidth + 2) return;

    // 如果因为 focus 事件被 rc-tabs 重置成了 0，先立刻恢复到点击前的滚动位置再平滑居中
    if (navWrap.scrollLeft === 0 && savedScrollLeftRef.current > 0) {
      navWrap.scrollLeft = savedScrollLeftRef.current;
    }

    const targetLeft =
      activeTab.offsetLeft - (navWrap.clientWidth - activeTab.offsetWidth) / 2;
    const maxScroll = Math.max(0, navWrap.scrollWidth - navWrap.clientWidth);
    const clampedLeft = Math.max(0, Math.min(maxScroll, targetLeft));
    savedScrollLeftRef.current = clampedLeft;
    navWrap.scrollTo({
      left: clampedLeft,
      behavior: 'smooth',
    });
  };

  useEffect(() => {
    const root = containerRef.current;
    const navWrap = root?.querySelector<HTMLElement>(
      '.section-tabs > .ant-tabs-nav .ant-tabs-nav-wrap',
    );
    if (!navWrap) return;
    const onScroll = () => {
      if (navWrap.scrollLeft > 0) {
        savedScrollLeftRef.current = navWrap.scrollLeft;
      }
    };
    const onPointerDown = () => {
      savedScrollLeftRef.current = navWrap.scrollLeft;
    };
    navWrap.addEventListener('scroll', onScroll, { passive: true });
    navWrap.addEventListener('pointerdown', onPointerDown, {
      passive: true,
      capture: true,
    });
    navWrap.addEventListener('touchstart', onPointerDown, {
      passive: true,
      capture: true,
    });
    return () => {
      navWrap.removeEventListener('scroll', onScroll);
      navWrap.removeEventListener('pointerdown', onPointerDown, {
        capture: true,
      });
      navWrap.removeEventListener('touchstart', onPointerDown, {
        capture: true,
      });
    };
  }, []);

  useEffect(() => {
    const id = window.requestAnimationFrame(() => {
      scrollActiveTabIntoCenter();
    });
    return () => window.cancelAnimationFrame(id);
  }, [activeKey, items?.length]);

  return (
    <div ref={containerRef} className="section-tabs-host">
      <Tabs
        {...props}
        activeKey={activeKey}
        onTabClick={(key, event) => {
          onTabClick?.(key, event);
          const tabEl = (
            event.currentTarget as HTMLElement | null
          )?.closest<HTMLElement>('.ant-tabs-tab');
          window.requestAnimationFrame(() => {
            scrollActiveTabIntoCenter(tabEl);
          });
        }}
        rootClassName={[
          'section-tabs',
          hasContent && 'section-tabs-with-content',
          rootClassName,
        ]
          .filter(Boolean)
          .join(' ')}
        tabBarGutter={tabBarGutter}
        style={
          {
            '--section-tabs-surface': token.colorBgContainer,
            '--section-tabs-border': token.colorBorderSecondary,
            '--section-tabs-radius': `${token.borderRadiusLG}px`,
            '--section-tabs-hover': token.colorFillTertiary,
            '--section-tabs-selected-bg': token.colorPrimaryBg,
            '--section-tabs-selected-text': token.colorPrimaryText,
            '--section-tabs-focus': token.colorPrimary,
            ...style,
          } as CSSProperties
        }
        items={
          cardContent
            ? items?.map((item) => ({
                ...item,
                children: <Card>{item.children}</Card>,
              }))
            : items
        }
      />
    </div>
  );
}
