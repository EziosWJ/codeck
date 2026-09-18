package httpapi

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"codeck/internal/config"
	"codeck/internal/store"
)

func TestChatReturnsConflictWhenConversationAlreadyRunning(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	p, err := db.CreateProfile(store.Profile{Name: "chat-profile", SandboxMode: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := db.CreateConversation(p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.BeginConversationTurn(conversation.ID, "existing"); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	s := NewServer(cfg, db, nil, nil, nil, testLogger(), "test", nil)
	body := []byte(`{"conversation_id":` + fmt.Sprint(conversation.ID) + `,"content":"second"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409: %s", rec.Code, rec.Body.String())
	}

	messages, err := db.ListMessages(conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("conflicting HTTP turn inserted rows: got %d messages, want 2", len(messages))
	}
}
