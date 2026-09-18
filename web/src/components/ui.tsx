import type { ReactNode } from 'react'
import { labelOf } from '../format'

/** Small, shared presentational pieces. No state, no data fetching. */

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="loading">
      <span className="spinner" />
      <span>{label ?? '加载中…'}</span>
    </div>
  )
}

export function ErrorBox({ error }: { error: string | null }) {
  if (!error) return null
  return <div className="error-box">{error}</div>
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>
}

export function StatusBadge({ status }: { status: string | null | undefined }) {
  if (!status) return <span className="faint">—</span>
  const cls =
    status === 'success' || status === 'ok'
      ? 'badge ok'
      : status === 'failed' || status === 'error'
        ? 'badge err'
        : status === 'running'
          ? 'badge run'
          : 'badge'
  return <span className={cls}>{labelOf(status)}</span>
}

/**
 * Whether the backend's automatic cron dispatch is on.
 * Renders nothing while unknown (e.g. health not loaded yet).
 */
export function SchedulerBadge({ enabled }: { enabled: boolean | null | undefined }) {
  if (enabled === true) return <span className="badge ok">定时调度运行中</span>
  if (enabled === false) return <span className="badge run">定时调度已暂停</span>
  return null
}

export function Modal({
  title,
  onClose,
  children,
  footer,
  wide,
}: {
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  wide?: boolean
}) {
  return (
    <div
      className="overlay"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div className={wide ? 'modal wide' : 'modal'} role="dialog" aria-label={title}>
        <div className="modal-head">
          <div className="modal-title">{title}</div>
          <button type="button" className="btn ghost sm" onClick={onClose}>
            关闭
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer ? <div className="modal-foot">{footer}</div> : null}
      </div>
    </div>
  )
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
      {hint ? <div className="hint">{hint}</div> : null}
    </div>
  )
}
