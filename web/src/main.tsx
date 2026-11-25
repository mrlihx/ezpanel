import './plugins/shared';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import './styles/global.css';

// 修复 Ant Design rc-tabs 在横向原生滚动时，Tab 按钮获取焦点（focus 先于 click 触发）时
// 浏览器 scrollIntoView(0) + rc-tabs 强制执行 navWrap.scrollLeft = 0 导致滚动栏瞬间弹回左侧、点击落空的 Bug
if (typeof window !== 'undefined' && typeof HTMLElement !== 'undefined') {
  let inTabFocusCycle = false;
  const markTabFocusCycle = () => {
    inTabFocusCycle = true;
    window.setTimeout(() => {
      inTabFocusCycle = false;
    }, 64);
  };

  document.addEventListener(
    'focusin',
    (e) => {
      if (
        e.target instanceof HTMLElement &&
        e.target.closest('.ant-tabs-nav-wrap')
      ) {
        markTabFocusCycle();
      }
    },
    { capture: true, passive: true },
  );

  const proto = HTMLElement.prototype;
  const origFocus = proto.focus;
  proto.focus = function (options?: FocusOptions) {
    if (
      this.classList?.contains('ant-tabs-tab-btn') ||
      this.closest?.('.ant-tabs-nav-wrap')
    ) {
      markTabFocusCycle();
      return origFocus.call(this, { ...options, preventScroll: true });
    }
    return origFocus.call(this, options);
  };

  const scrollLeftDesc =
    Object.getOwnPropertyDescriptor(Element.prototype, 'scrollLeft') ||
    Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollLeft');
  if (scrollLeftDesc?.set && scrollLeftDesc?.get) {
    Object.defineProperty(HTMLElement.prototype, 'scrollLeft', {
      configurable: true,
      enumerable: true,
      get() {
        return scrollLeftDesc.get!.call(this);
      },
      set(val: number) {
        if (
          inTabFocusCycle &&
          val === 0 &&
          this instanceof HTMLElement &&
          this.classList.contains('ant-tabs-nav-wrap') &&
          scrollLeftDesc.get!.call(this) > 2
        ) {
          // 仅在 Tab 焦点切换周期内阻止 rc-tabs onFocus 的 tabsWrapperRef.current.scrollLeft = 0 强制弹回
          return;
        }
        scrollLeftDesc.set!.call(this, val);
      },
    });
  }
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
