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

// The CLI writes a turn's whole response on one stream-json line.
const maxOutputLineBytes = 8 << 20

type CLIClient struct {
	Binary string
	Store  *Store
	// NoBrowser blocks the CLI's interactive browser OAuth fallback so
	// headless runs fail fast with a sign-in error instead.
	NoBrowser bool
	mu        sync.Mutex
	sessions  map[string]*cliSession
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
	if IsSignedOut(err) {
		return AuthStatus{Authenticated: false, Method: "oauth", Reason: "not logged into Antigravity"}, nil
	}
	return AuthStatus{}, err
}

func (c *CLIClient) ListModels(ctx context.Context) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := c.output(ctx, "models")
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

	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" && c.Store != nil && strings.TrimSpace(req.SessionID) != "" {
		if stored, ok, err := c.Store.Get(req.SessionID); err != nil {
			return ChatResponse{}, err
		} else if ok {
			conversationID = stored.ConversationID
		}
	}

	result, err := c.prompt(ctx, req, cwd, conversationID)
	if err != nil {
		return ChatResponse{}, err
	}

	nextConversationID := strings.TrimSpace(result.ConversationID)
	if nextConversationID == "" {
		nextConversationID = conversationID
	}
	resp := ChatResponse{Text: result.Response, ConversationID: nextConversationID, Usage: result.Usage}
	if path := PlanPath(result.Response); path != "" {
		resp.PlanPath = path
		if data, err := os.ReadFile(path); err == nil {
			resp.PlanText = string(data)
		}
	}
	return resp, nil
}

// output runs the CLI and returns its stdout.
func (c *CLIClient) output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	if c.NoBrowser {
		cmd.Env = noBrowserEnv()
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		if errText := strings.TrimSpace(stderr.String()); errText != "" {
			return "", fmt.Errorf("%w: %s", err, errText)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// noBrowserEnv prepends a directory of no-op URL openers (open, xdg-open) to
// PATH so the Antigravity CLI cannot launch a browser. The shim is rebuilt on
// every call so a reaped directory or transient write failure never disables
// suppression for the process lifetime. Returns the inherited environment
// unchanged if the shim cannot be created (or on Windows, where the CLI opens
// URLs without consulting PATH).
func noBrowserEnv() []string {
	env := os.Environ()
	if runtime.GOOS == "windows" {
		return env
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return env
	}
	dir := filepath.Join(cache, "agy-go", "no-browser")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return env
	}
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			return env
		}
	}
	return append(env, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// IsSignedOut reports whether err is the Antigravity CLI failing for lack of
// a signed-in Google account: "You are not logged into Antigravity", "Please
// sign in ...", or print mode's "authentication failed or timed out" after
// its interactive OAuth fallback goes unanswered.
func IsSignedOut(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not logged in") ||
		strings.Contains(msg, "please sign in") ||
		strings.Contains(msg, "authentication failed or timed out")
}

func DefaultStore() (*Store, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(dir, "agy-go"))
}
