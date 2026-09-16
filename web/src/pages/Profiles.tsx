import { useState } from 'react'
import {
  createProfile,
  deleteProfile,
  emptyProfile,
  getProfileConfig,
  listProfiles,
  testProfile,
  updateProfile,
} from '../api'
import type { Profile, ProfileInput, ProfileTestResult } from '../api'
import { errText } from '../api'
import { useLoad } from '../useLoad'
import { Empty, ErrorBox, Field, Modal, Spinner } from '../components/ui'
import { formatDuration, formatTs, labelOf, text, tokenSummary } from '../format'

const EFFORTS = [
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
  { value: 'xhigh', label: '极高' },
  { value: 'max', label: '最大' },
]
const SANDBOXES = [
  { value: 'read-only', label: '只读' },
  { value: 'workspace-write', label: '工作区可写' },
  { value: 'danger-full-access', label: '完全访问' },
]
const APPROVALS = [
  { value: 'untrusted', label: '不信任' },
  { value: 'on-failure', label: '失败时批准' },
  { value: 'on-request', label: '请求时批准' },
  { value: 'never', label: '从不批准' },
]

function optionLabel(options: { value: string; label: string }[], value: string): string {
  return options.find((o) => o.value === value)?.label ?? labelOf(value)
}

function pickOption(
  options: { value: string; label: string }[],
  value: string | null | undefined,
  fallback: string,
): string {
  if (value && options.some((o) => o.value === value)) return value
  return fallback
}

export default function Profiles() {
  const { data, error, loading, reload } = useLoad(listProfiles)
  const [editing, setEditing] = useState<Profile | 'new' | null>(null)
  const [configFor, setConfigFor] = useState<Profile | null>(null)
  const [testFor, setTestFor] = useState<Profile | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const profiles = data ?? []

  async function handleDelete(p: Profile) {
    const label = p.name?.trim() || `档案 #${p.id}`
    if (!window.confirm(`确定删除档案「${label}」？此操作不可撤销。`)) return
    try {
      await deleteProfile(p.id)
      setNotice(`已删除档案「${label}」。`)
      reload()
    } catch (e) {
      setNotice(null)
      window.alert(`删除失败：${errText(e)}`)
    }
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">档案</h1>
          <div className="page-desc">
            每个档案是一份 Codex CLI 配置：模型、沙箱与批准策略。
          </div>
        </div>
        <button type="button" className="btn primary" onClick={() => setEditing('new')}>
          新建档案
        </button>
      </div>

      {notice ? <div className="ok-box" style={{ marginBottom: 14 }}>{notice}</div> : null}
      <ErrorBox error={error} />

      <div className="card">
        {loading && !data ? (
          <Spinner label="加载档案…" />
        ) : profiles.length === 0 ? (
          <Empty>还没有档案。先创建一个，才能开始对话或任务。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table className="table">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>模型</th>
                  <th>推理强度</th>
                  <th>沙箱</th>
                  <th>批准</th>
                  <th>模式</th>
                  <th>更新</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {profiles.map((p) => (
                  <tr key={p.id}>
                    <td>
                      <div>{p.name?.trim() ? p.name : `档案 #${p.id}`}</div>
                      {p.description?.trim() ? (
                        <div className="small faint clamp" title={p.description}>
                          {p.description}
                        </div>
                      ) : null}
                    </td>
                    <td className="mono small">{text(p.model)}</td>
                    <td className="small dim">{optionLabel(EFFORTS, p.reasoning_effort)}</td>
                    <td className="small dim">{optionLabel(SANDBOXES, p.sandbox_mode)}</td>
                    <td className="small dim">{optionLabel(APPROVALS, p.approval_policy)}</td>
                    <td>
                      {p.is_minimal ? (
                        <span className="badge accent">精简</span>
                      ) : (
                        <span className="badge">完整</span>
                      )}
                    </td>
                    <td className="small num" title={formatTs(p.updated_at)}>
                      {formatTs(p.updated_at)}
                    </td>
                    <td>
                      <div className="row-actions">
                        <button
                          type="button"
                          className="btn sm"
                          onClick={() => setTestFor(p)}
                        >
                          测试
                        </button>
                        <button
                          type="button"
                          className="btn sm"
                          onClick={() => setConfigFor(p)}
                        >
                          config.toml
                        </button>
                        <button
                          type="button"
                          className="btn sm"
                          onClick={() => setEditing(p)}
                        >
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

      {editing ? (
        <ProfileForm
          profile={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={(saved, created) => {
            setEditing(null)
            setNotice(`已${created ? '创建' : '更新'}档案「${saved.name}」。`)
            reload()
          }}
        />
      ) : null}

      {configFor ? (
        <ConfigModal profile={configFor} onClose={() => setConfigFor(null)} />
      ) : null}

      {testFor ? <TestModal profile={testFor} onClose={() => setTestFor(null)} /> : null}
    </div>
  )
}

/* -------------------------------------------------------------- profile form */

function ProfileForm({
  profile,
  onClose,
  onSaved,
}: {
  profile: Profile | null
  onClose: () => void
  onSaved: (p: Profile, created: boolean) => void
}) {
  const [form, setForm] = useState<ProfileInput>(() =>
    profile
      ? {
          name: profile.name ?? '',
          description: profile.description ?? '',
          model: profile.model ?? '',
          reasoning_effort: pickOption(EFFORTS, profile.reasoning_effort, 'low'),
          sandbox_mode: pickOption(SANDBOXES, profile.sandbox_mode, 'read-only'),
          approval_policy: pickOption(APPROVALS, profile.approval_policy, 'never'),
          extra_config: profile.extra_config ?? '',
          work_dir: profile.work_dir ?? '',
          is_minimal: Boolean(profile.is_minimal),
        }
      : emptyProfile(),
  )
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function set<K extends keyof ProfileInput>(key: K, value: ProfileInput[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  async function submit() {
    if (form.name.trim() === '') {
      setError('名称为必填。')
      return
    }
    setSaving(true)
    setError(null)
    try {
      if (profile) {
        onSaved(await updateProfile(profile.id, form), false)
      } else {
        onSaved(await createProfile(form), true)
      }
    } catch (e) {
      setError(errText(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      title={profile ? `编辑档案：${profile.name || `#${profile.id}`}` : '新建档案'}
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
            {saving ? '保存中…' : profile ? '保存' : '创建档案'}
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
          placeholder="例如 gpt5-codex-minimal"
        />
      </Field>

      <Field label="说明">
        <input
          className="input"
          value={form.description}
          onChange={(e) => set('description', e.target.value)}
          placeholder="这个档案的用途"
        />
      </Field>

      <label className={form.is_minimal ? 'checkbox-row on' : 'checkbox-row'}>
        <input
          type="checkbox"
          checked={form.is_minimal}
          onChange={(e) => set('is_minimal', e.target.checked)}
        />
        <span>
          <span className="checkbox-title">精简模式</span>
          <span className="hint">
            关闭 MCP、skills、项目上下文和重型工具，请求上下文更小、更稳定。适合定时任务和一次性短提示；需要仓库工具时请关闭。
          </span>
        </span>
      </label>

      <div className="form-grid">
        <Field label="模型" hint="留空则使用 Codex 默认模型。">
          <input
            className="input"
            value={form.model}
            onChange={(e) => set('model', e.target.value)}
            placeholder="gpt-5-codex"
          />
        </Field>

        <Field label="推理强度">
          <OptionSelect
            value={form.reasoning_effort}
            options={EFFORTS}
            onChange={(v) => set('reasoning_effort', v)}
          />
        </Field>

        <Field label="沙箱模式">
          <OptionSelect
            value={form.sandbox_mode}
            options={SANDBOXES}
            onChange={(v) => set('sandbox_mode', v)}
          />
        </Field>

        <Field label="批准策略">
          <OptionSelect
            value={form.approval_policy}
            options={APPROVALS}
            onChange={(v) => set('approval_policy', v)}
          />
        </Field>
      </div>

      <Field label="工作目录" hint="CLI 运行的绝对路径。">
        <input
          className="input mono"
          value={form.work_dir}
          onChange={(e) => set('work_dir', e.target.value)}
          placeholder="/home/user/project"
        />
      </Field>

      <Field
        label="额外配置"
        hint="追加到生成的 config.toml 末尾的原始 TOML，可选。"
      >
        <textarea
          className="textarea"
          value={form.extra_config}
          onChange={(e) => set('extra_config', e.target.value)}
          placeholder={'[mcp_servers.example]\ncommand = "example"'}
        />
      </Field>
    </Modal>
  )
}

function OptionSelect({
  value,
  options,
  onChange,
}: {
  value: string
  options: { value: string; label: string }[]
  onChange: (v: string) => void
}) {
  // Preserve values that came from the server but are not in our suggestion list.
  const list =
    value && !options.some((o) => o.value === value)
      ? [{ value, label: value }, ...options]
      : options
  return (
    <select className="select" value={value} onChange={(e) => onChange(e.target.value)}>
      {list.map((opt) => (
        <option key={opt.value} value={opt.value}>
          {opt.label}
        </option>
      ))}
    </select>
  )
}

/* -------------------------------------------------------------- config view */

function ConfigModal({ profile, onClose }: { profile: Profile; onClose: () => void }) {
  const { data, error, loading } = useLoad(() => getProfileConfig(profile.id), [profile.id])
  return (
    <Modal title={`config.toml — ${profile.name || `#${profile.id}`}`} onClose={onClose} wide>
      <ErrorBox error={error} />
      <div className="hint" style={{ marginBottom: 10 }}>
        由服务端生成，只读。这就是该档案下 Codex CLI 实际读到的配置。
      </div>
      {loading && !data ? (
        <Spinner label="正在生成配置…" />
      ) : data ? (
        <pre className="pre">{data.config_toml?.trim() ? data.config_toml : '（空）'}</pre>
      ) : null}
    </Modal>
  )
}

/* ---------------------------------------------------------------- test run */

function TestModal({ profile, onClose }: { profile: Profile; onClose: () => void }) {
  const [prompt, setPrompt] = useState('Reply with the single word: ok')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<ProfileTestResult | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function run() {
    setRunning(true)
    setError(null)
    setResult(null)
    try {
      setResult(await testProfile(profile.id, prompt))
    } catch (e) {
      setError(errText(e))
    } finally {
      setRunning(false)
    }
  }

  return (
    <Modal
      title={`测试档案：${profile.name || `#${profile.id}`}`}
      onClose={onClose}
      wide
      footer={
        <>
          <button type="button" className="btn" onClick={onClose}>
            关闭
          </button>
          <button
            type="button"
            className="btn primary"
            onClick={() => void run()}
            disabled={running}
          >
            {running ? '运行中…' : '开始测试'}
          </button>
        </>
      }
    >
      <div className="hint" style={{ marginBottom: 12 }}>
        用此档案真实调用一次 Codex，大约需要 10–60 秒。
      </div>

      <Field label="提示词">
        <textarea
          className="textarea"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          disabled={running}
        />
      </Field>

      <ErrorBox error={error} />

      {running ? (
        <Spinner label="等待 Codex… 最多可能需要一分钟。" />
      ) : result ? (
        <div>
          <div className="toolbar" style={{ marginBottom: 10 }}>
            <span className={result.ok ? 'badge ok' : 'badge err'}>
              {result.ok ? '成功' : '失败'}
            </span>
            <span className="small dim">耗时 {formatDuration(result.duration_ms)}</span>
            <span className="small dim">
              {tokenSummary(result.input_tokens, result.output_tokens) || '无 token 用量'}
            </span>
          </div>
          {result.error ? (
            <div className="error-box">{result.error}</div>
          ) : null}
          <pre className="pre">{result.output?.trim() ? result.output : '（无输出）'}</pre>
        </div>
      ) : null}
    </Modal>
  )
}
