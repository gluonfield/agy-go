package agy

import "encoding/json"

// Step states and types reported by `agy --output-format stream-json`. The
// states are the harness's StepUpdate.State enum without its STATE_ prefix.
const (
	StepStateActive         = "ACTIVE"
	StepStateDone           = "DONE"
	StepStateWaitingForUser = "WAITING_FOR_USER"
	StepStateError          = "ERROR"

	StepTypeTool     = "tool"
	StepTypeSubagent = "subagent"
)

// StreamEvent is one line of the CLI's stream-json output. Exactly one payload
// is set; the final "result" line carries the same envelope the non-streaming
// JSON format returns.
type StreamEvent struct {
	ConversationID string       `json:"conversation_id"`
	Event          string       `json:"event"`
	StepUpdate     *StepUpdate  `json:"step_update"`
	Result         *PrintResult `json:"result"`
}

// StepUpdate reports one step of the CLI's work. Step indices are unique
// within a conversation and keep counting across turns.
type StepUpdate struct {
	StepIndex    int           `json:"step_index"`
	State        string        `json:"state"`
	StepType     string        `json:"step_type"`
	ToolName     string        `json:"tool_name"`
	ToolInfo     *ToolInfo     `json:"tool_info"`
	SubagentInfo *SubagentInfo `json:"subagent_info"`
	TextDelta    string        `json:"text_delta"`
}

type ToolInfo struct {
	Name       string         `json:"name"`
	Parameters map[string]any `json:"parameters"`
	Output     string         `json:"output"`
}

// SubagentInfo describes the subagents a "subagent" step delegates to. The CLI
// folds their work into the parent's step sequence rather than reporting a
// separate trajectory, so their steps arrive as ordinary steps afterwards.
type SubagentInfo struct {
	Subagents []Subagent `json:"subagents"`
}

type Subagent struct {
	TypeName      string `json:"type_name"`
	Role          string `json:"role"`
	InitialPrompt string `json:"initial_prompt"`
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
