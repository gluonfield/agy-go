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

type Model struct {
	Name string
}

type ChatRequest struct {
	SessionID                  string
	Cwd                        string
	ConversationID             string
	Message                    string
	Model                      string
	SystemInstructions         string
	Plan                       bool
	DangerouslySkipPermissions bool
	Timeout                    time.Duration
}

type ChatResponse struct {
	Text           string
	ConversationID string
	PlanPath       string
	PlanText       string
}
