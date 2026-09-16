package codex

import (
	"bufio"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// EventType values emitted by `codex exec --json`.
const (
	EventThreadStarted = "thread.started"
	EventTurnStarted   = "turn.started"
	EventTurnCompleted = "turn.completed"
	EventItemStarted   = "item.started"
	EventItemUpdated   = "item.updated"
	EventItemCompleted = "item.completed"
	EventError         = "error"
	EventTurnFailed    = "turn.failed"
	EventDelta         = "delta"
)

// Item types found inside item.* events.
const (
	ItemAgentMessage     = "agent_message"
	ItemReasoning        = "reasoning"
	ItemCommandExecution = "command_execution"
	ItemFileChange       = "file_change"
	ItemMCPToolCall      = "mcp_tool_call"
	ItemError            = "error"
)

// Event is one line of Codex's JSONL output.
type Event struct {
	Type     string      `json:"type"`
	ThreadID string      `json:"thread_id"`
	Message  string      `json:"message"`
	Item     *Item       `json:"item"`
	Usage    *Usage      `json:"usage"`
	Error    *TurnError `json:"error"`
}

// TurnError is the nested error object on turn.failed events.
type TurnError struct {
	Message string `json:"message"`
}

// Item is a unit of work reported by Codex. Which content field is meaningful
// depends on Type.
type Item struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Text             string `json:"text"`
	Message          string `json:"message"`
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	Status           string `json:"status"`
	ExitCode         *int   `json:"exit_code"`
}

// Usage is the token accounting Codex reports when a turn finishes.
type Usage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
}

// IsMessage reports whether the item carries assistant-visible text.
func (i *Item) IsMessage() bool {
	return i.Type == ItemAgentMessage && i.Text != ""
}

// Kind classifies an item for the UI so it can style progress notes apart from
// the final answer.
func (i *Item) Kind() string {
	switch i.Type {
	case ItemAgentMessage:
		return "message"
	case ItemReasoning:
		return "reasoning"
	case ItemCommandExecution:
		return "command"
	case ItemFileChange:
		return "file_change"
	case ItemMCPToolCall:
		return "tool"
	case ItemError:
		return "error"
	default:
		return "other"
	}
}

// Describe renders an item as a short human-readable note.
func (i *Item) Describe() string {
	switch i.Type {
	case ItemCommandExecution:
		label := i.Command
		if i.ExitCode != nil {
			label += "\n(exit " + strconv.Itoa(*i.ExitCode) + ")"
		}
		if out := strings.TrimSpace(i.AggregatedOutput); out != "" {
			const max = 2000
			if len(out) > max {
				out = out[:max] + "\n… (truncated)"
			}
			label += "\n" + out
		}
		return label
	case ItemError:
		return i.Message
	default:
		if i.Text != "" {
			return i.Text
		}
		return i.Message
	}
}

// scanEvents reads newline-delimited JSON from r and invokes handle for each
// decoded event. Lines that are not JSON are skipped rather than aborting the
// stream, because Codex interleaves plain-text warnings on stdout.
func scanEvents(r io.Reader, handle func(Event)) error {
	scanner := bufio.NewScanner(r)
	// A single event can carry a large command output blob.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		handle(ev)
	}
	return scanner.Err()
}

// eventErrorText extracts a human-readable failure from a JSONL event.
// Codex often puts the real API error on stdout as type=error / turn.failed
// and leaves stderr empty, so callers must read this rather than Wait().
func eventErrorText(ev Event) string {
	switch ev.Type {
	case EventError:
		return flattenCodexError(ev.Message)
	case EventTurnFailed:
		if ev.Error != nil {
			return flattenCodexError(ev.Error.Message)
		}
		return flattenCodexError(ev.Message)
	case EventItemCompleted, EventItemStarted:
		if ev.Item != nil && ev.Item.Type == ItemError {
			return flattenCodexError(ev.Item.Message)
		}
	}
	return ""
}

// flattenCodexError unwraps the nested JSON envelope Codex embeds in
// error.message, e.g. `{"error":{"message":"Unsupported value: ..."}}`.
func flattenCodexError(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "{") {
		return raw
	}
	var envelope struct {
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return raw
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		msg := envelope.Error.Message
		if envelope.Error.Param != "" {
			return msg + " (" + envelope.Error.Param + ")"
		}
		return msg
	}
	if envelope.Message != "" && envelope.Message != raw {
		return flattenCodexError(envelope.Message)
	}
	return raw
}

// failureDetail prefers JSONL / stderr text over a bare "exit status 1".
func failureDetail(waitErr error, stderr string, streamErrs []string) string {
	stream := uniqueJoin(streamErrs)
	detail := strings.TrimSpace(stderr)
	switch {
	case stream != "" && detail != "":
		return stream + "; " + detail
	case stream != "":
		return stream
	case detail != "":
		return detail
	case waitErr != nil:
		return waitErr.Error()
	default:
		return ""
	}
}

func uniqueJoin(parts []string) string {
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return strings.Join(out, "; ")
}
