package agy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const defaultTimeout = 5 * time.Minute

type CLIClient struct {
	Binary string
	Store  *Store
	// NoBrowser keeps headless runs headless: the Antigravity CLI opens a
	// browser OAuth flow when its silent auth fails, then waits for a pasted
	// code that a server host can never provide. Shadowing the URL openers on
	// PATH makes it fail fast with an auth error instead.
	NoBrowser bool
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
	if c.NoBrowser {
		cmd.Env = noBrowserEnv()
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

var noBrowser struct {
	once sync.Once
	dir  string
}

// noBrowserEnv prepends a directory of no-op URL openers (open, xdg-open) to
// PATH so the Antigravity CLI cannot launch a browser. Returns the inherited
// environment unchanged if the shim cannot be created (or on Windows, where
// the CLI opens URLs without consulting PATH).
func noBrowserEnv() []string {
	env := os.Environ()
	if runtime.GOOS == "windows" {
		return env
	}
	noBrowser.once.Do(func() {
		dir, err := os.MkdirTemp("", "agy-go-no-browser-")
		if err != nil {
			return
		}
		for _, name := range []string{"open", "xdg-open"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				return
			}
		}
		noBrowser.dir = dir
	})
	if noBrowser.dir == "" {
		return env
	}
	return append(env, "PATH="+noBrowser.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
