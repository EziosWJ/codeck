import { useCallback, useEffect, useRef, useState } from 'react'
import {
  createConversation,
  deleteConversation,
  errText,
  getConversation,
  listConversations,
  listProfiles,
  patchConversation,
  streamChat,
} from '../api'
import type { ChatItemKind, Conversation, ConversationDetail, Message, Profile } from '../api'
import { useLoad } from '../useLoad'
import { Empty, ErrorBox, Spinner, StatusBadge } from '../components/ui'
import { formatDuration, formatRelative, formatTs, labelOf, text, tokenSummary } from '../format'

interface SideNote {
  kind: ChatItemKind
  text: string
}

/** Shared empty array so the "no data yet" case keeps a stable reference. */
const EMPTY_CONVERSATIONS: Conversation[] = []

interface Pending {
  user: string
  text: string
  notes: SideNote[]
  error: string | null
  done: boolean
  inputTokens: number | null
  outputTokens: number | null
  durationMs: number | null
}

export default function Chat() {
  const conversations = useLoad(listConversations)
  const profiles = useLoad(listProfiles)

  const [activeId, setActiveId] = useState<number | null>(null)
  const [detail, setDetail] = useState<ConversationDetail | null>(null)
  const [detailError, setDetailError] = useState<string | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [pending, setPending] = useState<Pending | null>(null)
  const [input, setInput] = useState('')
  const [newProfileId, setNewProfileId] = useState(0)
  const [actionError, setActionError] = useState<string | null>(null)

  const abortRef = useRef<AbortController | null>(null)
  const seqRef = useRef(0)
  const activeIdRef = useRef<number | null>(null)
  const scrollRef = useRef<HTMLDivElement | null>(null)

  const list = conversations.data ?? EMPTY_CONVERSATIONS
  const profileList: Profile[] = profiles.data ?? []
  const streaming = pending !== null && !pending.done

  useEffect(() => {
    activeIdRef.current = activeId
  }, [activeId])

  const loadDetail = useCallback(async (id: number): Promise<ConversationDetail | null> => {
    const seq = ++seqRef.current
    setDetailLoading(true)
    try {
      const result = await getConversation(id)
      if (seqRef.current !== seq) return null
      setDetail(result)
      setDetailError(null)
      return result
    } catch (e) {
      if (seqRef.current !== seq) return null
      setDetail(null)
      setDetailError(errText(e))
      return null
    } finally {
      if (seqRef.current === seq) setDetailLoading(false)
    }
  }, [])

  /* Pick a conversation as soon as the list arrives. `list` is stable while the
     server data is unchanged, so this does not re-run on every render. */
  useEffect(() => {
    if (activeId !== null || list.length === 0) return
    setActiveId(list[0].id)
  }, [list, activeId])

  /* Load the transcript, and drop any in-flight stream when switching. */
  useEffect(() => {
    abortRef.current?.abort()
    abortRef.current = null
    setPending(null)
    setActionError(null)
    if (activeId === null) {
      seqRef.current++
      setDetail(null)
      setDetailError(null)
      return
    }
    void loadDetail(activeId)
  }, [activeId, loadDetail])

  useEffect(() => {
    return () => abortRef.current?.abort()
  }, [])

  /* Keep the newest content in view. */
  useEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [detail, pending?.text, pending?.notes.length, pending?.done, pending?.error])

  async function send() {
    const content = input.trim()
    if (content === '' || activeId === null || streaming) return
    setInput('')
    setActionError(null)
    setPending({
      user: content,
      text: '',
      notes: [],
      error: null,
      done: false,
      inputTokens: null,
      outputTokens: null,
      durationMs: null,
    })

    const conversationId = activeId
    const controller = new AbortController()
    abortRef.current = controller

    try {
      await streamChat(
        conversationId,
        content,
        (ev) => {
          setPending((prev) => {
            if (!prev) return prev
            switch (ev.type) {
              case 'delta':
                return { ...prev, text: prev.text + ev.text }
              case 'item':
                return { ...prev, notes: [...prev.notes, { kind: ev.kind, text: ev.text }] }
              case 'done':
                return {
                  ...prev,
                  done: true,
                  inputTokens: ev.input_tokens,
                  outputTokens: ev.output_tokens,
                  durationMs: ev.duration_ms,
                }
              case 'error':
                return { ...prev, done: true, error: ev.message }
              default:
                return prev
            }
          })
        },
        controller.signal,
      )
    } catch (e) {
      setPending((prev) => (prev ? { ...prev, done: true, error: errText(e) } : prev))
    } finally {
      abortRef.current = null
      // Reconcile with what the server actually stored — unless the user has
      // already switched to another conversation.
      if (activeIdRef.current === conversationId) await reconcile(conversationId)
    }
  }

  /** Reload the transcript + list, and retire the local pending bubble. */
  async function reconcile(conversationId: number) {
    const fresh = await loadDetail(conversationId)
    if (fresh === null) return
    conversations.reload()
    setPending((prev) => {
      if (!prev) return null
      // If the client saw an error the server did not record, keep it visible.
      const serverRecordedError = fresh.messages.some(
        (m) => m.role === 'assistant' && m.status === 'error',
      )
      if (prev.error && !serverRecordedError) return prev
      return null
    })
  }

  async function newConversation() {
    if (!newProfileId) {
      setActionError('请选择新对话使用的档案。')
      return
    }
    setActionError(null)
    try {
      const created = await createConversation(newProfileId)
      conversations.reload()
      setActiveId(created.id)
    } catch (e) {
      setActionError(errText(e))
    }
  }

  async function removeConversation(conv: Conversation) {
    const label = conv.title?.trim() || `对话 #${conv.id}`
    if (!window.confirm(`确定删除「${label}」及其全部消息？`)) return
    try {
      await deleteConversation(conv.id)
      if (activeId === conv.id) setActiveId(null)
      conversations.reload()
    } catch (e) {
      setActionError(errText(e))
    }
  }

  async function renameConversation(conv: Conversation) {
    const next = window.prompt('对话标题', conv.title ?? '')
    if (next === null) return
    try {
      await patchConversation(conv.id, { title: next })
      conversations.reload()
      if (activeId === conv.id) void loadDetail(conv.id)
    } catch (e) {
      setActionError(errText(e))
    }
  }

  const activeConv = detail?.conversation ?? list.find((c) => c.id === activeId) ?? null

  return (
    <div className="chat">
      <aside className="chat-side">
        <div className="chat-side-head">
          <select
            className="select"
            value={newProfileId || ''}
            onChange={(e) => setNewProfileId(Number(e.target.value))}
          >
            <option value="">选择对话所用档案…</option>
            {profileList.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name?.trim() ? p.name : `档案 #${p.id}`}
                {p.is_minimal ? '（精简）' : ''}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="btn primary"
            onClick={() => void newConversation()}
            disabled={profileList.length === 0}
          >
            新建对话
          </button>
        </div>

        <div className="conv-list">
          {conversations.loading && !conversations.data ? (
            <Spinner label="加载中…" />
          ) : list.length === 0 ? (
            <Empty>还没有对话。</Empty>
          ) : (
            list.map((conv) => (
              <div
                key={conv.id}
                className={conv.id === activeId ? 'conv active' : 'conv'}
                onClick={() => setActiveId(conv.id)}
              >
                <div className="conv-top">
                  <span className="conv-title">{text(conv.title)}</span>
                  <StatusBadge status={conv.status} />
                </div>
                <div className="conv-meta">
                  {text(conv.profile_name)} · {conv.message_count ?? 0} 条 ·{' '}
                  {formatRelative(conv.updated_at)}
                </div>
              </div>
            ))
          )}
        </div>
      </aside>

      <section className="chat-main">
        <div className="chat-head">
          {activeConv ? (
            <>
              <strong>{text(activeConv.title)}</strong>
              <span className="badge accent">
                档案：{text(activeConv.profile_name)}
              </span>
              {activeConv.thread_id ? (
                <span className="small faint mono" title={activeConv.thread_id}>
                  线程 {activeConv.thread_id.slice(0, 12)}
                </span>
              ) : (
                <span className="small faint">尚未建立线程</span>
              )}
              <div className="spacer" />
              <button
                type="button"
                className="btn sm"
                onClick={() => void renameConversation(activeConv)}
              >
                重命名
              </button>
              <button
                type="button"
                className="btn sm danger"
                onClick={() => void removeConversation(activeConv)}
              >
                删除
              </button>
            </>
          ) : (
            <span className="dim">未选择对话</span>
          )}
        </div>

        <div className="transcript" ref={scrollRef}>
          <div className="transcript-inner">
            <ErrorBox error={actionError} />
            <ErrorBox error={detailError} />
            <ErrorBox error={conversations.error} />

            {activeId === null ? (
              <Empty>
                {profileList.length === 0
                  ? '请先创建档案，对话必须绑定一个档案。'
                  : '选择已有对话，或新建一个。'}
              </Empty>
            ) : detailLoading && !detail ? (
              <Spinner label="加载记录…" />
            ) : detail && detail.messages.length === 0 && !pending ? (
              <Empty>还没有消息。在下方输入以开始。</Empty>
            ) : (
              <>
                {detail?.messages.map((m) => <MessageBubble key={m.id} message={m} />)}
                {pending ? <PendingTurn pending={pending} /> : null}
              </>
            )}
          </div>
        </div>

        <div className="composer">
          <div className="composer-inner">
            <textarea
              value={input}
              placeholder={
                activeId === null ? '请先选择对话' : '发给 Codex…'
              }
              disabled={activeId === null}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  void send()
                }
              }}
            />
            <button
              type="button"
              className="btn primary"
              onClick={() => void send()}
              disabled={activeId === null || streaming || input.trim() === ''}
            >
              {streaming ? '运行中…' : '发送'}
            </button>
          </div>
          <div className="composer-hint">
            <span>Enter 发送 · Shift+Enter 换行</span>
            {activeConv ? <span>绑定档案 {text(activeConv.profile_name)}</span> : null}
          </div>
        </div>
      </section>
    </div>
  )
}

function MessageBubble({ message }: { message: Message }) {
  const isUser = message.role === 'user'
  return (
    <div className={isUser ? 'msg user' : 'msg assistant'}>
      <div className="msg-meta">
        <span>{isUser ? '我' : 'Codex'}</span>
        <span>{formatTs(message.created_at)}</span>
        {message.status && message.status !== 'ok' ? (
          <StatusBadge status={message.status} />
        ) : null}
        {!isUser && message.status === 'running' ? (
          <span className="dots">
            <span>.</span>
            <span>.</span>
            <span>.</span>
          </span>
        ) : null}
      </div>
      <div className={message.status === 'error' ? 'msg-bubble error' : 'msg-bubble'}>
        {message.content && message.content.trim() !== '' ? message.content : ''}
        {message.status === 'running' && (!message.content || message.content === '') ? (
          <span className="dots">
            <span>.</span>
            <span>.</span>
            <span>.</span>
          </span>
        ) : null}
      </div>
      {message.error ? <div className="msg-bubble error">{message.error}</div> : null}
      {!isUser && message.status !== 'running' ? (
        <div className="msg-meta" style={{ marginTop: 6 }}>
          <span>{formatDuration(message.duration_ms)}</span>
          <span>{tokenSummary(message.input_tokens, message.output_tokens)}</span>
        </div>
      ) : null}
    </div>
  )
}

function PendingTurn({ pending }: { pending: Pending }) {
  const waiting = !pending.done && pending.text === ''
  return (
    <>
      <div className="msg user">
        <div className="msg-meta">
          <span>我</span>
          <span>发送中…</span>
        </div>
        <div className="msg-bubble">{pending.user}</div>
      </div>

      <div className="msg assistant">
        <div className="msg-meta">
          <span>Codex</span>
          {!pending.done ? (
            <span className="dots">
              <span>.</span>
              <span>.</span>
              <span>.</span>
            </span>
          ) : null}
        </div>
        {pending.notes.length > 0 ? (
          <div className="notes">
            {pending.notes.map((note, index) => (
              <div className="note" key={`${note.kind}-${index}`} title={note.text}>
                {labelOf(note.kind)}：{note.text}
              </div>
            ))}
          </div>
        ) : null}
        <div className={pending.error ? 'msg-bubble error' : 'msg-bubble'}>
          {pending.text}
          {!pending.done ? <span className="cursor" /> : null}
          {waiting ? <span className="dim">等待 Codex…</span> : null}
        </div>
        {pending.error ? <div className="msg-bubble error">{pending.error}</div> : null}
        {pending.done && !pending.error ? (
          <div className="msg-meta" style={{ marginTop: 6 }}>
            <span>{formatDuration(pending.durationMs)}</span>
            <span>{tokenSummary(pending.inputTokens, pending.outputTokens)}</span>
          </div>
        ) : null}
      </div>
    </>
  )
}
