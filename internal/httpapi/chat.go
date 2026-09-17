package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"codeck/internal/codex"
	"codeck/internal/store"
)

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	conversations, err := s.db.ListConversations(queryLimit(r, 200, 500))
	if err != nil {
		writeStoreError(w, err, "list conversations")
		return
	}
	writeJSON(w, http.StatusOK, conversations)
}

func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string `json:"title"`
		ProfileID int64  `json:"profile_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ProfileID <= 0 {
		writeError(w, http.StatusBadRequest, "profile_id is required")
		return
	}
	conversation, err := s.db.CreateConversation(req.ProfileID, req.Title)
	if err != nil {
		writeStoreError(w, err, "create conversation")
		return
	}
	s.log.Info("conversation created", "conversation", conversation.ID, "profile", conversation.ProfileName)
	writeJSON(w, http.StatusCreated, conversation)
}

func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	conversation, err := s.db.GetConversation(id)
	if err != nil {
		writeStoreError(w, err, "conversation")
		return
	}
	messages, err := s.db.ListMessages(id)
	if err != nil {
		writeStoreError(w, err, "list messages")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation": conversation,
		"messages":     messages,
	})
}

func (s *Server) handleUpdateConversation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	conversation, err := s.db.UpdateConversation(id, req.Title, req.Status)
	if err != nil {
		writeStoreError(w, err, "update conversation")
		return
	}
	writeJSON(w, http.StatusOK, conversation)
}

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.db.DeleteConversation(id); err != nil {
		writeStoreError(w, err, "delete conversation")
		return
	}
	s.log.Info("conversation deleted", "conversation", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// chatRequest is the payload for a streaming turn.
type chatRequest struct {
	ConversationID int64  `json:"conversation_id"`
	Content        string `json:"content"`
}

// sseEvent is one Server-Sent Event frame. The frontend switches on Type.
type sseEvent struct {
	Type         string `json:"type"`
	MessageID    int64  `json:"message_id,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	Text         string `json:"text,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Message      string `json:"message,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
}

// sseWriter serialises event frames. Codex reports progress from the process
// reader goroutine, so writes are guarded by a mutex.
type sseWriter struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
	broken  bool
}

func newSSEWriter(w http.ResponseWriter) *sseWriter {
	flusher, _ := w.(http.Flusher)
	return &sseWriter{w: w, flusher: flusher}
}

func (s *sseWriter) send(ev sseEvent) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken {
		return
	}
	if _, err := s.w.Write([]byte("data: " + string(payload) + "\n\n")); err != nil {
		// The client went away; further writes are pointless.
		s.broken = true
		return
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// handleChatStream runs one Codex turn and streams progress back as SSE.
//
// The event stream is real progress from the Codex CLI: turn lifecycle and
// completed tool activity events. Codex 0.154 does not emit token-level deltas
// for the assistant message, so the reply text arrives as a single `delta`
// event when the message is complete. If a future Codex version starts emitting
// incremental events they are forwarded unchanged.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	conversation, err := s.db.GetConversation(req.ConversationID)
	if err != nil {
		writeStoreError(w, err, "conversation")
		return
	}
	profile, err := s.db.GetProfile(conversation.ProfileID)
	if err != nil {
		writeStoreError(w, err, "profile")
		return
	}

	// The user's turn is persisted before the model is called, so a crash
	// mid-run cannot lose what was asked.
	userMsg, err := s.db.AddMessage(store.Message{
		ConversationID: conversation.ID,
		Role:           "user",
		Content:        content,
		Status:         "ok",
	})
	if err != nil {
		writeStoreError(w, err, "save message")
		return
	}
	s.autoTitleIfFirst(conversation, content)

	// An assistant row is created up front so the client has a stable id to
	// attach streamed text to.
	assistant, err := s.db.AddMessage(store.Message{
		ConversationID: conversation.ID,
		Role:           "assistant",
		Status:         "running",
	})
	if err != nil {
		writeStoreError(w, err, "save message")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Tell any intermediary not to buffer: buffering would defeat streaming.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	stream := newSSEWriter(w)
	stream.send(sseEvent{
		Type:      "started",
		MessageID: assistant.ID,
		ThreadID:  conversation.ThreadID,
	})

	// Text is accumulated for the final record. Codex may emit several agent
	// messages in a turn; all of them belong to the reply.
	var (
		mu      sync.Mutex
		texts   []string
		errored string
	)

	result, runErr := s.codex.Run(r.Context(), codex.RunOptions{
		Spec:     codex.SpecFromProfile(profile),
		Prompt:   content,
		ThreadID: conversation.ThreadID,
		Timeout:  s.cfg.DefaultTimeout,
		OnUpdate: func(u codex.Update) {
			switch u.Kind {
			case codex.UpdateThreadStarted:
				// Already announced above when resuming an existing thread.
				if u.ThreadID != "" && u.ThreadID != conversation.ThreadID {
					stream.send(sseEvent{Type: "started", MessageID: assistant.ID, ThreadID: u.ThreadID})
				}
			case codex.UpdateMessage:
				mu.Lock()
				texts = append(texts, u.Text)
				mu.Unlock()
				stream.send(sseEvent{Type: "delta", Text: u.Text, MessageID: assistant.ID})
			case codex.UpdateProgress:
				stream.send(sseEvent{Type: "item", Kind: u.ItemKind, Text: u.Text})
			case codex.UpdateError:
				mu.Lock()
				errored = u.Message
				mu.Unlock()
				stream.send(sseEvent{Type: "error", Message: u.Message})
			}
		},
	})

	mu.Lock()
	reply := strings.Join(texts, "\n\n")
	streamErr := errored
	mu.Unlock()

	finalStatus := "ok"
	finalErr := ""
	if runErr != nil {
		finalStatus = "error"
		finalErr = runErr.Error()
	} else if streamErr != "" && reply == "" {
		finalStatus = "error"
		finalErr = streamErr
	}
	if reply == "" && finalStatus == "ok" {
		reply = "(the model returned no text)"
	}
	// A cancelled request means the browser navigated away; record what arrived.
	if r.Context().Err() != nil && finalStatus == "ok" && reply == "" {
		finalStatus = "error"
		finalErr = "cancelled before the reply completed"
	}

	if err := s.db.FinalizeMessage(assistant.ID, reply, finalStatus, finalErr,
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Duration.Milliseconds()); err != nil {
		s.log.Error("failed to finalize assistant message", "message", assistant.ID, "error", err)
	}

	// Record the Codex thread so the next turn resumes the same session.
	threadID := conversation.ThreadID
	if result.ThreadID != "" {
		threadID = result.ThreadID
		if result.ThreadID != conversation.ThreadID {
			if err := s.db.SetConversationThread(conversation.ID, result.ThreadID); err != nil {
				s.log.Error("failed to store thread id", "conversation", conversation.ID, "error", err)
			}
		}
	}

	if runErr != nil {
		s.log.Warn("chat turn failed",
			"conversation", conversation.ID, "profile", profile.Name, "error", runErr)
		// The message row already carries the error; also surface it on the stream.
		stream.send(sseEvent{Type: "error", Message: runErr.Error(), MessageID: assistant.ID})
	} else {
		s.log.Info("chat turn finished",
			"conversation", conversation.ID, "profile", profile.Name,
			"user_message", userMsg.ID, "duration", result.Duration,
			"input_tokens", result.Usage.InputTokens, "output_tokens", result.Usage.OutputTokens)
	}

	stream.send(sseEvent{
		Type:         "done",
		MessageID:    assistant.ID,
		ThreadID:     threadID,
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
		DurationMs:   result.Duration.Milliseconds(),
	})
}

// autoTitleIfFirst gives a new conversation a meaningful name once its first
// message arrives, so the sidebar is not a wall of placeholder titles.
func (s *Server) autoTitleIfFirst(conversation store.Conversation, content string) {
	if conversation.MessageCount > 0 || !store.IsDefaultConversationTitle(conversation.Title) {
		return
	}
	title := strings.Join(strings.Fields(content), " ")
	const max = 60
	if len(title) > max {
		title = strings.TrimSpace(title[:max]) + "…"
	}
	if title == "" {
		return
	}
	if _, err := s.db.UpdateConversation(conversation.ID, title, ""); err != nil {
		s.log.Warn("could not title conversation", "conversation", conversation.ID, "error", err)
	}
}
