/**
 * Single place where the Codex backend is talked to.
 * Everything is typed, every call funnels through `request()`.
 */

const BASE = '/api'

export class ApiError extends Error {
  status: number
  constructor(message: string, status = 0) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export function errText(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}

async function readError(res: Response): Promise<string> {
  const text = await res.text().catch(() => '')
  if (!text) return `HTTP ${res.status} ${res.statusText}`
  try {
    const parsed: unknown = JSON.parse(text)
    if (parsed && typeof parsed === 'object') {
      const rec = parsed as Record<string, unknown>
      const msg = rec.error ?? rec.message
      if (typeof msg === 'string' && msg.trim() !== '') return msg
    }
  } catch {
    /* body was not JSON — fall through to the raw text */
  }
  return text.slice(0, 500)
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(BASE + path, {
      ...init,
      headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
    })
  } catch (e) {
    throw new ApiError(`无法连接服务器：${e instanceof Error ? e.message : String(e)}`)
  }
  if (!res.ok) throw new ApiError(await readError(res), res.status)
  const text = await res.text()
  if (!text) return undefined as T
  try {
    return JSON.parse(text) as T
  } catch {
    throw new ApiError(`接口 ${path} 返回了无效 JSON`, res.status)
  }
}

function body(data: unknown): RequestInit {
  return { method: 'POST', body: JSON.stringify(data) }
}

function put(data: unknown): RequestInit {
  return { method: 'PUT', body: JSON.stringify(data) }
}

/* ------------------------------------------------------------------ types */

export interface Health {
  ok: boolean
  codex_available: boolean
  codex_version: string
  codex_bin: string
  db_path: string
  version: string
  scheduler_enabled?: boolean
}

export interface Profile {
  id: number
  name: string
  description: string
  model: string
  reasoning_effort: string
  sandbox_mode: string
  approval_policy: string
  extra_config: string
  work_dir: string
  is_minimal: boolean
  include_permissions_instructions: boolean
  include_apps_instructions: boolean
  include_collaboration_mode_instructions: boolean
  include_environment_context: boolean
  created_at: string
  updated_at: string
}

/** Payload accepted by POST /profiles and PUT /profiles/{id}. */
export interface ProfileInput {
  name: string
  description: string
  model: string
  reasoning_effort: string
  sandbox_mode: string
  approval_policy: string
  extra_config: string
  work_dir: string
  is_minimal: boolean
  include_permissions_instructions: boolean
  include_apps_instructions: boolean
  include_collaboration_mode_instructions: boolean
  include_environment_context: boolean
}

export function emptyProfile(): ProfileInput {
  return {
    name: '',
    description: '',
    model: '',
    reasoning_effort: 'low',
    sandbox_mode: 'read-only',
    approval_policy: 'never',
    extra_config: '',
    work_dir: '',
    is_minimal: false,
    include_permissions_instructions: false,
    include_apps_instructions: false,
    include_collaboration_mode_instructions: false,
    include_environment_context: false,
  }
}

export type InboundKind =
  | 'skills'
  | 'permissions'
  | 'apps'
  | 'plugins'
  | 'plugin_recommendations'
  | 'environment'
  | 'user'
  | 'other'

export interface InboundBlock {
  kind: InboundKind | string
  role: string
  text: string
}

export interface InboundSettings {
  model?: string
  effort?: string
  approval_policy?: string
  sandbox?: string
}

export interface InboundPrompt {
  source: 'session' | 'prompt-input' | string
  thread_id?: string
  blocks: InboundBlock[]
  settings?: InboundSettings | null
}

export interface TokenUsage {
  input_tokens?: number
  cached_input_tokens?: number
  cache_write_input_tokens?: number
  output_tokens?: number
  reasoning_output_tokens?: number
  total_tokens?: number
  [key: string]: number | undefined
}

export interface ProfileTestResult {
  ok: boolean
  output: string
  error: string
  duration_ms: number | null
  input_tokens: number | null
  cached_input_tokens?: number | null
  output_tokens: number | null
  thread_id?: string
  usage?: TokenUsage | null
  inbound?: InboundPrompt | null
}

export interface Conversation {
  id: number
  title: string
  profile_id: number
  profile_name: string
  thread_id: string | null
  status: string
  message_count: number
  created_at: string
  updated_at: string
}

export type MessageRole = 'user' | 'assistant'
export type MessageStatus = 'ok' | 'error' | 'running'

export interface Message {
  id: number
  conversation_id: number
  role: MessageRole
  content: string
  status: MessageStatus
  error: string | null
  input_tokens: number | null
  output_tokens: number | null
  duration_ms: number | null
  created_at: string
}

export interface ConversationDetail {
  conversation: Conversation
  messages: Message[]
}

export interface Task {
  id: number
  name: string
  prompt: string
  profile_id: number
  profile_name: string
  cron_expr: string
  schedule_type: 'cron' | 'once' | string
  run_at: string | null
  enabled: boolean
  work_dir: string
  timeout_sec: number
  last_run_at: string | null
  next_run_at: string | null
  last_status: string | null
  created_at: string
  updated_at: string
}

/** Payload accepted by POST /tasks and PUT /tasks/{id}. */
export interface TaskInput {
  name: string
  prompt: string
  profile_id: number
  cron_expr: string
  schedule_type: 'cron' | 'once'
  run_at: string | null
  enabled: boolean
  work_dir: string
  timeout_sec: number
}

export type RunStatus = 'running' | 'success' | 'failed'
export type RunTrigger = 'schedule' | 'manual' | 'once'

export interface TaskRun {
  id: number
  task_id: number
  task_name: string
  status: RunStatus
  output: string
  error: string | null
  trigger: RunTrigger
  started_at: string
  finished_at: string | null
  duration_ms: number | null
  input_tokens: number | null
  output_tokens: number | null
}

export interface DashboardData {
  counts: {
    profiles: number
    conversations: number
    tasks: number
    enabled_tasks: number
    runs_today: number
  }
  recent_runs: TaskRun[]
  recent_conversations: Conversation[]
}

export interface RateLimitWindow {
  usedPercent: number
  windowDurationMins: number | null
  resetsAt: number | null
}

export interface RateLimitSnapshot {
  limitId?: string | null
  primary: RateLimitWindow | null
  secondary: RateLimitWindow | null
  credits: {
    hasCredits: boolean
    unlimited: boolean
    balance: string | null
  } | null
  planType: string | null
}

export interface AccountRead {
  account: {
    type?: string
    email?: string | null
    planType?: string
  } | null
  requiresOpenaiAuth: boolean
}

export interface RateLimitResetCredit {
  id: string
  status?: string
  grantedAt?: number
  expiresAt: number | null
  title: string | null
}

export interface AccountRateLimits {
  ordinaryUsageAllowed: boolean | null
  rateLimits: RateLimitSnapshot | null
  rateLimitResetCredits: {
    availableCount: number
    credits: RateLimitResetCredit[] | null
  } | null
  accountId: string | null
}

export interface AccountUsage {
  summary: {
    lifetimeTokens: number | null
    peakDailyTokens: number | null
    longestRunningTurnSec: number | null
    currentStreakDays: number | null
    longestStreakDays: number | null
  }
  dailyUsageBuckets: { startDate: string; tokens: number }[] | null
}

export interface AccountData {
  ok: boolean
  error?: string
  account: AccountRead | null
  rate_limits: AccountRateLimits | null
  usage: AccountUsage | null
}

export interface HistoryData {
  messages: Message[]
  runs: TaskRun[]
}

/* --------------------------------------------------------------- REST API */

export interface UsageTotals {
  turns: number
  input_tokens: number
  cached_tokens: number
  uncached_tokens: number
  cache_write_tokens: number
  output_tokens: number
  reasoning_tokens: number
  total_tokens: number
  usd: number | null
  unpriced_turns: number
  unpriced_tokens: number
}

export interface UsageModelRow extends UsageTotals {
  model: string
  matched_by?: string
  priced: boolean
  wildcard: boolean
  long_turns: number
}

export interface UsageDayModel {
  model: string
  tokens: number
  usd: number | null
}

export interface UsageDayRow {
  date: string
  tokens: number
  usd: number | null
  models: UsageDayModel[]
}

export interface UsageReport {
  scanned_at: string
  roots: string[]
  files: number
  turns: number
  errors?: string[]
  summary: UsageTotals
  by_model: UsageModelRow[]
  daily: UsageDayRow[]
}

export interface ModelPrice {
  id: number
  pattern: string
  input_usd_per_mtok: number
  cached_input_usd_per_mtok: number
  cache_write_usd_per_mtok: number
  output_usd_per_mtok: number
  long_input_usd_per_mtok: number | null
  long_cached_input_usd_per_mtok: number | null
  long_cache_write_usd_per_mtok: number | null
  long_output_usd_per_mtok: number | null
  long_threshold_tokens: number
  priority: number
  notes: string
  created_at: string
  updated_at: string
}

export interface ModelPriceInput {
  pattern: string
  input_usd_per_mtok: number
  cached_input_usd_per_mtok: number
  cache_write_usd_per_mtok: number
  output_usd_per_mtok: number
  long_input_usd_per_mtok: number | null
  long_cached_input_usd_per_mtok: number | null
  long_cache_write_usd_per_mtok: number | null
  long_output_usd_per_mtok: number | null
  long_threshold_tokens: number
  priority: number
  notes: string
}

export function emptyPrice(): ModelPriceInput {
  return {
    pattern: '',
    input_usd_per_mtok: 0,
    cached_input_usd_per_mtok: 0,
    cache_write_usd_per_mtok: 0,
    output_usd_per_mtok: 0,
    long_input_usd_per_mtok: null,
    long_cached_input_usd_per_mtok: null,
    long_cache_write_usd_per_mtok: null,
    long_output_usd_per_mtok: null,
    long_threshold_tokens: 272000,
    priority: 100,
    notes: '',
  }
}

export const getHealth = () => request<Health>('/health')
export const getDashboard = () => request<DashboardData>('/dashboard')
export const getAccount = () => request<AccountData>('/account')
export const getUsage = (refresh = false) =>
  request<UsageReport>(`/usage${refresh ? '?refresh=1' : ''}`)
export const listPrices = () => request<ModelPrice[]>('/prices')
export const createPrice = (p: ModelPriceInput) => request<ModelPrice>('/prices', body(p))
export const updatePrice = (id: number, p: ModelPriceInput) =>
  request<ModelPrice>(`/prices/${id}`, put(p))
export const deletePrice = (id: number) =>
  request<{ ok: boolean }>(`/prices/${id}`, { method: 'DELETE' })
export const restorePrices = () => request<ModelPrice[]>('/prices/restore', { method: 'POST' })

export const listProfiles = () => request<Profile[]>('/profiles')
export const createProfile = (p: ProfileInput) => request<Profile>('/profiles', body(p))
export const updateProfile = (id: number, p: ProfileInput) =>
  request<Profile>(`/profiles/${id}`, put(p))
export const deleteProfile = (id: number) =>
  request<{ ok: boolean }>(`/profiles/${id}`, { method: 'DELETE' })
export const getProfileConfig = (id: number) =>
  request<{ config_toml: string }>(`/profiles/${id}/config`)
export const testProfile = (id: number, prompt?: string) =>
  request<ProfileTestResult>(`/profiles/${id}/test`, body({ prompt: prompt ?? '' }))
export const previewProfilePrompt = (id: number, prompt?: string) =>
  request<InboundPrompt>(`/profiles/${id}/prompt-preview`, body({ prompt: prompt ?? '' }))

export const listConversations = () => request<Conversation[]>('/conversations')
export const createConversation = (profileId: number, title?: string) =>
  request<Conversation>('/conversations', body({ profile_id: profileId, title: title ?? '' }))
export const getConversation = (id: number) =>
  request<ConversationDetail>(`/conversations/${id}`)
export const patchConversation = (id: number, patch: { title?: string; status?: string }) =>
  request<Conversation>(`/conversations/${id}`, { method: 'PATCH', body: JSON.stringify(patch) })
export const deleteConversation = (id: number) =>
  request<{ ok: boolean }>(`/conversations/${id}`, { method: 'DELETE' })

export const listTasks = () => request<Task[]>('/tasks')
export const createTask = (t: TaskInput) => request<Task>('/tasks', body(t))
export const updateTask = (id: number, t: TaskInput) => request<Task>(`/tasks/${id}`, put(t))
export const deleteTask = (id: number) =>
  request<{ ok: boolean }>(`/tasks/${id}`, { method: 'DELETE' })
export const runTask = (id: number) =>
  request<{ run_id: number }>(`/tasks/${id}/run`, { method: 'POST' })
export const listTaskRuns = (id: number) => request<TaskRun[]>(`/tasks/${id}/runs`)
export const listRuns = (limit = 50) => request<TaskRun[]>(`/runs?limit=${limit}`)
export const getHistory = (limit = 100) => request<HistoryData>(`/history?limit=${limit}`)

/* ------------------------------------------------------------ chat stream */

/** Values produced by codex.Item.Kind on the server. */
export type ChatItemKind =
  | 'message'
  | 'reasoning'
  | 'command'
  | 'file_change'
  | 'tool'
  | 'error'
  | 'other'

export type ChatEvent =
  | { type: 'started'; message_id: number; thread_id: string }
  | { type: 'delta'; text: string }
  | { type: 'item'; kind: ChatItemKind; text: string }
  | {
      type: 'done'
      message_id: number
      input_tokens: number
      output_tokens: number
      duration_ms: number
    }
  | { type: 'error'; message: string }

function isAbort(e: unknown): boolean {
  return e instanceof Error && e.name === 'AbortError'
}

/**
 * SSE frame extraction: a frame is a block of lines, and every `data:` line
 * belongs to the payload (joined with newlines, per the SSE spec).
 */
function frameData(frame: string): string | null {
  const parts: string[] = []
  for (const rawLine of frame.split('\n')) {
    const line = rawLine.endsWith('\r') ? rawLine.slice(0, -1) : rawLine
    if (!line.startsWith('data:')) continue
    const value = line.slice(5)
    parts.push(value.startsWith(' ') ? value.slice(1) : value)
  }
  return parts.length > 0 ? parts.join('\n') : null
}

/**
 * POSTs to /api/chat/stream and pumps the SSE body.
 *
 * EventSource cannot be used here because the endpoint is a POST, so the
 * body is read manually. Chunks can split a frame anywhere, so a buffer is
 * kept between reads and only complete `\n\n`-terminated frames are consumed.
 */
export async function streamChat(
  conversationId: number,
  content: string,
  onEvent: (ev: ChatEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  let res: Response
  try {
    res = await fetch(`${BASE}/chat/stream`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
      body: JSON.stringify({ conversation_id: conversationId, content }),
      signal,
    })
  } catch (e) {
    if (isAbort(e)) return
    throw new ApiError(`无法连接服务器：${e instanceof Error ? e.message : String(e)}`)
  }
  if (!res.ok) throw new ApiError(await readError(res), res.status)
  if (!res.body) throw new ApiError('当前浏览器无法读取流式响应。')

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  const drain = (flush: boolean) => {
    buffer = buffer.replace(/\r\n/g, '\n')
    let sep = buffer.indexOf('\n\n')
    while (sep !== -1) {
      const frame = buffer.slice(0, sep)
      buffer = buffer.slice(sep + 2)
      emit(frame, onEvent)
      sep = buffer.indexOf('\n\n')
    }
    if (flush && buffer.trim() !== '') {
      emit(buffer, onEvent)
      buffer = ''
    }
  }

  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      drain(false)
    }
    buffer += decoder.decode()
    drain(true)
  } catch (e) {
    if (isAbort(e)) return
    throw new ApiError(`流式响应中断：${e instanceof Error ? e.message : String(e)}`)
  }
}

function emit(frame: string, onEvent: (ev: ChatEvent) => void) {
  const data = frameData(frame)
  if (data === null) return
  let parsed: unknown
  try {
    parsed = JSON.parse(data)
  } catch {
    return // ignore keep-alive comments and malformed frames
  }
  if (!parsed || typeof parsed !== 'object') return
  const ev = parsed as ChatEvent
  if (typeof (ev as { type?: unknown }).type !== 'string') return
  onEvent(ev)
}
