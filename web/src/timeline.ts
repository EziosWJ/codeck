/**
 * 「重置时间线」的纯计算部分：每行的标尺刻度、条形几何、倒计时文案。
 *
 * 这里刻意不依赖 React，也没有任何运行时 import（只有 `import type`，会被
 * 类型擦除），因此可以直接用 node 单独跑一遍核对数字，而不用起前端。
 *
 * 约定：
 * - **每行一根独立标尺**，跨度 = 该窗口自身时长 × 倍率。5 小时行和 7 天行
 *   因此各有自己的刻度，不会互相挤压。
 * - 横轴左端 = 当前时刻。Codeck 没有历史配额采样，所以只能向后画。
 * - 每根条从「现在」画到该窗口的 `resetsAt`；条内深色段 = 条宽 × usedPercent%。
 * - `resetsAt` 是 Unix 秒（与 web/src/format.ts 的既有约定一致）。
 */
import type { AccountData, RateLimitWindow } from './api'

/** 轴跨度相对单窗口的倍率。>1 时轴会跨进后续窗口，于是出现「预计周期」。 */
export type Multiplier = 1 | 2 | 4

export const MULTIPLIERS: Multiplier[] = [1, 2, 4]

/** 30 天视野下最多推算这么多个后续周期，否则 5 小时窗口要画上百段。 */
export const MAX_FORECAST = 12

/** 窗口时长拿不到时的兜底：7 天，照截图的长窗口。 */
const FALLBACK_DUR_MS = 10_080 * 60_000

const MIN = 60_000
const DAY = 24 * 60 * MIN

/** `/api/account` 里的两个额度窗口，顺序固定：primary（短窗口）在前。 */
export interface TimelineWindow {
  key: 'primary' | 'secondary'
  window: RateLimitWindow
}

export interface Tick {
  /** 0–1，相对该行标尺左端的位置。 */
  pos: number
  label: string
  /** 外层（大刻度）还是内层（小刻度）——决定标签大小与颜色。 */
  layer: 'outer' | 'inner'
}

export interface Segment {
  /** 0–1，相对该行标尺左端。 */
  left: number
  /** 0–1，相对该行标尺总跨度。 */
  width: number
  /** 当前周期为已用百分比；预计周期为 null。 */
  usedPct: number | null
  /** 0 = 当前周期，≥1 = 预计的第 N 个后续周期。 */
  cycle: number
  /** 条尾被标尺右端裁掉（真实重置时刻在轴外）。 */
  clipped: boolean
  /** 该周期的重置时刻（Unix 秒），预计周期用来做提示。 */
  resetAt: number
}

function isNum(n: unknown): n is number {
  return typeof n === 'number' && Number.isFinite(n)
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

/** 取 `/api/account` 里的两个窗口；数据缺失时返回空数组。 */
export function rateLimitWindows(data: AccountData | null): TimelineWindow[] {
  const limits = data?.rate_limits?.rateLimits
  if (!limits) return []
  const out: TimelineWindow[] = []
  if (limits.primary) out.push({ key: 'primary', window: limits.primary })
  if (limits.secondary) out.push({ key: 'secondary', window: limits.secondary })
  return out
}

/**
 * 该行标尺的跨度 = 窗口自身时长 × 倍率。
 * 时长缺失时退化为 7 天（照截图的长窗口）。
 */
export function rowSpanMs(window: RateLimitWindow, mult: Multiplier): number {
  const dur = windowDurationMs(window)
  return (dur ?? FALLBACK_DUR_MS) * mult
}

/** 窗口时长（毫秒）；`windowDurationMins` 缺失或非法时为 null。 */
export function windowDurationMs(window: RateLimitWindow): number | null {
  const mins = window.windowDurationMins
  if (!isNum(mins) || mins <= 0) return null
  return mins * MIN
}

/**
 * 刻度步长阶梯。子日步长（15 分–12 小时）都能整除 24 小时，因此锚在本地
 * 午夜即可自然对齐；日级步长用日历运算，跨夏令时也不漂。
 */
const LADDER = [
  15 * MIN,
  30 * MIN,
  MIN * 60,
  120 * MIN,
  180 * MIN,
  360 * MIN,
  720 * MIN,
  DAY,
  2 * DAY,
  7 * DAY,
  14 * DAY,
  28 * DAY,
]

function chooseStep(spanMs: number): number {
  for (const step of LADDER) {
    if (spanMs / step <= 10) return step
  }
  return LADDER[LADDER.length - 1]
}

/** 大刻度 = 小刻度的整数倍，且至少给 5 个以上的小刻度留出对比。 */
function chooseOuter(stepMs: number, spanMs: number): number {
  let k = 1
  while (spanMs / (stepMs * k) > 8 && k < 60) k++
  return stepMs * k
}

/**
 * 单行标尺的刻度：从当前时刻起、按本地时间边界对齐。
 * 步长随该行跨度自适应（5 小时行 → 半小时一格；7 天行 → 一天一格）。
 */
export function buildTicks(spanMs: number, startMs: number): Tick[] {
  const ticks: Tick[] = []
  if (!(spanMs > 0)) return ticks
  const step = chooseStep(spanMs)
  const outer = chooseOuter(step, spanMs)
  const endMs = startMs + spanMs
  const dayScale = step >= DAY

  const start = new Date(startMs)
  start.setHours(0, 0, 0, 0)
  let cursor = start.getTime()
  // 逼近窗口起点，避免从很久以前开始逐格步进。
  const skip = Math.max(0, Math.floor((startMs - cursor) / step) - 1)
  cursor += skip * step

  for (let guard = 0; cursor <= endMs && guard < 400; guard++) {
    const at = new Date(cursor)
    if (cursor >= startMs) {
      const pos = (cursor - startMs) / spanMs
      const isOuter = (cursor - start.getTime()) % outer === 0
      ticks.push({ pos, label: tickLabel(at, step, isOuter), layer: isOuter ? 'outer' : 'inner' })
    }
    cursor = dayScale ? addDays(cursor, Math.round(step / DAY)) : cursor + step
  }
  return ticks
}

function tickLabel(at: Date, stepMs: number, isOuter: boolean): string {
  if (stepMs >= DAY) {
    return isOuter ? `${pad(at.getMonth() + 1)}/${pad(at.getDate())}` : String(at.getDate())
  }
  if (at.getHours() === 0 && at.getMinutes() === 0) return `${pad(at.getMonth() + 1)}/${pad(at.getDate())}`
  return isOuter ? `${pad(at.getHours())}:00` : `${pad(at.getHours())}:${pad(at.getMinutes())}`
}

/**
 * 该行要画的条（含倍率 >1 时的预计周期）。
 *
 * - 返回 `null`：`resetsAt` 拿不到 → 该行画虚线轨道，行保留（避免布局抖动）。
 * - 返回 `[]`：`resetsAt` 已过（缓存里的旧数据）→ 等待下一次刷新。
 *
 * @param allowForecast 倍率 >1 时才画后续周期：×1 的语义是「只看这一个窗口」。
 */
export function buildSegments(
  window: RateLimitWindow,
  spanMs: number,
  startMs: number,
  allowForecast: boolean,
  maxForecast = MAX_FORECAST,
): Segment[] | null {
  if (!isNum(window.resetsAt) || window.resetsAt <= 0) return null
  const endMs = startMs + spanMs
  const resetMs = window.resetsAt * 1000
  if (resetMs <= startMs) return []

  const usedPct = clampPct(window.usedPercent)
  const segments: Segment[] = [
    {
      left: 0,
      width: (Math.min(resetMs, endMs) - startMs) / spanMs,
      usedPct,
      cycle: 0,
      clipped: resetMs > endMs,
      resetAt: window.resetsAt,
    },
  ]

  const durMs = windowDurationMs(window)
  if (!allowForecast || durMs === null || durMs <= 0) return segments

  let cursor = resetMs
  for (let n = 1; n <= maxForecast && cursor < endMs; n++) {
    const cycleEnd = Math.min(cursor + durMs, endMs)
    segments.push({
      left: (cursor - startMs) / spanMs,
      width: (cycleEnd - cursor) / spanMs,
      usedPct: null,
      cycle: n,
      clipped: cursor + durMs > endMs,
      resetAt: Math.round((cursor + durMs) / 1000),
    })
    cursor = cycleEnd
  }
  return segments
}

/** 距重置还剩多久，精确到分（条宽撑不住信息时，靠这段文字）。 */
export function countdownText(ms: number): string {
  if (!isNum(ms)) return '—'
  if (ms <= 0) return '已到重置时刻'
  const total = Math.round(ms / 1000)
  const days = Math.floor(total / 86_400)
  const hours = Math.floor((total % 86_400) / 3600)
  const mins = Math.floor((total % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${mins} 分`
  if (mins > 0) return `${mins} 分钟`
  return `${total} 秒`
}

/** Unix 秒 → "09/22 18:00"（本地时区，照截图的紧凑写法）。 */
export function shortDateTime(sec: number | null | undefined): string {
  if (!isNum(sec) || sec <= 0) return '—'
  const d = new Date(sec * 1000)
  return `${pad(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function clampPct(n: number | null | undefined): number {
  if (!isNum(n)) return 0
  return Math.max(0, Math.min(100, Math.round(n)))
}

/** 与 Quota.tsx 一致的健康语义：剩余 ≤10% 红、≤30% 黄、其余蓝。 */
export function remainingTone(usedPct: number): 'hot' | 'warn' | 'ok' {
  const remaining = 100 - clampPct(usedPct)
  return remaining <= 10 ? 'hot' : remaining <= 30 ? 'warn' : 'ok'
}

function addDays(ms: number, n: number): number {
  const d = new Date(ms)
  d.setDate(d.getDate() + n)
  return d.getTime()
}
