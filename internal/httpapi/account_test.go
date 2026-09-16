package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccountEndpointWithoutAppServer(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.handleAccount(w, httptest.NewRequest(http.MethodGet, "/api/account", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got accountSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK {
		t.Fatal("expected ok=false when App Server is not running")
	}
	if got.Error == "" {
		t.Fatal("expected an error message")
	}
}
