/** Small formatting helpers. Every one of them tolerates null/undefined. */

const DASH = '—'

function toDate(ts: string | null | undefined): Date | null {
  if (!ts) return null
  const d = new Date(ts)
  return Number.isNaN(d.getTime()) ? null : d
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

/** "2026-09-16 14:03:22" in the viewer's local time. */
export function formatTs(ts: string | null | undefined): string {
  const d = toDate(ts)
  if (!d) return DASH
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  )
}

/** "3分钟前" / "2小时后" — used for next/last run columns. */
export function formatRelative(ts: string | null | undefined): string {
  const d = toDate(ts)
  if (!d) return DASH
  const diff = d.getTime() - Date.now()
  const past = diff < 0
  const secs = Math.abs(Math.round(diff / 1000))
  let value: string
  if (secs < 45) value = `${secs}秒`
  else if (secs < 3600) value = `${Math.round(secs / 60)}分钟`
  else if (secs < 86400) value = `${Math.round(secs / 3600)}小时`
  else value = `${Math.round(secs / 86400)}天`
  return past ? `${value}前` : `${value}后`
}

/** True only for a real, finite number — guards against nulls and stray strings. */
function isNum(n: unknown): n is number {
  return typeof n === 'number' && Number.isFinite(n)
}

export function formatDuration(ms: number | null | undefined): string {
  if (!isNum(ms)) return DASH
  if (ms < 1000) return `${Math.round(ms)}ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = Math.floor(s / 60)
  return `${m}m ${Math.round(s - m * 60)}s`
}

export function formatTokens(n: number | null | undefined): string {
  if (!isNum(n)) return DASH
  return String(n)
}

export function tokenSummary(
  input: number | null | undefined,
  output: number | null | undefined,
): string {
  const hasIn = isNum(input)
  const hasOut = isNum(output)
  if (!hasIn && !hasOut) return ''
  return `入 ${hasIn ? input : 0} / 出 ${hasOut ? output : 0}`
}

/** Chinese labels for API enums shown in the UI. Unknown values pass through. */
const LABELS: Record<string, string> = {
  success: '成功',
  ok: '正常',
  failed: '失败',
  error: '错误',
  running: '运行中',
  active: '进行中',
  schedule: '定时',
  manual: '手动',
  user: '用户',
  assistant: '助手',
  message: '消息',
  reasoning: '推理',
  command: '命令',
  file_change: '改文件',
  tool: '工具',
  other: '其他',
  'read-only': '只读',
  'workspace-write': '工作区可写',
  'danger-full-access': '完全访问',
  none: '无',
  minimal: '最低',
  low: '低',
  medium: '中',
  high: '高',
  xhigh: '极高',
  max: '最大',
  untrusted: '不信任',
  'on-failure': '失败时批准',
  'on-request': '请求时批准',
  never: '从不批准',
}

export function labelOf(value: string | null | undefined): string {
  if (value === null || value === undefined) return DASH
  const trimmed = value.trim()
  if (trimmed === '') return DASH
  return LABELS[trimmed] ?? value
}

export function text(value: string | null | undefined): string {
  if (value === null || value === undefined) return DASH
  const trimmed = value.trim()
  return trimmed === '' ? DASH : value
}

export function truncate(value: string | null | undefined, max = 80): string {
  if (!value) return DASH
  const oneLine = value.replace(/\s+/g, ' ').trim()
  if (oneLine === '') return DASH
  return oneLine.length > max ? `${oneLine.slice(0, max - 1)}…` : oneLine
}
