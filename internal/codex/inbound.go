package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PromptBlock is one piece of the model-visible inbound prompt.
type PromptBlock struct {
	Kind string `json:"kind"`
	Role string `json:"role"`
	Text string `json:"text"`
}

// TurnSettings is the snapshot Codex recorded for the turn (session files only).
type TurnSettings struct {
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	ApprovalPolicy string `json:"approval_policy,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
}

// InboundPrompt is the assembled prompt Codex stuffed in front of the user text.
type InboundPrompt struct {
	Source   string        `json:"source"`
	ThreadID string        `json:"thread_id,omitempty"`
	Blocks   []PromptBlock `json:"blocks"`
	Settings *TurnSettings `json:"settings,omitempty"`
}

const (
	kindSkills      = "skills"
	kindPermissions = "permissions"
	kindApps        = "apps"
	kindPlugins     = "plugins"
	kindPluginRecs  = "plugin_recommendations"
	kindEnvironment = "environment"
	kindUser        = "user"
	kindOther       = "other"
)

// LoadInboundFromSession reads the rollout jsonl for threadID under the
// profile home. Missing files return a zero value, not an error: a failed
// exec may still have produced no session.
func (s *Service) LoadInboundFromSession(spec ProfileSpec, threadID string) (InboundPrompt, error) {
	if threadID == "" {
		return InboundPrompt{}, nil
	}
	home := s.HomeDir(spec.Name)
	var path string
	var err error
	for i := 0; i < 8; i++ {
		path, err = findSessionFile(home, threadID)
		if err != nil {
			return InboundPrompt{}, err
		}
		if path != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if path == "" {
		return InboundPrompt{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return InboundPrompt{}, err
	}
	defer f.Close()
	inbound, err := parseSessionInbound(f)
	if err != nil {
		return InboundPrompt{}, err
	}
	inbound.Source = "session"
	inbound.ThreadID = threadID
	return inbound, nil
}

// LoadUsageFromSession reads the last token_usage_record from the thread's
// rollout file. The session payload is richer than exec --json (cached tokens).
func (s *Service) LoadUsageFromSession(spec ProfileSpec, threadID string) (Usage, bool, error) {
	if threadID == "" {
		return Usage{}, false, nil
	}
	home := s.HomeDir(spec.Name)
	path, err := findSessionFile(home, threadID)
	if err != nil || path == "" {
		return Usage{}, false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Usage{}, false, err
	}
	defer f.Close()
	usage, ok := parseSessionUsage(f)
	return usage, ok, nil
}

// PreviewInbound runs `codex debug prompt-input` in the profile home. It does
// not call the model.
func (s *Service) PreviewInbound(ctx context.Context, spec ProfileSpec, prompt string) (InboundPrompt, error) {
	home, err := s.ensureHome(spec)
	if err != nil {
		return InboundPrompt{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	args := []string{"debug", "prompt-input"}
	if strings.TrimSpace(prompt) != "" {
		args = append(args, prompt)
	}
	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Dir = s.WorkspaceDir(spec)
	cmd.Env = s.childEnv(home)
	configureProcess(cmd)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return InboundPrompt{}, fmt.Errorf("codex debug prompt-input: %s", truncate(detail, 2000))
	}
	inbound, err := parsePromptInputJSON(out)
	if err != nil {
		return InboundPrompt{}, err
	}
	inbound.Source = "prompt-input"
	return inbound, nil
}

func findSessionFile(home, threadID string) (string, error) {
	root := filepath.Join(home, "sessions")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return "", nil
	}
	var match string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.Contains(d.Name(), threadID) && strings.HasSuffix(d.Name(), ".jsonl") {
			match = path
			return fs.SkipAll
		}
		return nil
	})
	return match, err
}

func parseSessionInbound(r io.Reader) (InboundPrompt, error) {
	dec := json.NewDecoder(r)
	var inbound InboundPrompt
	for {
		var line struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := dec.Decode(&line); err != nil {
			if err == io.EOF {
				break
			}
			return inbound, err
		}
		switch line.Type {
		case "response_item":
			blocks := blocksFromResponseItem(line.Payload)
			inbound.Blocks = append(inbound.Blocks, blocks...)
		case "turn_context":
			if settings := settingsFromTurnContext(line.Payload); settings != nil {
				inbound.Settings = settings
			}
		}
	}
	return inbound, nil
}

func parseSessionUsage(r io.Reader) (Usage, bool) {
	dec := json.NewDecoder(r)
	var last Usage
	found := false
	for {
		var line struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := dec.Decode(&line); err != nil {
			break
		}
		switch line.Type {
		case "token_usage_record":
			var payload struct {
				Usage *Usage `json:"usage"`
			}
			if err := json.Unmarshal(line.Payload, &payload); err != nil || payload.Usage == nil {
				continue
			}
			last = *payload.Usage
			found = true
		case "event_msg":
			var payload struct {
				Type string          `json:"type"`
				Info *TokenCountInfo `json:"info"`
			}
			if err := json.Unmarshal(line.Payload, &payload); err != nil {
				continue
			}
			if payload.Type != EventTokenCount || payload.Info == nil {
				continue
			}
			src := payload.Info.LastTokenUsage
			if src == nil {
				src = payload.Info.TotalTokenUsage
			}
			if src == nil {
				continue
			}
			last = *src
			found = true
		}
	}
	return last, found
}

func blocksFromResponseItem(raw json.RawMessage) []PromptBlock {
	var payload struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Meta struct {
			Kinds []string `json:"content_item_kinds"`
		} `json:"internal_chat_message_metadata_passthrough"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	if payload.Type != "" && payload.Type != "message" {
		return nil
	}
	if payload.Role == "assistant" {
		return nil
	}
	var out []PromptBlock
	for i, part := range payload.Content {
		if part.Type != "" && part.Type != "input_text" {
			continue
		}
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		kind := kindOther
		if i < len(payload.Meta.Kinds) {
			kind = kindFromContentItem(payload.Meta.Kinds[i])
		}
		if kind == kindOther {
			kind = kindFromText(text, payload.Role)
		}
		out = append(out, PromptBlock{Kind: kind, Role: payload.Role, Text: text})
	}
	return out
}

func settingsFromTurnContext(raw json.RawMessage) *TurnSettings {
	var payload struct {
		Model          string `json:"model"`
		Effort         string `json:"effort"`
		ApprovalPolicy string `json:"approval_policy"`
		SandboxPolicy  struct {
			Type string `json:"type"`
		} `json:"sandbox_policy"`
		CollaborationMode struct {
			Settings struct {
				ReasoningEffort string `json:"reasoning_effort"`
			} `json:"settings"`
		} `json:"collaboration_mode"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	effort := payload.Effort
	if effort == "" {
		effort = payload.CollaborationMode.Settings.ReasoningEffort
	}
	if payload.Model == "" && effort == "" && payload.ApprovalPolicy == "" && payload.SandboxPolicy.Type == "" {
		return nil
	}
	return &TurnSettings{
		Model:          payload.Model,
		Effort:         effort,
		ApprovalPolicy: payload.ApprovalPolicy,
		Sandbox:        payload.SandboxPolicy.Type,
	}
}

func parsePromptInputJSON(raw []byte) (InboundPrompt, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return InboundPrompt{}, fmt.Errorf("codex debug prompt-input produced no output")
	}
	// Skip any leading non-JSON warnings Codex may print.
	if i := bytes.IndexAny(raw, "[{"); i > 0 {
		raw = raw[i:]
	}
	var messages []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		return InboundPrompt{}, fmt.Errorf("parse prompt-input json: %w", err)
	}
	var inbound InboundPrompt
	for _, msg := range messages {
		if msg.Role == "assistant" {
			continue
		}
		for _, part := range msg.Content {
			if part.Type != "" && part.Type != "input_text" {
				continue
			}
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			inbound.Blocks = append(inbound.Blocks, PromptBlock{
				Kind: kindFromText(text, msg.Role),
				Role: msg.Role,
				Text: text,
			})
		}
	}
	return inbound, nil
}

func kindFromContentItem(kind string) string {
	switch kind {
	case "host_skills.instructions":
		return kindSkills
	case "permissions.instructions":
		return kindPermissions
	case "apps.instructions":
		return kindApps
	case "plugins.usage_instructions":
		return kindPlugins
	case "plugins.recommendations":
		return kindPluginRecs
	case "environments.environment_context":
		return kindEnvironment
	case "user.text":
		return kindUser
	default:
		return kindOther
	}
}

func kindFromText(text, role string) string {
	switch {
	case strings.HasPrefix(text, "<skills_instructions>"):
		return kindSkills
	case strings.HasPrefix(text, "<permissions"):
		return kindPermissions
	case strings.HasPrefix(text, "<environment_context>"):
		return kindEnvironment
	case strings.HasPrefix(text, "<recommended_plugins>"):
		return kindPluginRecs
	case strings.Contains(text, "<apps"):
		return kindApps
	case strings.Contains(text, "plugin") && role == "developer":
		return kindPlugins
	case role == "user":
		return kindUser
	default:
		return kindOther
	}
}
