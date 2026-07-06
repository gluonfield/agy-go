package agy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultTimeout = 5 * time.Minute

type CLIClient struct {
	Binary string
	Store  *Store
}

func NewCLIClient(binary string, store *Store) *CLIClient {
	if strings.TrimSpace(binary) == "" {
		binary = "agy"
	}
	return &CLIClient{Binary: binary, Store: store}
}

func (c *CLIClient) AuthStatus(ctx context.Context) (AuthStatus, error) {
	models, err := c.ListModels(ctx)
	if err == nil {
		return AuthStatus{Authenticated: true, Method: "oauth", Models: models}, nil
	}
	if isNotLoggedIn(err) {
		return AuthStatus{Authenticated: false, Method: "oauth", Reason: "not logged into Antigravity"}, nil
	}
	return AuthStatus{}, err
}

func (c *CLIClient) ListModels(ctx context.Context) ([]Model, error) {
	out, err := c.run(ctx, "", 30*time.Second, "models")
	if err != nil {
		return nil, err
	}
	return ParseModels(out), nil
}

func (c *CLIClient) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if strings.TrimSpace(req.Message) == "" {
		return ChatResponse{}, errors.New("message is required")
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return ChatResponse{}, err
		}
	}

	unlock, err := c.lockCWD(ctx, cwd)
	if err != nil {
		return ChatResponse{}, err
	}
	defer unlock()

	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" && c.Store != nil && strings.TrimSpace(req.SessionID) != "" {
		if stored, ok, err := c.Store.Get(req.SessionID); err != nil {
			return ChatResponse{}, err
		} else if ok {
			conversationID = stored.ConversationID
		}
	}

	message := req.Message
	if req.SystemInstructions != "" {
		message = "System instructions:\n" + req.SystemInstructions + "\n\nUser request:\n" + message
	}
	if req.Plan && !strings.HasPrefix(strings.TrimSpace(message), "/plan") {
		message = "/plan " + message
	}

	args := []string{"--print-timeout", timeoutArg(req.Timeout), "--print", message}
	if conversationID != "" {
		args = append([]string{"--conversation", conversationID}, args...)
	} else {
		args = append([]string{"--new-project"}, args...)
	}
	if req.Model != "" {
		args = append([]string{"--model", req.Model}, args...)
	}
	if req.DangerouslySkipPermissions {
		args = append([]string{"--dangerously-skip-permissions"}, args...)
	}

	out, err := c.run(ctx, cwd, req.Timeout, args...)
	if err != nil {
		return ChatResponse{}, err
	}

	nextConversationID := conversationID
	if c.Store != nil {
		if id, err := c.Store.LastConversationForCwd(cwd); err == nil && strings.TrimSpace(id) != "" {
			nextConversationID = id
		}
		if strings.TrimSpace(req.SessionID) != "" {
			if err := c.Store.Put(Session{
				ID:             req.SessionID,
				Cwd:            cwd,
				ConversationID: nextConversationID,
				UpdatedAt:      time.Now().UTC(),
			}); err != nil {
				return ChatResponse{}, err
			}
		}
	}

	resp := ChatResponse{Text: out, ConversationID: nextConversationID}
	if path := PlanPath(out); path != "" {
		resp.PlanPath = path
		if data, err := os.ReadFile(path); err == nil {
			resp.PlanText = string(data)
		}
	}
	return resp, nil
}

func (c *CLIClient) run(ctx context.Context, cwd string, timeout time.Duration, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, c.Binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	errText := strings.TrimSpace(stderr.String())
	if runCtx.Err() != nil {
		return text, runCtx.Err()
	}
	if err != nil {
		if errText != "" {
			return text, fmt.Errorf("%w: %s", err, errText)
		}
		return text, err
	}
	return text, nil
}

func (c *CLIClient) lockCWD(ctx context.Context, cwd string) (func(), error) {
	if c.Store == nil {
		return func() {}, nil
	}
	return c.Store.LockCWD(ctx, cwd)
}

func timeoutArg(timeout time.Duration) string {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout%time.Second == 0 {
		return fmt.Sprintf("%ds", int(timeout/time.Second))
	}
	return timeout.String()
}

func isNotLoggedIn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not logged into antigravity") ||
		strings.Contains(msg, "not logged in")
}

func DefaultStore() (*Store, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(dir, "agy-go"))
}
