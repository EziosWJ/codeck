import type { AccountData, RateLimitWindow } from '../api'
import { formatWindowMins } from '../format'
import {
  buildSegments,
  buildTicks,
  clampPct,
  countdownText,
  rateLimitWindows,
  remainingTone,
  rowSpanMs,
  shortDateTime,
  type Multiplier,
} from '../timeline'
import { Empty, ErrorBox, Spinner } from './ui'

/**
 * 「重置时间线」——每行一个额度窗口，**每行一根独立标尺**。
 *
 * 行 = `primary`（短窗口，通常 5 小时）与 `secondary`（长窗口，通常 7 天）。
 * 标尺跨度 = 该窗口自身时长 × 倍率，两行因此各有自己的刻度，不互相挤压。
 * 条从「现在」画到该窗口的 `resetsAt`，条内深色段 = 条宽 × usedPercent%。
 * Codeck 没有历史配额采样，所以这根轴只向后看。
 */
export function Timeline({
  data,
  loading,
  error,
  nowMs,
  multiplier,
}: {
  data: AccountData | null
  loading: boolean
  error: string | null
  /** 当前时刻（毫秒）。由页面传入并按轮询节奏更新，组件本身不持有时钟。 */
  nowMs: number
  multiplier: Multiplier
}) {
  if (loading && !data) return <Spinner label="正在读取额度窗口…" />
  if (error && !data) return <ErrorBox error={error} />
  if (!data) return <Empty>无法读取额度窗口。</Empty>
  if (!data.rate_limits) {
    return <ErrorBox error={data.error || 'Codex App Server 未启动，额度窗口无法读取。'} />
  }

  const windows = rateLimitWindows(data)
  if (windows.length === 0) return <Empty>当前账号没有返回额度窗口。</Empty>

  return (
    <div className="timeline">
      <div className="timeline-head">
        <div className="timeline-axis-title">额度窗口</div>
        <div className="timeline-axis-note">
          每行独立标尺：跨度 = 该窗口时长 × {multiplier}，左端为当前时刻
        </div>
      </div>

      <div className="timeline-rows">
        {windows.map(({ key, window }) => (
          <TimelineRow key={key} window={window} nowMs={nowMs} multiplier={multiplier} />
        ))}
      </div>

      <div className="timeline-foot">
        条 = 从现在到该窗口重置时刻；深色段 = 已用比例，剩余部分为可用额度。
        {multiplier > 1
          ? ` 标尺拉长到 ${multiplier} 个窗口，超出当前窗口的部分是按窗口时长推算的后续重置周期。`
          : ' ×1 只看当前窗口。'}
        只画未来：Codeck 不保留历史配额采样。
      </div>
    </div>
  )
}

function TimelineRow({
  window,
  nowMs,
  multiplier,
}: {
  window: RateLimitWindow
  nowMs: number
  multiplier: Multiplier
}) {
  const used = clampPct(window.usedPercent)
  const remaining = 100 - used
  const tone = remainingTone(used)
  const label = windowLabel(window)
  const spanMs = rowSpanMs(window, multiplier)
  const ticks = buildTicks(spanMs, nowMs)
  const segments = buildSegments(window, spanMs, nowMs, multiplier > 1)
  const countdown = window.resetsAt ? countdownText(window.resetsAt * 1000 - nowMs) : '—'
  const summary =
    segments === null
      ? `${label}：重置时间未知，已用 ${used}%`
      : segments.length === 0
        ? `${label}：缓存的重置时刻已过，等待刷新`
        : `${label}：已用 ${used}%，剩余 ${remaining}%，${countdown}后重置于 ${shortDateTime(window.resetsAt)}`

  return (
    <div className="timeline-row">
      <div className="timeline-row-label">
        <div className="timeline-row-name">{label}</div>
        <div className="timeline-row-meta">
          <span className={`timeline-row-pct ${tone}`}>{remaining}%</span>
          <span className="faint">可用</span>
        </div>
        <div className="timeline-row-sub">
          {segments === null
            ? '重置时间未知'
            : segments.length === 0
              ? '等待刷新'
              : `${countdown}后重置 · ${shortDateTime(window.resetsAt)}`}
        </div>
      </div>

      <div className="timeline-row-scale">
        <div className="timeline-axis" aria-hidden="true">
          {ticks.map((tick, i) => (
            <div
              key={`${tick.layer}-${i}-${tick.pos}`}
              className={tick.layer === 'outer' ? 'timeline-tick' : 'timeline-tick minor'}
              style={{ left: `${tick.pos * 100}%` }}
            >
              {tick.label ? <span className="timeline-tick-label">{tick.label}</span> : null}
            </div>
          ))}
        </div>

        <div className="timeline-track" role="img" aria-label={summary}>
          {segments === null || segments.length === 0 ? (
            <div
              className="timeline-bar unknown"
              style={{ left: 0, width: '100%' }}
              title={
                segments === null
                  ? `${label}：重置时间未知 · 已用 ${used}%`
                  : `${label}：缓存的重置时刻已过，等待下一次刷新`
              }
            />
          ) : (
            segments.map((seg) => {
              if (seg.usedPct === null) {
                return (
                  <div
                    key={`c${seg.cycle}`}
                    className="timeline-seg forecast"
                    style={{ left: `${seg.left * 100}%`, width: `${seg.width * 100}%` }}
                    title={`预计第 ${seg.cycle} 个周期 · ${shortDateTime(seg.resetAt)} 重置`}
                  />
                )
              }
              return (
                <div
                  key={`c${seg.cycle}`}
                  className={`timeline-seg ${tone}`}
                  style={{ left: `${seg.left * 100}%`, width: `${seg.width * 100}%` }}
                  title={`${label} · ${seg.usedPct}% 已用 · ${countdownText(seg.resetAt * 1000 - nowMs)}后重置 · ${shortDateTime(seg.resetAt)}`}
                >
                  <div className="timeline-used" style={{ width: `${seg.usedPct}%` }} />
                  {seg.clipped ? <div className="timeline-clip">→</div> : null}
                </div>
              )
            })
          )}
          <div className="timeline-now" />
        </div>
      </div>
    </div>
  )
}

function windowLabel(window: RateLimitWindow): string {
  const label = formatWindowMins(window.windowDurationMins)
  return label === '—' ? '额度窗口' : label
}
