package agy

import (
	"context"
	"time"
)

type Agent interface {
	AuthStatus(context.Context) (AuthStatus, error)
	ListModels(context.Context) ([]Model, error)
	Chat(context.Context, ChatRequest) (ChatResponse, error)
}

type AuthStatus struct {
	Authenticated bool
	Method        string
	Reason        string
	Models        []Model
}

// Model mirrors one row of `agy models`: a stable selection ID and the
// display name. Both are accepted by the CLI's --model flag.
type Model struct {
	ID   string
	Name string
}

type ChatRequest struct {
	SessionID                  string
	Cwd                        string
	ConversationID             string
	Message                    string
	Model                      string
	Effort                     string
	Plan                       bool
	DangerouslySkipPermissions bool
	// Timeout bounds the turn; zero lets it run until the CLI reports a
	// result, as the CLI itself does.
	Timeout time.Duration
	// OnEvent, when set, receives the CLI's step events as the turn runs.
	OnEvent func(StreamEvent)
}

type ChatResponse struct {
	Text           string
	ConversationID string
	PlanPath       string
	PlanText       string
	Usage          Usage
}

// Usage carries the CLI's token counters verbatim. They are cumulative over
// the whole conversation rather than per turn, and total_tokens is input plus
// output. CacheReadTokens is reported on its own terms: it can exceed
// InputTokens, so it is not a share of the input.
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ThinkingTokens  int64 `json:"thinking_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

func (u Usage) IsZero() bool { return u == Usage{} }

// Sub reports what changed since an earlier cumulative reading, clamped at
// zero so a conversation whose counters reset cannot report negative usage.
func (u Usage) Sub(prev Usage) Usage {
	return Usage{
		InputTokens:     nonNegative(u.InputTokens - prev.InputTokens),
		OutputTokens:    nonNegative(u.OutputTokens - prev.OutputTokens),
		ThinkingTokens:  nonNegative(u.ThinkingTokens - prev.ThinkingTokens),
		CacheReadTokens: nonNegative(u.CacheReadTokens - prev.CacheReadTokens),
		TotalTokens:     nonNegative(u.TotalTokens - prev.TotalTokens),
	}
}

func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
