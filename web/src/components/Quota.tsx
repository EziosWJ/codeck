import type { AccountData, RateLimitResetCredit, RateLimitWindow } from '../api'
import {
  formatCompact,
  formatUnixDate,
  formatUnixDateTime,
  formatUnixRelative,
  formatWindowMins,
  labelOf,
  text,
} from '../format'
import { Empty, ErrorBox, Spinner } from './ui'

const SPARK_DAYS = 42

export function QuotaPanel({
  data,
  loading,
  error,
}: {
  data: AccountData | null
  loading: boolean
  error: string | null
}) {
  if (loading && !data) return <Spinner label="正在读取账号额度…" />
  if (error && !data) return <ErrorBox error={error} />
  if (!data) return <Empty>无法读取账号额度。</Empty>
  if (!data.account && !data.rate_limits && !data.usage) {
    return <ErrorBox error={data.error || 'Codex App Server 未启动，账号额度无法读取。'} />
  }

  const account = data.account?.account ?? null
  const limits = data.rate_limits?.rateLimits ?? null
  const allowed = data.rate_limits?.ordinaryUsageAllowed
  const usage = data.usage
  const resetCredits = data.rate_limits?.rateLimitResetCredits
  const who = accountName(account)
  const plan = account?.planType || limits?.planType

  return (
    <div className="quota">
      {data.error ? <ErrorBox error={data.error} /> : null}

      <div className="quota-id">
        <div className="quota-who">
          <div className="quota-email">{who}</div>
          <div className="quota-meta">
            {plan ? <span className="badge accent">{labelOf(plan)}</span> : null}
            {account?.type && account.type !== 'chatgpt' ? (
              <span className="badge">{labelOf(account.type)}</span>
            ) : null}
          </div>
        </div>
        <QuotaStatus allowed={allowed} />
      </div>

      {limits?.primary || limits?.secondary ? (
        <div className="quota-windows">
          {limits.primary ? <WindowMeter window={limits.primary} fallback="短窗口" /> : null}
          {limits.secondary ? <WindowMeter window={limits.secondary} fallback="长窗口" /> : null}
        </div>
      ) : null}

      <CreditLine
        balance={limits?.credits?.balance}
        unlimited={limits?.credits?.unlimited}
        hasCredits={limits?.credits?.hasCredits}
        resetCount={resetCredits?.availableCount}
        resetCredits={resetCredits?.credits}
      />

      {usage ? <BurnStrip usage={usage} /> : null}
    </div>
  )
}

function accountName(account: { type?: string; email?: string | null } | null): string {
  if (!account) return '未登录'
  if (account.email) return account.email
  if (account.type === 'apiKey') return 'API Key 账号'
  if (account.type === 'amazonBedrock') return 'Bedrock 账号'
  return labelOf(account.type)
}

function QuotaStatus({ allowed }: { allowed: boolean | null | undefined }) {
  if (allowed === true) {
    return (
      <div className="quota-status ok">
        <span className="dot ok" />
        额度可用
      </div>
    )
  }
  if (allowed === false) {
    return (
      <div className="quota-status bad">
        <span className="dot bad" />
        额度已用尽
      </div>
    )
  }
  return (
    <div className="quota-status">
      <span className="dot" />
      额度未知
    </div>
  )
}

function WindowMeter({ window, fallback }: { window: RateLimitWindow; fallback: string }) {
  const remaining = clampPercent(100 - window.usedPercent)
  const tone = remaining <= 10 ? 'hot' : remaining <= 30 ? 'warn' : 'ok'
  const label = formatWindowMins(window.windowDurationMins)
  const name = label === '—' ? fallback : label
  const relative = formatUnixRelative(window.resetsAt)
  const eta = formatUnixDateTime(window.resetsAt)
  const resetText = relative === '—' ? '—' : eta === '—' ? relative : `${relative}（预计 ${eta}）`
  return (
    <div className="quota-window">
      <div className="quota-window-top">
        <span className="quota-window-label">{name}</span>
        <span className={`quota-window-pct ${tone}`}>{remaining}%</span>
      </div>
      <div
        className="quota-track"
        role="meter"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={remaining}
        aria-label={`${name}剩余 ${remaining}%`}
      >
        <div className={`quota-fill ${tone}`} style={{ width: `${remaining}%` }} />
      </div>
      <div className="quota-window-foot">重置于 {resetText}</div>
    </div>
  )
}

function CreditLine({
  balance,
  unlimited,
  hasCredits,
  resetCount,
  resetCredits,
}: {
  balance?: string | null
  unlimited?: boolean
  hasCredits?: boolean
  resetCount?: number
  resetCredits?: RateLimitResetCredit[] | null
}) {
  const balanceText = unlimited
    ? '额度不限'
    : hasCredits && balance
      ? `余额 ${trimBalance(balance)} credits`
      : null
  const rows = (resetCredits ?? []).filter((c) => !c.status || c.status === 'available')
  const n = typeof resetCount === 'number' ? resetCount : rows.length
  if (!balanceText && n <= 0 && rows.length === 0) return null
  return (
    <div className="quota-credits">
      {balanceText ? <div>{balanceText}</div> : null}
      <ResetCredits count={n} credits={rows} />
    </div>
  )
}

function ResetCredits({
  count,
  credits,
}: {
  count: number
  credits: RateLimitResetCredit[]
}) {
  if (count <= 0 && credits.length === 0) return null
  if (credits.length === 0) return <div>{count} 张重置券</div>
  return (
    <details className="fold quota-resets">
      <summary>
        {count} 张重置券
      </summary>
      <ul className="quota-reset-list">
        {credits.map((c) => (
          <li key={c.id}>
            <span>{c.title || '重置券'}</span>
            <span className="quota-reset-exp">
              {c.expiresAt
                ? `截止 ${formatUnixDate(c.expiresAt)}（${formatUnixRelative(c.expiresAt)}）`
                : '无截止日期'}
            </span>
          </li>
        ))}
      </ul>
    </details>
  )
}

function BurnStrip({ usage }: { usage: NonNullable<AccountData['usage']> }) {
  const days = lastDays(usage.dailyUsageBuckets ?? [], SPARK_DAYS)
  const max = days.reduce((m, d) => (d.tokens > m ? d.tokens : m), 0)
  const summary = usage.summary
  return (
    <div className="quota-burn">
      <div className="quota-burn-chart">
        <div className="quota-burn-label">近 {SPARK_DAYS} 天用量</div>
        <div className="quota-spark" role="img" aria-label={`近 ${SPARK_DAYS} 天 token 用量`}>
          {days.map((d) => {
            const h = max > 0 ? Math.max(d.tokens > 0 ? 8 : 2, Math.round((d.tokens / max) * 56)) : 2
            return (
              <div
                key={d.date}
                className={d.tokens > 0 ? 'quota-bar on' : 'quota-bar'}
                style={{ height: h }}
                title={`${d.date}  ${d.tokens > 0 ? formatCompact(d.tokens) + ' tokens' : '无用量'}`}
              />
            )
          })}
        </div>
      </div>
      <dl className="quota-stats">
        <div>
          <dt>累计</dt>
          <dd>{formatCompact(summary.lifetimeTokens)}</dd>
        </div>
        <div>
          <dt>单日峰值</dt>
          <dd>{formatCompact(summary.peakDailyTokens)}</dd>
        </div>
        <div>
          <dt>连续使用</dt>
          <dd>{summary.currentStreakDays != null ? `${summary.currentStreakDays} 天` : text(null)}</dd>
        </div>
      </dl>
    </div>
  )
}

function clampPercent(n: number): number {
  if (!Number.isFinite(n)) return 0
  return Math.max(0, Math.min(100, Math.round(n)))
}

function trimBalance(raw: string): string {
  const n = Number(raw)
  if (!Number.isFinite(n)) return raw
  return n.toFixed(n >= 100 ? 0 : 1).replace(/\.0$/, '')
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function lastDays(buckets: { startDate: string; tokens: number }[], n: number) {
  const map = new Map(buckets.map((b) => [b.startDate, b.tokens]))
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const out: { date: string; tokens: number }[] = []
  for (let i = n - 1; i >= 0; i--) {
    const d = new Date(today)
    d.setDate(today.getDate() - i)
    const key = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
    out.push({ date: key, tokens: map.get(key) ?? 0 })
  }
  return out
}
