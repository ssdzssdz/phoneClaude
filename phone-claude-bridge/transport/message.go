package transport

import (
	"encoding/json"
	"time"
)

type MessageEnvelope struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`    // chat | control | file | session | system
	TS      int64           `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

type ChatPayload struct {
	Action      string  `json:"action"`       // send | delta | done | error
	SessionID   string  `json:"session_id"`
	Message     string  `json:"message,omitempty"`
	Content     string  `json:"content,omitempty"`
	DeltaIndex  int     `json:"delta_index,omitempty"`
	TotalTokens int     `json:"total_tokens,omitempty"`
	UsageCost   float64 `json:"usage_cost,omitempty"`
}

type ControlPayload struct {
	Action    string `json:"action"`     // interrupt | status
	SessionID string `json:"session_id"`
	Status    string `json:"status,omitempty"`
}

type FilePayload struct {
	Action    string      `json:"action"`   // autocomplete | autocomplete_result | list | list_result
	SessionID string      `json:"session_id,omitempty"`
	Prefix    string      `json:"prefix,omitempty"`
	Path      string      `json:"path,omitempty"`
	Matches   []string    `json:"matches,omitempty"`
	Entries   []FileEntry `json:"entries,omitempty"`
}

type FileEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

type SystemPayload struct {
	Action    string `json:"action"`     // ping | pong | notify
	Level     string `json:"level,omitempty"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type SessionPayload struct {
	Action    string `json:"action"`     // switch | info | list
	SessionID string `json:"session_id,omitempty"`
	Name      string `json:"name,omitempty"`
	WorkDir   string `json:"work_dir,omitempty"`
}

func NewEnvelope(id, msgType string, payload interface{}) (*MessageEnvelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &MessageEnvelope{
		ID:      id,
		Type:    msgType,
		TS:      time.Now().Unix(),
		Payload: data,
	}, nil
}

func (m *MessageEnvelope) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

func UnmarshalEnvelope(data []byte) (*MessageEnvelope, error) {
	var m MessageEnvelope
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
