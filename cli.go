package agy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultTimeout = 5 * time.Minute

// The CLI writes a turn's whole response on one stream-json line.
const maxOutputLineBytes = 8 << 20

type CLIClient struct {
	Binary string
	Store  *Store
	// NoBrowser blocks the CLI's interactive browser OAuth fallback so
	// headless runs fail fast with a sign-in error instead.
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
	if IsSignedOut(err) {
		return AuthStatus{Authenticated: false, Method: "oauth", Reason: "not logged into Antigravity"}, nil
	}
	return AuthStatus{}, err
}

func (c *CLIClient) ListModels(ctx context.Context) ([]Model, error) {
	out, err := c.run(ctx, "", 30*time.Second, nil, "models")
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

	message := req.Message
	if req.SystemInstructions != "" {
		message = "System instructions:\n" + req.SystemInstructions + "\n\nUser request:\n" + message
	}
	if req.Plan && !strings.HasPrefix(strings.TrimSpace(message), "/plan") {
		message = "/plan " + message
	}

	args := []string{"--output-format", "stream-json", "--print-timeout", timeoutArg(req.Timeout), "--print", message}
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

	var result PrintResult
	if _, err := c.run(ctx, cwd, req.Timeout, func(line []byte) {
		event, ok := DecodeStreamEvent(line)
		if !ok {
			return
		}
		if event.Result != nil {
			result = *event.Result
		}
		if req.OnEvent != nil {
			req.OnEvent(event)
		}
	}, args...); err != nil {
		return ChatResponse{}, err
	}
	if result.ConversationID == "" && result.Response == "" {
		return ChatResponse{}, errors.New("agy reported no result for this turn")
	}

	nextConversationID := strings.TrimSpace(result.ConversationID)
	if nextConversationID == "" {
		nextConversationID = conversationID
	}
	if c.Store != nil && strings.TrimSpace(req.SessionID) != "" {
		if err := c.Store.Put(Session{
			ID:             req.SessionID,
			Cwd:            cwd,
			ConversationID: nextConversationID,
			UpdatedAt:      time.Now().UTC(),
		}); err != nil {
			return ChatResponse{}, err
		}
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

// run executes the CLI and returns its stdout. onLine, when set, receives each
// stdout line as it arrives; the slice it is handed is only valid for the
// duration of the call.
func (c *CLIClient) run(ctx context.Context, cwd string, timeout time.Duration, onLine func([]byte), args ...string) (string, error) {
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}

	var out bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(nil, maxOutputLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		out.Write(line)
		out.WriteByte('\n')
		if onLine != nil {
			onLine(line)
		}
	}
	scanErr := scanner.Err()

	err = cmd.Wait()
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
	if scanErr != nil {
		return text, fmt.Errorf("read agy output: %w", scanErr)
	}
	return text, nil
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
