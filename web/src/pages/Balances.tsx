import { useEffect, useState } from 'react'
import {
  createBalance,
  deleteBalance,
  listBalances,
  refreshBalance,
  updateBalance,
} from '../api'
import type { BalanceConfig, BalanceInput } from '../api'
import { errText } from '../api'
import { Empty, ErrorBox, Field, Modal, Spinner } from '../components/ui'
import { formatTs } from '../format'
import { useLoad } from '../useLoad'

type Editor = {
  id?: number
  provider: 'deepseek' | 'openrouter'
  name: string
  key: string
  intervalMinutes: string
}

const newEditor = (): Editor => ({
  provider: 'deepseek', name: '', key: '', intervalMinutes: '5',
})

export default function Balances() {
  const balances = useLoad(listBalances)
  const [editor, setEditor] = useState<Editor | null>(null)
  const [busy, setBusy] = useState<number | 'save' | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const timer = window.setInterval(balances.reload, 15_000)
    return () => window.clearInterval(timer)
  }, [balances.reload])

  async function save() {
    if (!editor) return
    setBusy('save')
    setError(null)
    const input: BalanceInput = {
      provider: editor.provider,
      name: editor.name.trim(),
      key: editor.key,
      interval_seconds: Number(editor.intervalMinutes) * 60,
    }
    try {
      if (editor.id) await updateBalance(editor.id, input)
      else await createBalance(input)
      setEditor(null)
      balances.reload()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(null)
    }
  }

  async function refresh(row: BalanceConfig) {
    setBusy(row.id)
    setError(null)
    try {
      const updated = await refreshBalance(row.id)
      balances.setData((previous) => previous
        ? { ...previous, balances: previous.balances.map((item) => item.id === row.id ? updated : item) }
        : previous)
    } catch (e) {
      setError(errText(e))
      balances.reload()
    } finally {
      setBusy(null)
    }
  }

  async function remove(row: BalanceConfig) {
    if (!window.confirm(`删除“${row.name}”及其保存的 Key 和余额？`)) return
    setBusy(row.id)
    setError(null)
    try {
      await deleteBalance(row.id)
      balances.setData((previous) => previous
        ? { ...previous, balances: previous.balances.filter((item) => item.id !== row.id) }
        : previous)
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(null)
    }
  }

  function edit(row: BalanceConfig) {
    if (row.provider !== 'deepseek' && row.provider !== 'openrouter') return
    setError(null)
    setEditor({
      id: row.id,
      provider: row.provider,
      name: row.name,
      key: '',
      intervalMinutes: String(row.interval_seconds / 60),
    })
  }

  const data = balances.data
  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">余额</h1>
          <div className="page-desc">查看 DeepSeek 与 OpenRouter 账户余额。各配置独立查询，金额保留平台原币种。</div>
        </div>
        <button
          type="button"
          className="btn primary"
          onClick={() => { setError(null); setEditor(newEditor()) }}
          disabled={data?.available === false}
        >
          新增余额配置
        </button>
      </div>

      {error ? <ErrorBox error={error} /> : null}
      {balances.error ? <ErrorBox error={balances.error} /> : null}
      {data?.available === false ? <div className="error-box">{data.error ?? '余额功能暂不可用，请检查服务配置。'}</div> : null}
      {balances.loading && !data ? <Spinner label="正在读取余额配置…" /> : null}

      {data?.balances.length === 0 ? (
        <Empty>还没有余额配置。添加平台 Key 后，Codeck 会立即查询并按各自周期更新。</Empty>
      ) : null}

      <div className="balance-list">
        {data?.balances.map((row) => (
          <BalanceCard
            key={row.id}
            row={row}
            disabled={!data.available || busy !== null}
            refreshing={busy === row.id}
            onRefresh={() => void refresh(row)}
            onEdit={() => edit(row)}
            onDelete={() => void remove(row)}
          />
        ))}
      </div>

      {editor ? (
        <BalanceEditor
          editor={editor}
          busy={busy === 'save'}
          error={error}
          onChange={setEditor}
          onClose={() => setEditor(null)}
          onSave={() => void save()}
        />
      ) : null}
    </div>
  )
}

function BalanceCard({
  row,
  disabled,
  refreshing,
  onRefresh,
  onEdit,
  onDelete,
}: {
  row: BalanceConfig
  disabled: boolean
  refreshing: boolean
  onRefresh: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const provider = row.provider === 'deepseek' ? 'DeepSeek' : 'OpenRouter'
  return (
    <section className="card balance-card">
      <div className="card-head">
        <div>
          <div className="card-title">{row.name}</div>
          <div className="balance-provider">{provider} · 每 {row.interval_seconds / 60} 分钟查询</div>
        </div>
        <div className="row-actions">
          <button type="button" className="btn sm" onClick={onRefresh} disabled={disabled}>
            {refreshing ? '查询中…' : '刷新'}
          </button>
          <button type="button" className="btn sm" onClick={onEdit} disabled={disabled}>编辑</button>
          <button type="button" className="btn danger sm" onClick={onDelete} disabled={disabled}>删除</button>
        </div>
      </div>
      <div className="card-body balance-body">
        {row.balances.length > 0 ? (
          <div className="balance-amounts">
            {row.balances.map((amount) => (
              <div className="balance-amount" key={amount.currency}>
                <span className="balance-value">{formatAmount(amount.total, amount.currency)}</span>
                <span className="balance-currency">{amount.currency}</span>
                {row.provider === 'deepseek' && amount.details ? (
                  <details className="balance-details">
                    <summary>余额组成</summary>
                    <div>赠金 {formatAmount(amount.details.granted_balance ?? '0', amount.currency)}</div>
                    <div>充值 {formatAmount(amount.details.topped_up_balance ?? '0', amount.currency)}</div>
                  </details>
                ) : null}
              </div>
            ))}
          </div>
        ) : (
          <div className="balance-unavailable">{row.last_error ? '尚未成功获取余额' : '正在获取余额…'}</div>
        )}
        <div className="balance-foot">
          <span>{row.last_success_at ? `最近成功查询 ${formatTs(row.last_success_at)}` : '尚无成功查询'}</span>
          {row.last_error ? <span className="balance-error">{row.last_error}</span> : null}
        </div>
      </div>
    </section>
  )
}

function BalanceEditor({
  editor,
  busy,
  error,
  onChange,
  onClose,
  onSave,
}: {
  editor: Editor
  busy: boolean
  error: string | null
  onChange: (editor: Editor) => void
  onClose: () => void
  onSave: () => void
}) {
  const isEdit = editor.id !== undefined
  const update = <K extends keyof Editor>(key: K, value: Editor[K]) =>
    onChange({ ...editor, [key]: value })

  return (
    <Modal
      title={isEdit ? '编辑余额配置' : '新增余额配置'}
      onClose={onClose}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>取消</button>
          <button type="button" className="btn primary" onClick={onSave} disabled={busy}>
            {busy ? '保存中…' : '保存并查询'}
          </button>
        </>
      }
    >
      <ErrorBox error={error && error !== '余额加密密钥未配置或格式无效' ? error : null} />
      <Field label="平台">
        <select
          className="select"
          value={editor.provider}
          onChange={(e) => update('provider', e.target.value as Editor['provider'])}
        >
          <option value="deepseek">DeepSeek</option>
          <option value="openrouter">OpenRouter</option>
        </select>
      </Field>
      <Field label="名称" hint="用名称区分同一平台下的多条配置。">
        <input className="input" value={editor.name} onChange={(e) => update('name', e.target.value)} maxLength={120} />
      </Field>
      <Field
        label="API Key"
        hint={editor.provider === 'openrouter'
          ? '账户余额需要 OpenRouter Management Key，该 Key 具备管理账户 API Key 的权限。'
          : '使用 DeepSeek API Key 查询账户余额。'}
      >
        <input
          className="input mono"
          type="password"
          autoComplete="new-password"
          value={editor.key}
          onChange={(e) => update('key', e.target.value)}
          placeholder={isEdit ? '已安全保存；留空保持不变' : '粘贴 API Key'}
          required={!isEdit}
        />
      </Field>
      <Field label="轮询周期（分钟）" hint="服务启动后立即查询，之后在后台按此周期更新。">
        <input
          className="input"
          type="number"
          min={1}
          max={43200}
          step={1}
          value={editor.intervalMinutes}
          onChange={(e) => update('intervalMinutes', e.target.value)}
        />
      </Field>
      <div className="hint">保存的 Key 使用 CODECK_BALANCE_ENCRYPTION_KEY 加密。更换 Key 后会清除旧余额并立即查询。</div>
    </Modal>
  )
}

function formatAmount(raw: string, currency: string) {
  const value = Number(raw)
  if (!Number.isFinite(value)) return `${raw} ${currency}`
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency', currency, minimumFractionDigits: 2, maximumFractionDigits: 2,
    }).format(value)
  } catch {
    return `${raw} ${currency}`
  }
}
