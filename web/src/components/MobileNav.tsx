import { useEffect, useRef, useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import type { RefObject } from 'react'
import { GITHUB_URL, NAV } from '../App'

/**
 *窄屏（≤820px）导航形态：顶栏 + 抽屉（见 CONTEXT.md）。
 *
 * 两棵树各自独立：这里渲染顶栏/抽屉/遮罩，`App.tsx` 渲染桌面侧栏；由同一条
 * 820px 媒体查询决定哪一棵可见。导航项与仓库链接地址都来自 `App.tsx` 的
 * 模块级导出，两棵树消费同一份定义。
 *
 * 抽屉与遮罩始终在 DOM 里（关闭时 `display: none`），这样媒体查询、焦点管理
 * 与 `aria-controls` 都无需条件渲染。
 */

/* 与桌面侧栏品牌区同一份文案；侧栏那里的字面量保持不变。 */
const BRAND_NAME = 'Codeck'
const BRAND_SUB = '本机 Codex 控制台'

/** 当前页在顶栏显示的短名：优先精确匹配，其次前缀匹配。 */
function currentLabel(pathname: string): string {
  const hit = NAV.find(
    (item) =>
      item.end
        ? pathname === item.to
        : pathname === item.to || pathname.startsWith(`${item.to}/`),
  )
  return hit ? hit.label : ''
}

export function MobileNav({ mainRef }: { mainRef: RefObject<HTMLElement | null> }) {
  const location = useLocation()
  /* 记录抽屉是在哪个 pathname 上打开的：路由一变，open 自动变 false。 */
  const [openPath, setOpenPath] = useState<string | null>(null)
  const open = openPath !== null && openPath === location.pathname

  const burgerRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  /* 只在开合真正翻转时做焦点/滚动副作用，首帧不触发。 */
  const wasOpenRef = useRef(false)

  useEffect(() => {
    if (open === wasOpenRef.current) return
    wasOpenRef.current = open
    const main = mainRef.current
    /* 滚动锁用 class 而不是行内样式：`overflow` 规则写在媒体查询内部，
       跨过 820px 进入桌面布局时自动不再匹配，不会出现"抽屉不可见却
       锁着滚动"。锁定的是 `.main`（唯一滚动容器），不是 body。 */
    if (main) main.classList.toggle('nav-locked', open)
    if (open) {
      panelRef.current?.focus()
    } else {
      burgerRef.current?.focus()
    }
  }, [open, mainRef])

  useEffect(() => {
    if (!open) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpenPath(null)
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [open])

  return (
    <>
      <header className="mobile-bar">
        <button
          type="button"
          ref={burgerRef}
          className="mobile-bar-btn"
          aria-expanded={open}
          aria-controls="mobile-drawer"
          aria-label={open ? '收起导航' : '展开导航'}
          onClick={() => setOpenPath(open ? null : location.pathname)}
        >
          <BurgerIcon />
        </button>
        <div className="mobile-bar-title">{currentLabel(location.pathname)}</div>
        <a
          className="mobile-bar-github"
          href={GITHUB_URL}
          target="_blank"
          rel="noreferrer"
          title={GITHUB_URL}
          aria-label="GitHub 仓库"
        >
          <GithubIcon />
        </a>
      </header>

      <div
        className={open ? 'drawer-scrim open' : 'drawer-scrim'}
        aria-hidden="true"
        onClick={() => setOpenPath(null)}
      />

      <div
        id="mobile-drawer"
        className={open ? 'drawer open' : 'drawer'}
        ref={panelRef}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label="页面导航"
      >
        <div className="drawer-brand">
          <div className="brand-name">{BRAND_NAME}</div>
          <div className="brand-sub">{BRAND_SUB}</div>
        </div>
        <nav className="drawer-nav">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                isActive ? 'drawer-item active' : 'drawer-item'
              }
              onClick={() => setOpenPath(null)}
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="drawer-foot">
          <a
            className="drawer-github"
            href={GITHUB_URL}
            target="_blank"
            rel="noreferrer"
            title={GITHUB_URL}
          >
            <GithubIcon />
            GitHub 仓库
          </a>
        </div>
      </div>
    </>
  )
}

function BurgerIcon() {
  return (
    <svg
      className="mobile-bar-icon"
      viewBox="0 0 16 16"
      width="18"
      height="18"
      aria-hidden="true"
    >
      <path fill="currentColor" d="M1 3h14v2H1zM1 7h14v2H1zM1 11h14v2H1z" />
    </svg>
  )
}

function GithubIcon() {
  return (
    <svg
      className="github-icon"
      viewBox="0 0 16 16"
      width="12"
      height="12"
      aria-hidden="true"
    >
      <path
        fill="currentColor"
        d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
      />
    </svg>
  )
}
