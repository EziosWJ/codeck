import { useState } from 'react'
import {
  createPrice,
  deletePrice,
  emptyPrice,
  listPrices,
  restorePrices,
  updatePrice,
  getUsage,
} from '../api'
import type { ModelPrice, ModelPriceInput, UsageDayRow, UsageReport } from '../api'
import { errText } from '../api'
import { useLoad } from '../useLoad'
import { Empty, ErrorBox, Field, Modal, Spinner } from '../components/ui'
import { formatCompact, formatTs, formatUSD } from '../format'

const SPARK_DAYS = 42

export default function Usage() {
  const report = useLoad(() => getUsage(false))
  const prices = useLoad(listPrices)
  const [editing, setEditing] = useState<ModelPrice | 'new' | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function refreshAll() {
    setBusy(true)
    try {
      report.setData(await getUsage(true))
      prices.reload()
    } catch (e) {
      setNotice(errText(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleRestore() {
    if (!window.confirm('用官网 Standard 种子覆盖同名 pattern，不会删除你自己加的行。继续？')) return
    setBusy(true)
    try {
      await restorePrices()
      setNotice('已恢复默认价格。')
      prices.reload()
      report.setData(await getUsage(true))
    } catch (e) {
      setNotice(errText(e))
    } finally {
      setBusy(false)
    }
  }

  const data = report.data

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">用量</h1>
          <div className="page-desc">
            本机 session 按模型汇总，再换成 Standard API 单价。不是 ChatGPT credits。
            {data?.scanned_at ? ` · 扫描于 ${formatTs(data.scanned_at)}` : ''}
          </div>
        </div>
        <button
          type="button"
          className="btn"
          onClick={() => void refreshAll()}
          disabled={report.loading || busy}
        >
          重新扫描
        </button>
      </div>

      {notice ? <div className="ok-box" style={{ marginBottom: 14 }}>{notice}</div> : null}
      <ErrorBox error={report.error} />

      {report.loading && !data ? (
        <Spinner label="正在扫描本机 session…" />
      ) : data ? (
        <>
          <Summary data={data} />
          <DayChart days={data.daily ?? []} />
          <ModelTable rows={data.by_model ?? []} />
          {data.errors && data.errors.length > 0 ? (
            <details className="fold">
              <summary>扫描警告 {data.errors.length}</summary>
              <ul className="small dim">
                {data.errors.map((e) => (
                  <li key={e}>{e}</li>
                ))}
              </ul>
            </details>
          ) : null}
          <p className="faint small usage-foot">
            单价为 OpenAI Standard（短上下文）；input 超过阈值走长上下文价。推理 token 已含在
            output 中。未计入 Fast、工具附加费。未匹配模型只计 token。
          </p>
        </>
      ) : (
        <Empty>无法读取本地用量。</Empty>
      )}

      <div className="card">
        <div className="card-head">
          <div className="card-title">价格表</div>
          <div className="row-actions">
            <button type="button" className="btn sm" onClick={() => void handleRestore()} disabled={busy}>
              恢复默认价
            </button>
            <button type="button" className="btn primary sm" onClick={() => setEditing('new')}>
              新增
            </button>
          </div>
        </div>
        <div className="card-body">
          <ErrorBox error={prices.error} />
          {prices.loading && !prices.data ? (
            <Spinner />
          ) : !prices.data || prices.data.length === 0 ? (
            <Empty>还没有价格行。</Empty>
          ) : (
            <div style={{ overflowX: 'auto' }}>
              <table className="table">
                <thead>
                  <tr>
                    <th>pattern</th>
                    <th>入</th>
                    <th>缓存入</th>
                    <th>写缓存</th>
                    <th>出</th>
                    <th>优先级</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {prices.data.map((p) => (
                    <tr key={p.id}>
                      <td className="mono">
                        {p.pattern}
                        {p.pattern.includes('*') || p.pattern.includes('?') ? (
                          <span className="badge" style={{ marginLeft: 8 }}>
                            通配
                          </span>
                        ) : null}
                      </td>
                      <td className="num">{p.input_usd_per_mtok}</td>
                      <td className="num">{p.cached_input_usd_per_mtok}</td>
                      <td className="num">{p.cache_write_usd_per_mtok}</td>
                      <td className="num">{p.output_usd_per_mtok}</td>
                      <td className="num">{p.priority}</td>
                      <td>
                        <div className="row-actions">
                          <button type="button" className="btn sm" onClick={() => setEditing(p)}>
                            编辑
                          </button>
                          <button
                            type="button"
                            className="btn sm danger"
                            onClick={() => void handleDelete(p)}
                          >
                            删除
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {editing ? (
        <PriceForm
          price={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            setNotice('价格已保存。')
            prices.reload()
            void getUsage(true).then((data) => report.setData(data))
          }}
        />
      ) : null}
    </div>
  )

  async function handleDelete(p: ModelPrice) {
    if (!window.confirm(`删除价格「${p.pattern}」？`)) return
    try {
      await deletePrice(p.id)
      setNotice(`已删除 ${p.pattern}。`)
      prices.reload()
      report.setData(await getUsage(true))
    } catch (e) {
      setNotice(errText(e))
    }
  }
}

function Summary({ data }: { data: UsageReport }) {
  const s = data.summary
  return (
    <div className="grid grid-5">
      <div className="tile">
        <div className="tile-label">等价成本</div>
        <div className="tile-value">{formatUSD(s.usd)}</div>
      </div>
      <div className="tile">
        <div className="tile-label">合计 tokens</div>
        <div className="tile-value">{formatCompact(s.total_tokens)}</div>
      </div>
      <div className="tile">
        <div className="tile-label">未定价</div>
        <div className="tile-value">{formatCompact(s.unpriced_tokens)}</div>
        <div className="tile-sub">{s.unpriced_turns} 次 turn</div>
      </div>
      <div className="tile">
        <div className="tile-label">文件 / turn</div>
        <div className="tile-value">
          {data.files} / {data.turns}
        </div>
      </div>
      <div className="tile">
        <div className="tile-label">扫描根</div>
        <div className="tile-value small" style={{ fontSize: 13, fontWeight: 500 }}>
          {data.roots.length}
        </div>
        <div className="tile-sub clamp" title={data.roots.join('\n')}>
          {data.roots.map((r) => r.split(/[/\\]/).pop()).join(' · ') || '—'}
        </div>
      </div>
    </div>
  )
}

function DayChart({ days }: { days: UsageDayRow[] }) {
  const map = new Map(days.map((d) => [d.date, d]))
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const series: UsageDayRow[] = []
  for (let i = SPARK_DAYS - 1; i >= 0; i--) {
    const d = new Date(today)
    d.setDate(today.getDate() - i)
    const key = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
    series.push(map.get(key) ?? { date: key, tokens: 0, usd: null, models: [] })
  }
  const max = series.reduce((m, d) => (d.tokens > m ? d.tokens : m), 0)
  return (
    <div className="card">
      <div className="card-head">
        <div className="card-title">近 {SPARK_DAYS} 天</div>
      </div>
      <div className="card-body">
        <div className="quota-spark" role="img" aria-label={`近 ${SPARK_DAYS} 天本地用量`}>
          {series.map((d) => {
            const h = max > 0 ? Math.max(d.tokens > 0 ? 8 : 2, Math.round((d.tokens / max) * 56)) : 2
            const title = `${d.date}  ${d.tokens > 0 ? formatCompact(d.tokens) + ' tokens' : '无用量'}${
              d.usd != null ? `  ${formatUSD(d.usd)}` : ''
            }`
            return (
              <div
                key={d.date}
                className={d.tokens > 0 ? 'quota-bar on' : 'quota-bar'}
                style={{ height: h }}
                title={title}
              />
            )
          })}
        </div>
      </div>
    </div>
  )
}

function ModelTable({ rows }: { rows: UsageReport['by_model'] }) {
  if (rows.length === 0) return <Empty>没有 token 记录。</Empty>
  return (
    <div className="card">
      <div className="card-head">
        <div className="card-title">按模型</div>
      </div>
      <div className="card-body" style={{ overflowX: 'auto' }}>
        <table className="table">
          <thead>
            <tr>
              <th>模型</th>
              <th>turn</th>
              <th>未缓存入</th>
              <th>缓存入</th>
              <th>出</th>
              <th>合计</th>
              <th>等价 USD</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.model}>
                <td>
                  <span className="mono">{r.model}</span>
                  {!r.priced ? (
                    <span className="badge err" style={{ marginLeft: 8 }}>
                      未定价
                    </span>
                  ) : r.wildcard ? (
                    <span className="badge" style={{ marginLeft: 8 }} title={r.matched_by}>
                      按通配价
                    </span>
                  ) : null}
                </td>
                <td className="num">{r.turns}</td>
                <td className="num">{formatCompact(r.uncached_tokens)}</td>
                <td className="num">{formatCompact(r.cached_tokens)}</td>
                <td className="num">{formatCompact(r.output_tokens)}</td>
                <td className="num">{formatCompact(r.total_tokens)}</td>
                <td className="num">{formatUSD(r.usd)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function PriceForm({
  price,
  onClose,
  onSaved,
}: {
  price: ModelPrice | null
  onClose: () => void
  onSaved: () => void
}) {
  const [form, setForm] = useState<ModelPriceInput>(() =>
    price
      ? {
          pattern: price.pattern,
          input_usd_per_mtok: price.input_usd_per_mtok,
          cached_input_usd_per_mtok: price.cached_input_usd_per_mtok,
          cache_write_usd_per_mtok: price.cache_write_usd_per_mtok,
          output_usd_per_mtok: price.output_usd_per_mtok,
          long_input_usd_per_mtok: price.long_input_usd_per_mtok,
          long_cached_input_usd_per_mtok: price.long_cached_input_usd_per_mtok,
          long_cache_write_usd_per_mtok: price.long_cache_write_usd_per_mtok,
          long_output_usd_per_mtok: price.long_output_usd_per_mtok,
          long_threshold_tokens: price.long_threshold_tokens,
          priority: price.priority,
          notes: price.notes ?? '',
        }
      : emptyPrice(),
  )
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function set<K extends keyof ModelPriceInput>(key: K, value: ModelPriceInput[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  async function submit() {
    if (form.pattern.trim() === '') {
      setError('pattern 为必填，例如 gpt-6-luna 或 gpt-*-sol。')
      return
    }
    setSaving(true)
    setError(null)
    try {
      if (price) await updatePrice(price.id, form)
      else await createPrice(form)
      onSaved()
    } catch (e) {
      setError(errText(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      title={price ? `编辑 ${price.pattern}` : '新增价格'}
      onClose={onClose}
      wide
      footer={
        <>
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button type="button" className="btn primary" onClick={() => void submit()} disabled={saving}>
            保存
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <Field label="pattern" hint="精确 slug 或 glob。gpt-7-sol 可被 gpt-*-sol 接住。">
        <input
          className="input"
          value={form.pattern}
          onChange={(e) => set('pattern', e.target.value)}
          placeholder="gpt-6-luna"
        />
      </Field>
      <div className="grid grid-2">
        <NumField label="入 / 1M" value={form.input_usd_per_mtok} onChange={(n) => set('input_usd_per_mtok', n ?? 0)} />
        <NumField
          label="缓存入 / 1M"
          value={form.cached_input_usd_per_mtok}
          onChange={(n) => set('cached_input_usd_per_mtok', n ?? 0)}
        />
        <NumField
          label="写缓存 / 1M"
          value={form.cache_write_usd_per_mtok}
          onChange={(n) => set('cache_write_usd_per_mtok', n ?? 0)}
        />
        <NumField label="出 / 1M" value={form.output_usd_per_mtok} onChange={(n) => set('output_usd_per_mtok', n ?? 0)} />
      </div>
      <div className="grid grid-2">
        <NumField
          label="长上下文入 / 1M"
          value={form.long_input_usd_per_mtok}
          nullable
          onChange={(n) => set('long_input_usd_per_mtok', n)}
        />
        <NumField
          label="长上下文缓存入 / 1M"
          value={form.long_cached_input_usd_per_mtok}
          nullable
          onChange={(n) => set('long_cached_input_usd_per_mtok', n)}
        />
        <NumField
          label="长上下文写缓存 / 1M"
          value={form.long_cache_write_usd_per_mtok}
          nullable
          onChange={(n) => set('long_cache_write_usd_per_mtok', n)}
        />
        <NumField
          label="长上下文出 / 1M"
          value={form.long_output_usd_per_mtok}
          nullable
          onChange={(n) => set('long_output_usd_per_mtok', n)}
        />
      </div>
      <div className="grid grid-2">
        <NumField
          label="长上下文阈值 (tokens)"
          value={form.long_threshold_tokens}
          onChange={(n) => set('long_threshold_tokens', Math.round(n ?? 272000))}
        />
        <NumField
          label="优先级"
          value={form.priority}
          onChange={(n) => set('priority', Math.round(n ?? 100))}
        />
      </div>
      <Field label="备注">
        <input className="input" value={form.notes} onChange={(e) => set('notes', e.target.value)} />
      </Field>
    </Modal>
  )
}

function NumField({
  label,
  value,
  onChange,
  nullable,
}: {
  label: string
  value: number | null
  onChange: (n: number | null) => void
  nullable?: boolean
}) {
  return (
    <Field label={label}>
      <input
        className="input"
        type="number"
        step="any"
        value={value ?? ''}
        onChange={(e) => {
          const raw = e.target.value
          if (raw === '' && nullable) {
            onChange(null)
            return
          }
          const n = Number(raw)
          onChange(Number.isFinite(n) ? n : 0)
        }}
      />
    </Field>
  )
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}
