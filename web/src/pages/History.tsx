import { useState } from 'react'
import { getHistory } from '../api'
import type { HistoryData, Message, TaskRun } from '../api'
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

const LIMITS = [50, 100, 200, 500]

export default function History() {
  const [limit, setLimit] = useState(100)
  const { data, error, loading, reload } = useLoad<HistoryData>(
    () => getHistory(limit),
    [limit],
  )

  const messages = data?.messages ?? []
  const runs = data?.runs ?? []

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">历史</h1>
          <div className="page-desc">运行器记下的全部内容：对话回合与定时执行。</div>
        </div>
        <div className="toolbar">
          <label className="small dim">条数</label>
          <select
            className="select"
            style={{ width: 110 }}
            value={limit}
            onChange={(e) => setLimit(Number(e.target.value))}
          >
            {LIMITS.map((n) => (
              <option key={n} value={n}>
                最近 {n}
              </option>
            ))}
          </select>
          <button type="button" className="btn" onClick={reload} disabled={loading}>
            刷新
          </button>
        </div>
      </div>

      <ErrorBox error={error} />

      <div className="card">
        <div className="card-head">
          <div className="card-title">对话消息</div>
          <span className="small faint">显示 {messages.length} 条</span>
        </div>
        {loading && !data ? (
          <Spinner label="加载消息…" />
        ) : messages.length === 0 ? (
          <Empty>没有对话消息。</Empty>
        ) : (
          <MessageTable messages={messages} />
        )}
      </div>

      <div className="card">
        <div className="card-head">
          <div className="card-title">任务运行</div>
          <span className="small faint">显示 {runs.length} 条</span>
        </div>
        {loading && !data ? (
          <Spinner label="加载运行记录…" />
        ) : runs.length === 0 ? (
          <Empty>没有任务运行记录。</Empty>
        ) : (
          <RunTable runs={runs} />
        )}
      </div>
    </div>
  )
}

function MessageTable({ messages }: { messages: Message[] }) {
  return (
    <div style={{ overflowX: 'auto' }}>
      <table className="table">
        <thead>
          <tr>
            <th>时间</th>
            <th>对话</th>
            <th>角色</th>
            <th>状态</th>
            <th>内容</th>
            <th>Token</th>
            <th>耗时</th>
          </tr>
        </thead>
        <tbody>
          {messages.map((m) => (
            <tr key={m.id}>
              <td className="num small nowrap" title={formatTs(m.created_at)}>
                {formatRelative(m.created_at)}
              </td>
              <td className="small dim num">#{m.conversation_id}</td>
              <td>
                <span className={m.role === 'user' ? 'badge' : 'badge accent'}>
                  {labelOf(m.role)}
                </span>
              </td>
              <td>
                <StatusBadge status={m.status} />
              </td>
              <td>
                <span className="clamp" title={m.content ?? ''}>
                  {truncate(m.content, 100)}
                </span>
                {m.error ? (
                  <div className="small" style={{ color: 'var(--err)' }}>
                    {truncate(m.error, 90)}
                  </div>
                ) : null}
              </td>
              <td className="num small dim">
                {tokenSummary(m.input_tokens, m.output_tokens) || '—'}
              </td>
              <td className="num small">{formatDuration(m.duration_ms)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function RunTable({ runs }: { runs: TaskRun[] }) {
  return (
    <div style={{ overflowX: 'auto' }}>
      <table className="table">
        <thead>
          <tr>
            <th>时间</th>
            <th>任务</th>
            <th>状态</th>
            <th>触发</th>
            <th>耗时</th>
            <th>Token</th>
            <th>结果</th>
          </tr>
        </thead>
        <tbody>
          {runs.map((r) => (
            <tr key={r.id}>
              <td className="num small nowrap" title={formatTs(r.started_at)}>
                {formatRelative(r.started_at)}
              </td>
              <td>
                {text(r.task_name)}
                <div className="small faint num">任务 #{r.task_id}</div>
              </td>
              <td>
                <StatusBadge status={r.status} />
              </td>
              <td className="small dim">{labelOf(r.trigger)}</td>
              <td className="num small">{formatDuration(r.duration_ms)}</td>
              <td className="num small dim">
                {tokenSummary(r.input_tokens, r.output_tokens) || '—'}
              </td>
              <td>
                {r.error ? (
                  <span className="small" style={{ color: 'var(--err)' }} title={r.error}>
                    {truncate(r.error, 80)}
                  </span>
                ) : (
                  <span className="clamp small dim" title={r.output ?? ''}>
                    {truncate(r.output, 80)}
                  </span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
