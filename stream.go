package agy

import "encoding/json"

// Step states and types reported by `agy --output-format stream-json`.
const (
	StepStateActive = "ACTIVE"
	StepStateDone   = "DONE"

	StepTypeTool = "tool"
)

// StreamEvent is one line of the CLI's stream-json output. Exactly one payload
// is set; the final "result" line carries the same envelope the non-streaming
// JSON format returns.
type StreamEvent struct {
	Event      string       `json:"event"`
	StepUpdate *StepUpdate  `json:"step_update"`
	Result     *PrintResult `json:"result"`
}

// StepUpdate reports one step of the CLI's work. Step indices are unique
// within a conversation and keep counting across turns.
type StepUpdate struct {
	StepIndex int       `json:"step_index"`
	State     string    `json:"state"`
	StepType  string    `json:"step_type"`
	ToolName  string    `json:"tool_name"`
	ToolInfo  *ToolInfo `json:"tool_info"`
	TextDelta string    `json:"text_delta"`
}

type ToolInfo struct {
	Name       string         `json:"name"`
	Parameters map[string]any `json:"parameters"`
	Output     string         `json:"output"`
}

// DecodeStreamEvent reads one stream-json line. Lines that are not events the
// CLI documents decode to a zero event, which callers ignore.
func DecodeStreamEvent(line []byte) (StreamEvent, bool) {
	var event StreamEvent
	if err := json.Unmarshal(line, &event); err != nil || event.Event == "" {
		return StreamEvent{}, false
	}
	return event, true
}
