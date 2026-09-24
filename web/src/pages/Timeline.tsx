import { useEffect, useState } from 'react'
import { getAccount } from '../api'
import { Timeline } from '../components/Timeline'
import { QuotaPanel } from '../components/Quota'
import { ErrorBox } from '../components/ui'
import { formatTs } from '../format'
import { MULTIPLIERS, type Multiplier } from '../timeline'
import { useLoad } from '../useLoad'

const REFRESH_MS = 10_000

export default function TimelinePage() {
  const account = useLoad(getAccount)
  const reloadAccount = account.reload
  const [nowMs, setNowMs] = useState(() => Date.now())
  const [multiplier, setMultiplier] = useState<Multiplier>(1)

  useEffect(() => {
    const timer = setInterval(() => {
      setNowMs(Date.now())
      reloadAccount()
    }, REFRESH_MS)
    return () => clearInterval(timer)
  }, [reloadAccount])

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">重置时间线</h1>
          <div className="page-desc">
            每个额度窗口一根独立标尺，从现在画到重置时刻 · 每 {REFRESH_MS / 1000} 秒刷新 · 基准{' '}
            {formatTs(new Date(nowMs).toISOString())}
          </div>
        </div>
        <div className="timeline-multipliers" role="group" aria-label="标尺跨度倍率">
          {MULTIPLIERS.map((m) => (
            <button
              key={m}
              type="button"
              className={multiplier === m ? 'btn sm active' : 'btn sm'}
              onClick={() => setMultiplier(m)}
              title={m === 1 ? '只看当前窗口' : `标尺拉长到 ${m} 个窗口，显示后续重置周期`}
            >
              ×{m}
            </button>
          ))}
        </div>
      </div>

      <ErrorBox error={account.error} />

      <div className="card">
        <div className="card-head">
          <div className="card-title">账号额度</div>
        </div>
        <div className="card-body">
          <QuotaPanel data={account.data} loading={account.loading} error={account.error} />
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div className="card-title">重置时间线</div>
          <button
            type="button"
            className="btn sm"
            onClick={() => {
              setNowMs(Date.now())
              reloadAccount()
            }}
            disabled={account.loading}
          >
            立即刷新
          </button>
        </div>
        <div className="card-body flush">
          <Timeline
            data={account.data}
            loading={account.loading}
            error={account.error}
            nowMs={nowMs}
            multiplier={multiplier}
          />
        </div>
      </div>
    </div>
  )
}
