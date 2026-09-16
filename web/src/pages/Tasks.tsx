import { useEffect, useState } from 'react'
import {
  createTask,
  deleteTask,
  listProfiles,
  listTaskRuns,
  listTasks,
  runTask,
  updateTask,
} from '../api'
import type { Profile, Task, TaskInput, TaskRun } from '../api'
import { errText } from '../api'
import { useLoad } from '../useLoad'
import { Empty, ErrorBox, Field, Modal, Spinner, StatusBadge } from '../components/ui'
import { formatDuration, formatRelative, formatTs, labelOf, text, tokenSummary, truncate } from '../format'

export default function Tasks() {
  const tasks = useLoad(listTasks)
  const profiles = useLoad(listProfiles)
  const [editing, setEditing] = useState<Task | 'new' | null>(null)
  const [expanded, setExpanded] = useState<number | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [historyNonce, setHistoryNonce] = useState(0)

  const rows = tasks.data ?? []

  async function toggleEnabled(task: Task) {
    setBusyId(task.id)
    try {
      await updateTask(task.id, toInput(task, { enabled: !task.enabled }))
      tasks.reload()
    } catch (e) {
      window.alert(`无法更新任务：${errText(e)}`)
    } finally {
      setBusyId(null)
    }
  }

  async function runNow(task: Task) {
    setBusyId(task.id)
    setNotice(null)
    try {
      const { run_id } = await runTask(task.id)
      setNotice(`已排队「${task.name || `任务 #${task.id}`}」（运行 #${run_id}）。`)
      setExpanded(task.id)
      // The run is fire-and-forget; give the scheduler a moment, then refresh.
      window.setTimeout(() => {
        tasks.reload()
        setHistoryNonce((n) => n + 1)
      }, 1200)
    } catch (e) {
      window.alert(`无法启动任务：${errText(e)}`)
    } finally {
      setBusyId(null)
    }
  }

  async function remove(task: Task) {
    const label = task.name?.trim() || `任务 #${task.id}`
    if (!window.confirm(`确定删除任务「${label}」？运行记录会保留。`)) return
    try {
      await deleteTask(task.id)
      setNotice(`已删除任务「${label}」。`)
      tasks.reload()
    } catch (e) {
      window.alert(`删除失败：${errText(e)}`)
    }
  }

  const profileList: Profile[] = profiles.data ?? []

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">任务</h1>
          <div className="page-desc">
            定时 Codex 提示词。由后端按 cron 执行，也可立即手动跑一次。
          </div>
        </div>
        <button
          type="button"
          className="btn primary"
          onClick={() => setEditing('new')}
          disabled={profileList.length === 0}
          title={profileList.length === 0 ? '请先创建档案' : undefined}
        >
          新建任务
        </button>
      </div>

      {notice ? <div className="ok-box" style={{ marginBottom: 14 }}>{notice}</div> : null}
      <ErrorBox error={tasks.error} />
      <ErrorBox error={profiles.error} />
      {profileList.length === 0 && !profiles.loading ? (
        <div className="card">
          <div className="card-body hint">
            还没有档案。任务必须绑定档案，请先到「档案」页创建一个。
          </div>
        </div>
      ) : null}

      <div className="card">
        {tasks.loading && !tasks.data ? (
          <Spinner label="加载任务…" />
        ) : rows.length === 0 ? (
          <Empty>还没有任务。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table className="table">
              <thead>
                <tr>
                  <th>任务</th>
                  <th>Cron</th>
                  <th>启用</th>
                  <th>下次运行</th>
                  <th>上次运行</th>
                  <th>上次状态</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {rows.map((task) => (
                  <TaskRow
                    key={task.id}
                    task={task}
                    busy={busyId === task.id}
                    expanded={expanded === task.id}
                    historyNonce={historyNonce}
                    onToggleExpand={() =>
                      setExpanded((cur) => (cur === task.id ? null : task.id))
                    }
                    onToggleEnabled={() => void toggleEnabled(task)}
                    onRun={() => void runNow(task)}
                    onEdit={() => setEditing(task)}
                    onDelete={() => void remove(task)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {editing ? (
        <TaskForm
          task={editing === 'new' ? null : editing}
          profiles={profileList}
          onClose={() => setEditing(null)}
          onSaved={(saved, created) => {
            setEditing(null)
            setNotice(`已${created ? '创建' : '更新'}任务「${saved.name}」。`)
            tasks.reload()
          }}
        />
      ) : null}
    </div>
  )
}

function toInput(task: Task, override: Partial<TaskInput> = {}): TaskInput {
  return {
    name: task.name ?? '',
    prompt: task.prompt ?? '',
    profile_id: task.profile_id,
    cron_expr: task.cron_expr ?? '',
    enabled: Boolean(task.enabled),
    work_dir: task.work_dir ?? '',
    timeout_sec: task.timeout_sec ?? 0,
    ...override,
  }
}

function TaskRow({
  task,
  busy,
  expanded,
  historyNonce,
  onToggleExpand,
  onToggleEnabled,
  onRun,
  onEdit,
  onDelete,
}: {
  task: Task
  busy: boolean
  expanded: boolean
  historyNonce: number
  onToggleExpand: () => void
  onToggleEnabled: () => void
  onRun: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  return (
    <>
      <tr>
        <td>
          <div>{task.name?.trim() ? task.name : `任务 #${task.id}`}</div>
          <div className="small faint clamp" title={task.prompt}>
            {truncate(task.prompt, 70)}
          </div>
          <div className="small faint">档案：{text(task.profile_name)}</div>
        </td>
        <td className="mono small nowrap">{text(task.cron_expr)}</td>
        <td>
          <label className="switch">
            <input type="checkbox" checked={Boolean(task.enabled)} onChange={onToggleEnabled} disabled={busy} />
            {task.enabled ? '开' : '关'}
          </label>
        </td>
        <td className="num small nowrap" title={formatTs(task.next_run_at)}>
          {formatRelative(task.next_run_at)}
        </td>
        <td className="num small nowrap" title={formatTs(task.last_run_at)}>
          {formatRelative(task.last_run_at)}
        </td>
        <td>
          <StatusBadge status={task.last_status} />
        </td>
        <td>
          <div className="row-actions">
            <button type="button" className="btn sm" onClick={onRun} disabled={busy}>
              {busy ? '启动中…' : '立即运行'}
            </button>
            <button type="button" className="btn sm" onClick={onToggleExpand}>
              {expanded ? '收起' : '记录'}
            </button>
            <button type="button" className="btn sm" onClick={onEdit} disabled={busy}>
              编辑
            </button>
            <button type="button" className="btn sm danger" onClick={onDelete} disabled={busy}>
              删除
            </button>
          </div>
        </td>
      </tr>
      {expanded ? (
        <tr className="subtable">
          <td colSpan={7}>
            <RunHistory taskId={task.id} nonce={historyNonce} />
          </td>
        </tr>
      ) : null}
    </>
  )
}

function RunHistory({ taskId, nonce }: { taskId: number; nonce: number }) {
  const [runs, setRuns] = useState<TaskRun[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let alive = true
    setLoading(true)
    listTaskRuns(taskId)
      .then((r) => {
        if (!alive) return
        setRuns(r)
        setError(null)
      })
      .catch((e: unknown) => {
        if (alive) setError(errText(e))
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [taskId, nonce])

  if (loading && !runs) return <Spinner label="加载运行记录…" />
  if (error) return <ErrorBox error={error} />
  if (!runs || runs.length === 0) return <Empty>此任务尚未运行。</Empty>

  return (
    <div style={{ overflowX: 'auto' }}>
      <table className="table">
        <thead>
          <tr>
            <th>运行</th>
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
              <td className="num small">#{run.id}</td>
              <td>
                <StatusBadge status={run.status} />
              </td>
              <td className="small dim">{labelOf(run.trigger)}</td>
              <td className="num small nowrap" title={formatTs(run.started_at)}>
                {formatTs(run.started_at)}
              </td>
              <td className="num small">{formatDuration(run.duration_ms)}</td>
              <td className="num small dim">
                {tokenSummary(run.input_tokens, run.output_tokens) || '—'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {runs
        .filter((r) => r.error || r.output)
        .slice(0, 3)
        .map((run) => (
          <div key={`out-${run.id}`} style={{ padding: '4px 0 10px' }}>
            <div className="small faint">运行 #{run.id} 输出</div>
            {run.error ? (
              <pre className="run-output" style={{ color: 'var(--err)' }}>
                {run.error}
              </pre>
            ) : null}
            {run.output ? <pre className="run-output">{run.output}</pre> : null}
          </div>
        ))}
    </div>
  )
}

function TaskForm({
  task,
  profiles,
  onClose,
  onSaved,
}: {
  task: Task | null
  profiles: Profile[]
  onClose: () => void
  onSaved: (t: Task, created: boolean) => void
}) {
  const [form, setForm] = useState<TaskInput>(() => ({
    name: task?.name ?? '',
    prompt: task?.prompt ?? '',
    profile_id: task?.profile_id ?? profiles[0]?.id ?? 0,
    cron_expr: task?.cron_expr ?? '*/5 * * * *',
    enabled: task ? Boolean(task.enabled) : true,
    work_dir: task?.work_dir ?? '',
    timeout_sec: task?.timeout_sec ?? 300,
  }))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function set<K extends keyof TaskInput>(key: K, value: TaskInput[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  async function submit() {
    if (form.name.trim() === '') return setError('名称为必填。')
    if (!form.cron_expr.trim()) return setError('cron 表达式为必填。')
    if (!form.profile_id) return setError('请选择档案。')
    setSaving(true)
    setError(null)
    try {
      if (task) {
        onSaved(await updateTask(task.id, form), false)
      } else {
        onSaved(await createTask(form), true)
      }
    } catch (e) {
      setError(errText(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      title={task ? `编辑任务：${task.name || `#${task.id}`}` : '新建任务'}
      onClose={onClose}
      wide
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={saving}>
            取消
          </button>
          <button
            type="button"
            className="btn primary"
            onClick={() => void submit()}
            disabled={saving}
          >
            {saving ? '保存中…' : task ? '保存' : '创建任务'}
          </button>
        </>
      }
    >
      <ErrorBox error={error} />

      <Field label="名称">
        <input
          className="input"
          value={form.name}
          onChange={(e) => set('name', e.target.value)}
          placeholder="例如 nightly-summary"
        />
      </Field>

      <Field label="提示词" hint="每次运行原样发给 Codex。">
        <textarea
          className="textarea"
          style={{ minHeight: 130 }}
          value={form.prompt}
          onChange={(e) => set('prompt', e.target.value)}
          placeholder="汇总昨天的提交并写入 NOTES.md"
        />
      </Field>

      <div className="form-grid">
        <Field label="档案">
          <select
            className="select"
            value={String(form.profile_id)}
            onChange={(e) => set('profile_id', Number(e.target.value))}
          >
            <option value="0">（选择档案）</option>
            {profiles.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name?.trim() ? p.name : `档案 #${p.id}`}
                {p.is_minimal ? '（精简）' : ''}
              </option>
            ))}
          </select>
        </Field>

        <Field label="Cron 表达式" hint="标准五字段 cron，例如 */5 * * * *">
          <input
            className="input mono"
            value={form.cron_expr}
            onChange={(e) => set('cron_expr', e.target.value)}
            placeholder="0 9 * * 1-5"
          />
        </Field>

        <Field label="超时（秒）">
          <input
            className="input"
            type="number"
            min={1}
            value={form.timeout_sec}
            onChange={(e) => set('timeout_sec', Number(e.target.value))}
          />
        </Field>

        <Field label="工作目录" hint="留空则使用档案的工作目录。">
          <input
            className="input mono"
            value={form.work_dir}
            onChange={(e) => set('work_dir', e.target.value)}
            placeholder="/home/user/project"
          />
        </Field>
      </div>

      <label className="checkbox-row">
        <input
          type="checkbox"
          checked={form.enabled}
          onChange={(e) => set('enabled', e.target.checked)}
        />
        <span>
          <span className="checkbox-title">启用</span>
          <span className="hint">关闭后仍保留计划，但不会触发。</span>
        </span>
      </label>
    </Modal>
  )
}
