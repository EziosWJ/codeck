package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"croncodex/internal/codex"
	"croncodex/internal/store"
)

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.db.ListProfiles()
	if err != nil {
		writeStoreError(w, err, "list profiles")
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

// profileRequest is the accepted payload for creating and updating a profile.
// It mirrors the profile fields a client is allowed to set.
type profileRequest struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	SandboxMode     string `json:"sandbox_mode"`
	ApprovalPolicy  string `json:"approval_policy"`
	ExtraConfig     string `json:"extra_config"`
	WorkDir         string `json:"work_dir"`
	IsMinimal       bool   `json:"is_minimal"`
}

func (p profileRequest) toStore() store.Profile {
	return store.Profile{
		Name:            p.Name,
		Description:     p.Description,
		Model:           p.Model,
		ReasoningEffort: p.ReasoningEffort,
		SandboxMode:     p.SandboxMode,
		ApprovalPolicy:  p.ApprovalPolicy,
		ExtraConfig:     p.ExtraConfig,
		WorkDir:         p.WorkDir,
		IsMinimal:       p.IsMinimal,
	}
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req profileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	created, err := s.db.CreateProfile(req.toStore())
	if err != nil {
		writeProfileError(w, err, "create profile")
		return
	}
	// Materialise the profile's CODEX_HOME immediately so problems (an
	// unwritable data directory, missing credentials) surface now rather than
	// on the first chat message.
	if _, err := s.codex.EnsureHome(codex.SpecFromProfile(created)); err != nil {
		s.log.Error("profile saved but its codex home could not be prepared",
			"profile", created.Name, "error", err)
		writeJSON(w, http.StatusCreated, map[string]any{
			"profile": created,
			"warning": "the profile was saved but its codex home could not be prepared: " + err.Error(),
		})
		return
	}
	s.log.Info("profile created", "profile", created.ID, "name", created.Name, "minimal", created.IsMinimal)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req profileRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	existing, err := s.db.GetProfile(id)
	if err != nil {
		writeStoreError(w, err, "profile")
		return
	}

	updated := req.toStore()
	updated.ID = id
	saved, err := s.db.UpdateProfile(updated)
	if err != nil {
		writeProfileError(w, err, "update profile")
		return
	}

	// Renaming a profile moves its CODEX_HOME, so the old directory is removed.
	if existing.Name != saved.Name {
		if err := s.codex.RemoveHome(existing.Name); err != nil {
			s.log.Warn("could not remove the previous codex home", "name", existing.Name, "error", err)
		}
	}
	if _, err := s.codex.EnsureHome(codex.SpecFromProfile(saved)); err != nil {
		s.log.Error("profile saved but its codex home could not be prepared",
			"profile", saved.Name, "error", err)
		writeJSON(w, http.StatusOK, map[string]any{
			"profile": saved,
			"warning": "the profile was saved but its codex home could not be prepared: " + err.Error(),
		})
		return
	}
	s.log.Info("profile updated", "profile", saved.ID, "name", saved.Name)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	profile, err := s.db.GetProfile(id)
	if err != nil {
		writeStoreError(w, err, "profile")
		return
	}

	// Conversations and tasks cascade with the profile; report what was lost.
	conversations, _ := s.db.CountConversationsForProfile(id)
	if err := s.db.DeleteProfile(id); err != nil {
		writeStoreError(w, err, "delete profile")
		return
	}
	if err := s.codex.RemoveHome(profile.Name); err != nil {
		s.log.Warn("profile deleted but its codex home remains", "name", profile.Name, "error", err)
	}
	s.log.Info("profile deleted", "profile", id, "name", profile.Name, "conversations_removed", conversations)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "conversations_removed": conversations})
}

func (s *Server) handleProfileConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	profile, err := s.db.GetProfile(id)
	if err != nil {
		writeStoreError(w, err, "profile")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"config_toml": s.codex.PreviewConfig(codex.SpecFromProfile(profile)),
	})
}

// handleTestProfile runs a single throwaway turn to confirm a profile works.
// It does not create a conversation and does not resume any thread.
func (s *Server) handleTestProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	// A body is optional here, so an empty request is not an error.
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = "Reply with exactly: PONG"
	}

	profile, err := s.db.GetProfile(id)
	if err != nil {
		writeStoreError(w, err, "profile")
		return
	}

	// Allow a little more than the default: a cold Codex start is slow.
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.DefaultTimeout+30*time.Second)
	defer cancel()

	s.log.Info("testing profile", "profile", profile.Name, "minimal", profile.IsMinimal)
	result, runErr := s.codex.Run(ctx, codex.RunOptions{
		Spec:    codex.SpecFromProfile(profile),
		Prompt:  prompt,
		Timeout: s.cfg.DefaultTimeout + 30*time.Second,
	})

	resp := map[string]any{
		"ok":            runErr == nil,
		"output":        result.Output,
		"error":         "",
		"duration_ms":   result.Duration.Milliseconds(),
		"input_tokens":  result.Usage.InputTokens,
		"output_tokens": result.Usage.OutputTokens,
		"thread_id":     result.ThreadID,
	}
	if runErr != nil {
		resp["error"] = runErr.Error()
		s.log.Warn("profile test failed", "profile", profile.Name, "error", runErr)
	} else {
		s.log.Info("profile test succeeded", "profile", profile.Name,
			"duration", result.Duration, "input_tokens", result.Usage.InputTokens)
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeProfileError reports validation problems as 400s and everything else as
// a store error.
func writeProfileError(w http.ResponseWriter, err error, what string) {
	if isValidationError(err) {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}
	writeStoreError(w, err, what)
}

// isValidationError distinguishes user-fixable input problems from failures.
// The store's Validate messages are the only 400-worthy errors it returns.
func isValidationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"name must", "sandbox_mode must", "reasoning_effort must",
		"approval_policy must", "minimal mode cannot",
		"already exists", "name is required", "prompt is required",
		"cron_expr is required", "invalid cron expression",
		"cron expression is required",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
