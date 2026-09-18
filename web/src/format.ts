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

/** Compact Chinese counts for large token totals: 151万, 3.02亿. */
export function formatCompact(n: number | null | undefined): string {
  if (!isNum(n)) return DASH
  const sign = n < 0 ? '-' : ''
  const abs = Math.abs(n)
  if (abs >= 1e8) return `${sign}${trimFloat(abs / 1e8)}亿`
  if (abs >= 1e4) return `${sign}${trimFloat(abs / 1e4)}万`
  return sign + String(Math.round(abs))
}

function trimFloat(n: number): string {
  const digits = n >= 100 ? 0 : n >= 10 ? 1 : 2
  return n.toFixed(digits).replace(/\.0+$/, '').replace(/(\.\d*[1-9])0+$/, '$1')
}

/** Equivalent API dollars. Tiny amounts keep extra digits so luna-scale costs stay visible. */
export function formatUSD(n: number | null | undefined): string {
  if (!isNum(n)) return DASH
  const sign = n < 0 ? '-' : ''
  const abs = Math.abs(n)
  if (abs === 0) return '$0'
  if (abs >= 100) return `${sign}$${abs.toFixed(0)}`
  if (abs >= 1) return `${sign}$${abs.toFixed(2)}`
  if (abs >= 0.01) return `${sign}$${abs.toFixed(4)}`
  return `${sign}$${abs.toFixed(6)}`
}

/** Unix seconds → "3小时后". */
export function formatUnixRelative(sec: number | null | undefined): string {
  if (!isNum(sec) || sec <= 0) return DASH
  return formatRelative(new Date(sec * 1000).toISOString())
}

/** Unix seconds → "2026-09-16". */
export function formatUnixDate(sec: number | null | undefined): string {
  if (!isNum(sec) || sec <= 0) return DASH
  const d = new Date(sec * 1000)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** Unix seconds → "2026-09-16 14:03" in the viewer's local time (no seconds). */
export function formatUnixDateTime(sec: number | null | undefined): string {
  if (!isNum(sec) || sec <= 0) return DASH
  const d = new Date(sec * 1000)
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}`
  )
}

/** Rate-limit window length from minutes. */
export function formatWindowMins(mins: number | null | undefined): string {
  if (!isNum(mins) || mins <= 0) return DASH
  if (mins % (24 * 60) === 0) {
    const days = mins / (24 * 60)
    return days === 1 ? '1 天' : `${days} 天`
  }
  if (mins % 60 === 0) {
    const hours = mins / 60
    return hours === 1 ? '1 小时' : `${hours} 小时`
  }
  return `${mins} 分钟`
}

export function tokenSummary(
  input: number | null | undefined,
  output: number | null | undefined,
  cached?: number | null | undefined,
): string {
  const hasIn = isNum(input)
  const hasOut = isNum(output)
  const hasCached = isNum(cached)
  if (!hasIn && !hasOut && !hasCached) return ''
  const parts = [`入 ${hasIn ? input : 0}`]
  if (hasCached) parts.push(`缓存入 ${cached}`)
  parts.push(`出 ${hasOut ? output : 0}`)
  return parts.join(' / ')
}

const USAGE_LABELS: Record<string, string> = {
  input_tokens: '入',
  cached_input_tokens: '缓存入',
  cache_write_input_tokens: '写入缓存',
  output_tokens: '出',
  reasoning_output_tokens: '推理出',
  total_tokens: '合计',
}

const USAGE_ORDER = [
  'input_tokens',
  'cached_input_tokens',
  'cache_write_input_tokens',
  'output_tokens',
  'reasoning_output_tokens',
  'total_tokens',
] as const

/** Render every numeric field on a Codex usage object. */
export function formatUsage(u: Record<string, unknown> | null | undefined): string {
  if (!u) return ''
  const seen = new Set<string>()
  const parts: string[] = []
  for (const key of USAGE_ORDER) {
    const n = u[key]
    if (!isNum(n)) continue
    parts.push(`${USAGE_LABELS[key]} ${n}`)
    seen.add(key)
  }
  for (const [key, n] of Object.entries(u)) {
    if (seen.has(key) || !isNum(n)) continue
    parts.push(`${key} ${n}`)
  }
  return parts.join(' / ')
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
  plus: 'Plus',
  pro: 'Pro',
  free: '免费',
  go: 'Go',
  prolite: 'Pro Lite',
  team: 'Team',
  business: 'Business',
  enterprise: 'Enterprise',
  edu: 'Edu',
  chatgpt: 'ChatGPT',
  apiKey: 'API Key',
  amazonBedrock: 'Bedrock',
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
