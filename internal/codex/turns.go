package codex

import (
	"encoding/json"
	"io"
	"time"
)

// TurnUsage is one token_usage_record joined with the model from that turn's
// turn_context. Model is "unknown" when no context was recorded.
type TurnUsage struct {
	TurnID    string    `json:"turn_id"`
	Model     string    `json:"model"`
	Timestamp time.Time `json:"timestamp"`
	Usage     Usage     `json:"usage"`
}

const unknownModel = "unknown"

// ParseSessionTurns reads a Codex rollout jsonl and returns every usage
// record. A token_usage_record is paired with the latest turn_context for the
// same turn_id; if that is missing, the most recent model on the file is used.
func ParseSessionTurns(r io.Reader) ([]TurnUsage, error) {
	dec := json.NewDecoder(r)
	models := map[string]string{}
	lastModel := ""
	var out []TurnUsage
	for {
		var line struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := dec.Decode(&line); err != nil {
			if err == io.EOF {
				break
			}
			return out, err
		}
		switch line.Type {
		case "turn_context":
			if settings := settingsFromTurnContext(line.Payload); settings != nil && settings.Model != "" {
				lastModel = settings.Model
				var payload struct {
					TurnID string `json:"turn_id"`
				}
				if json.Unmarshal(line.Payload, &payload) == nil && payload.TurnID != "" {
					models[payload.TurnID] = settings.Model
				}
			}
		case "token_usage_record":
			var payload struct {
				TurnID string `json:"turn_id"`
				Usage  *Usage `json:"usage"`
			}
			if err := json.Unmarshal(line.Payload, &payload); err != nil || payload.Usage == nil {
				continue
			}
			model := models[payload.TurnID]
			if model == "" {
				model = lastModel
			}
			if model == "" {
				model = unknownModel
			}
			out = append(out, TurnUsage{
				TurnID:    payload.TurnID,
				Model:     model,
				Timestamp: parseSessionTimestamp(line.Timestamp),
				Usage:     *payload.Usage,
			})
		}
	}
	return out, nil
}

func parseSessionTimestamp(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}
