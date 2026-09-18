package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultConversationTitle is assigned when a conversation is created without one.
const DefaultConversationTitle = "新对话"

// legacyDefaultConversationTitle is the pre-localisation default; auto-title
// still treats it as unnamed so existing rows get a real title on first message.
const legacyDefaultConversationTitle = "New conversation"

// IsDefaultConversationTitle reports whether title is still the placeholder.
func IsDefaultConversationTitle(title string) bool {
	return title == DefaultConversationTitle || title == legacyDefaultConversationTitle
}

// Conversation is a chat thread bound to a single profile. ThreadID is the
// Codex thread id used to resume the underlying Codex session.
type Conversation struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	ProfileID    int64     `json:"profile_id"`
	ProfileName  string    `json:"profile_name"`
	ThreadID     string    `json:"thread_id"`
	Status       string    `json:"status"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Message is a single turn stored in a conversation.
type Message struct {
	ID             int64     `json:"id"`
	ConversationID int64     `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	Status         string    `json:"status"`
	Error          string    `json:"error"`
	InputTokens    int       `json:"input_tokens"`
	OutputTokens   int       `json:"output_tokens"`
	DurationMs     int64     `json:"duration_ms"`
	CreatedAt      time.Time `json:"created_at"`
}

const conversationCols = `c.id, c.title, c.profile_id, COALESCE(p.name, ''), c.thread_id,
	c.status, (SELECT COUNT(*) FROM messages m WHERE m.conversation_id = c.id),
	c.created_at, c.updated_at`

func scanConversation(sc interface{ Scan(...any) error }) (Conversation, error) {
	var c Conversation
	var created, updated string
	err := sc.Scan(&c.ID, &c.Title, &c.ProfileID, &c.ProfileName, &c.ThreadID,
		&c.Status, &c.MessageCount, &created, &updated)
	if err != nil {
		return c, err
	}
	c.CreatedAt, _ = parseTime(created)
	c.UpdatedAt, _ = parseTime(updated)
	return c, nil
}

// ListConversations returns conversations, most recently updated first.
func (d *DB) ListConversations(limit int) ([]Conversation, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := d.sql.Query(`SELECT `+conversationCols+` FROM conversations c
		LEFT JOIN profiles p ON p.id = c.profile_id
		ORDER BY c.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Conversation{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConversation loads one conversation by id.
func (d *DB) GetConversation(id int64) (Conversation, error) {
	row := d.sql.QueryRow(`SELECT `+conversationCols+` FROM conversations c
		LEFT JOIN profiles p ON p.id = c.profile_id WHERE c.id = ?`, id)
	c, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// CreateConversation starts a new thread against a profile.
func (d *DB) CreateConversation(profileID int64, title string) (Conversation, error) {
	if _, err := d.GetProfile(profileID); err != nil {
		return Conversation{}, fmt.Errorf("profile %d: %w", profileID, err)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = DefaultConversationTitle
	}
	now := formatTime(time.Now())
	res, err := d.sql.Exec(`
INSERT INTO conversations (title, profile_id, thread_id, status, created_at, updated_at)
VALUES (?,?,?,?,?,?)`, title, profileID, "", "active", now, now)
	if err != nil {
		return Conversation{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Conversation{}, err
	}
	return d.GetConversation(id)
}

// UpdateConversation patches the title and/or status. Empty values are ignored.
func (d *DB) UpdateConversation(id int64, title, status string) (Conversation, error) {
	existing, err := d.GetConversation(id)
	if err != nil {
		return Conversation{}, err
	}
	if t := strings.TrimSpace(title); t != "" {
		existing.Title = t
	}
	if s := strings.TrimSpace(status); s != "" {
		existing.Status = s
	}
	_, err = d.sql.Exec(`UPDATE conversations SET title = ?, status = ?, updated_at = ? WHERE id = ?`,
		existing.Title, existing.Status, formatTime(time.Now()), id)
	if err != nil {
		return Conversation{}, err
	}
	return d.GetConversation(id)
}

// SetConversationThread records the Codex thread id so later turns can resume.
func (d *DB) SetConversationThread(id int64, threadID string) error {
	_, err := d.sql.Exec(`UPDATE conversations SET thread_id = ?, updated_at = ? WHERE id = ?`,
		threadID, formatTime(time.Now()), id)
	return err
}

// DeleteConversation removes a conversation and its messages.
func (d *DB) DeleteConversation(id int64) error {
	res, err := d.sql.Exec(`DELETE FROM conversations WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const messageCols = `id, conversation_id, role, content, status, error,
	input_tokens, output_tokens, duration_ms, created_at`

func scanMessage(sc interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var created string
	err := sc.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Status,
		&m.Error, &m.InputTokens, &m.OutputTokens, &m.DurationMs, &created)
	if err != nil {
		return m, err
	}
	m.CreatedAt, _ = parseTime(created)
	return m, nil
}

// ListMessages returns every message in a conversation, oldest first.
func (d *DB) ListMessages(conversationID int64) ([]Message, error) {
	rows, err := d.sql.Query(`SELECT `+messageCols+` FROM messages
		WHERE conversation_id = ? ORDER BY id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMessage loads a single message.
func (d *DB) GetMessage(id int64) (Message, error) {
	row := d.sql.QueryRow(`SELECT `+messageCols+` FROM messages WHERE id = ?`, id)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// BeginConversationTurn atomically appends the user message and reserves the
// conversation with one running assistant message. The partial unique index on
// running assistant rows makes this safe across processes sharing the database.
func (d *DB) BeginConversationTurn(conversationID int64, content string) (Message, Message, error) {
	tx, err := d.sql.Begin()
	if err != nil {
		return Message{}, Message{}, err
	}
	rollback := func(err error) (Message, Message, error) {
		_ = tx.Rollback()
		return Message{}, Message{}, err
	}

	now := formatTime(time.Now())
	userRes, err := tx.Exec(`
INSERT INTO messages (conversation_id, role, content, status, error,
    input_tokens, output_tokens, duration_ms, created_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		conversationID, "user", content, "ok", "", 0, 0, 0, now)
	if err != nil {
		return rollback(err)
	}
	userID, err := userRes.LastInsertId()
	if err != nil {
		return rollback(err)
	}

	assistantRes, err := tx.Exec(`
INSERT INTO messages (conversation_id, role, content, status, error,
    input_tokens, output_tokens, duration_ms, created_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		conversationID, "assistant", "", "running", "", 0, 0, 0, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return rollback(fmt.Errorf("%w: conversation already has an active turn", ErrConflict))
		}
		return rollback(err)
	}
	assistantID, err := assistantRes.LastInsertId()
	if err != nil {
		return rollback(err)
	}

	if _, err := tx.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`, now, conversationID); err != nil {
		return rollback(err)
	}
	if err := tx.Commit(); err != nil {
		return Message{}, Message{}, err
	}

	user, err := d.GetMessage(userID)
	if err != nil {
		return Message{}, Message{}, err
	}
	assistant, err := d.GetMessage(assistantID)
	if err != nil {
		return Message{}, Message{}, err
	}
	return user, assistant, nil
}

// AddMessage appends a message and touches the conversation's updated_at.
func (d *DB) AddMessage(m Message) (Message, error) {
	if m.Status == "" {
		m.Status = "ok"
	}
	now := formatTime(time.Now())
	res, err := d.sql.Exec(`
INSERT INTO messages (conversation_id, role, content, status, error,
    input_tokens, output_tokens, duration_ms, created_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		m.ConversationID, m.Role, m.Content, m.Status, m.Error,
		m.InputTokens, m.OutputTokens, m.DurationMs, now)
	if err != nil {
		return Message{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	if _, err := d.sql.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`,
		now, m.ConversationID); err != nil {
		return Message{}, err
	}
	return d.GetMessage(id)
}

// FinalizeMessage writes the outcome of a streamed assistant turn.
func (d *DB) FinalizeMessage(id int64, content, status, errText string, inTok, outTok int, durationMs int64) error {
	_, err := d.sql.Exec(`
UPDATE messages SET content = ?, status = ?, error = ?, input_tokens = ?,
    output_tokens = ?, duration_ms = ? WHERE id = ?`,
		content, status, errText, inTok, outTok, durationMs, id)
	return err
}

// FinalizeConversationTurn updates the Codex thread identity and releases the
// active-turn reservation in one transaction. Thread state is committed before
// the assistant row stops being "running", so the next admitted turn cannot
// observe a stale thread id.
func (d *DB) FinalizeConversationTurn(messageID, conversationID int64, threadID,
	content, status, errText string, inTok, outTok int, durationMs int64) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	rollback := func(err error) error {
		_ = tx.Rollback()
		return err
	}

	now := formatTime(time.Now())
	if threadID != "" {
		if _, err := tx.Exec(`UPDATE conversations SET thread_id = ?, updated_at = ? WHERE id = ?`,
			threadID, now, conversationID); err != nil {
			return rollback(err)
		}
	} else if _, err := tx.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`,
		now, conversationID); err != nil {
		return rollback(err)
	}

	res, err := tx.Exec(`
UPDATE messages SET content = ?, status = ?, error = ?, input_tokens = ?,
    output_tokens = ?, duration_ms = ?
WHERE id = ? AND conversation_id = ? AND role = 'assistant' AND status = 'running'`,
		content, status, errText, inTok, outTok, durationMs, messageID, conversationID)
	if err != nil {
		return rollback(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return rollback(fmt.Errorf("%w: assistant turn is no longer running", ErrConflict))
	}
	return tx.Commit()
}

// ListRecentMessages returns the newest messages across all conversations,
// joined with their conversation title for display in the history view.
func (d *DB) ListRecentMessages(limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := d.sql.Query(`SELECT `+messageCols+` FROM messages ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkStaleMessagesFailed closes assistant messages left in the running state
// by a previous process, so the UI never shows a permanently pending reply.
func (d *DB) MarkStaleMessagesFailed() (int64, error) {
	res, err := d.sql.Exec(`UPDATE messages SET status = 'error',
		error = 'interrupted: the service restarted while this reply was in progress'
		WHERE status = 'running'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountConversations returns the total number of conversations.
func (d *DB) CountConversations() (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&n)
	return n, err
}
