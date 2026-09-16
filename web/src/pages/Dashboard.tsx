import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getDashboard, getHealth } from '../api'
import type { Conversation, TaskRun } from '../api'
import { useLoad } from '../useLoad'
import { Empty, ErrorBox, Spinner, StatusBadge } from '../components/ui'
import {
  formatDuration,
  formatRelative,
  formatTs,
  labelOf,
  text,
  tokenSummary,
  truncate,
} from '../format'

const REFRESH_MS = 10_000

export default function Dashboard() {
  const health = useLoad(getHealth)
  const overview = useLoad(getDashboard)
  const [lastLoaded, setLastLoaded] = useState<string | null>(null)

  const reload = overview.reload
  const reloadHealth = health.reload

  useEffect(() => {
    const timer = setInterval(() => {
      reloadHealth()
      reload()
    }, REFRESH_MS)
    return () => clearInterval(timer)
  }, [reload, reloadHealth])

  useEffect(() => {
    if (overview.data) setLastLoaded(new Date().toISOString())
  }, [overview.data])

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">总览</h1>
          <div className="page-desc">
            Codex 运行状态，每 {REFRESH_MS / 1000} 秒刷新
            {lastLoaded ? ` · 更新于 ${formatTs(lastLoaded)}` : ''}
          </div>
        </div>
        <button
          type="button"
          className="btn"
          onClick={() => {
            reloadHealth()
            reload()
          }}
          disabled={overview.loading || health.loading}
        >
          立即刷新
        </button>
      </div>

      <ErrorBox error={health.error} />
      <ErrorBox error={overview.error} />

      <div className="card">
        <div className="card-head">
          <div className="card-title">后端状态</div>
        </div>
        <div className="card-body">
          {health.loading && !health.data ? (
            <Spinner label="正在检查后端…" />
          ) : health.data ? (
            <div className="health">
              <div className="health-item">
                <div className="health-label">Codex</div>
                <div className="health-value">
                  <span className={health.data.codex_available ? 'dot ok' : 'dot bad'} />
                  {health.data.codex_available ? '可用' : '不可用'}
                </div>
              </div>
              <div className="health-item">
                <div className="health-label">版本</div>
                <div className="health-value">{text(health.data.codex_version)}</div>
              </div>
              <div className="health-item">
                <div className="health-label">可执行文件</div>
                <div className="health-value">{text(health.data.codex_bin)}</div>
              </div>
              <div className="health-item">
                <div className="health-label">数据库</div>
                <div className="health-value">{text(health.data.db_path)}</div>
              </div>
              <div className="health-item">
                <div className="health-label">服务</div>
                <div className="health-value">{text(health.data.version)}</div>
              </div>
            </div>
          ) : (
            <Empty>无法获取健康信息。</Empty>
          )}
        </div>
      </div>

      <div className="grid grid-5">
        <Tile label="档案" value={overview.data?.counts.profiles} to="/profiles" />
        <Tile label="对话" value={overview.data?.counts.conversations} to="/chat" />
        <Tile label="任务" value={overview.data?.counts.tasks} to="/tasks" />
        <Tile
          label="已启用任务"
          value={overview.data?.counts.enabled_tasks}
          sub={
            overview.data && overview.data.counts.tasks > 0
              ? `${overview.data.counts.enabled_tasks} / ${overview.data.counts.tasks}`
              : undefined
          }
          to="/tasks"
        />
        <Tile label="今日运行" value={overview.data?.counts.runs_today} to="/history" />
      </div>

      <div className="card">
        <div className="card-head">
          <div className="card-title">最近任务运行</div>
          <Link className="small" to="/history">
            查看全部
          </Link>
        </div>
        <RecentRuns runs={overview.data?.recent_runs} loading={overview.loading} />
      </div>

      <div className="card">
        <div className="card-head">
          <div className="card-title">最近对话</div>
          <Link className="small" to="/chat">
            打开对话
          </Link>
        </div>
        <RecentConversations
          items={overview.data?.recent_conversations}
          loading={overview.loading}
        />
      </div>
    </div>
  )
}

function Tile({
  label,
  value,
  sub,
  to,
}: {
  label: string
  value: number | undefined
  sub?: string
  to: string
}) {
  return (
    <Link to={to} className="tile" style={{ color: 'inherit' }}>
      <div className="tile-label">{label}</div>
      <div className="tile-value">{value ?? '—'}</div>
      {sub ? <div className="tile-sub">{sub}</div> : null}
    </Link>
  )
}

function RecentRuns({ runs, loading }: { runs?: TaskRun[]; loading: boolean }) {
  if (loading && !runs) return <Spinner />
  if (!runs || runs.length === 0) return <Empty>还没有任务运行记录。</Empty>
  return (
    <div style={{ overflowX: 'auto' }}>
      <table className="table">
        <thead>
          <tr>
            <th>任务</th>
            <th>状态</th>
            <th>触发</th>
            <th>开始</th>
            <th>耗时</th>
            <th>Token</th>
          </tr>
        </thead>
        <tbody>
          {runs.map((run) => (
            <tr key={run.id}>
              <td>
                {text(run.task_name)}
                {run.error ? (
                  <div className="small faint clamp" title={run.error}>
                    {truncate(run.error, 90)}
                  </div>
                ) : null}
              </td>
              <td>
                <StatusBadge status={run.status} />
              </td>
              <td className="dim small">{labelOf(run.trigger)}</td>
              <td className="num small" title={formatTs(run.started_at)}>
                {formatRelative(run.started_at)}
              </td>
              <td className="num small">{formatDuration(run.duration_ms)}</td>
              <td className="num small dim">
                {tokenSummary(run.input_tokens, run.output_tokens) || '—'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function RecentConversations({
  items,
  loading,
}: {
  items?: Conversation[]
  loading: boolean
}) {
  if (loading && !items) return <Spinner />
  if (!items || items.length === 0) return <Empty>还没有对话。</Empty>
  return (
    <div style={{ overflowX: 'auto' }}>
      <table className="table">
        <thead>
          <tr>
            <th>标题</th>
            <th>档案</th>
            <th>消息</th>
            <th>状态</th>
            <th>更新</th>
          </tr>
        </thead>
        <tbody>
          {items.map((conv) => (
            <tr key={conv.id}>
              <td>{truncate(conv.title, 60)}</td>
              <td className="dim">{text(conv.profile_name)}</td>
              <td className="num">{conv.message_count ?? 0}</td>
              <td>
                <StatusBadge status={conv.status} />
              </td>
              <td className="num small" title={formatTs(conv.updated_at)}>
                {formatRelative(conv.updated_at)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
