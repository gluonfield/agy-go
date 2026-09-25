package agy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

type cliSession struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	process        *cliProcess
	options        sessionOptions
	conversationID string
}

type sessionOptions struct {
	cwd, model, effort string
	plan, allowAll     bool
	timeout            time.Duration
}

type cliProcess struct {
	stdin  io.WriteCloser
	events chan StreamEvent
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func (c *CLIClient) CloseSession(id string) {
	c.mu.Lock()
	session := c.sessions[id]
	delete(c.sessions, id)
	c.mu.Unlock()
	if session != nil {
		session.cancel()
	}
}

func (c *CLIClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, session := range c.sessions {
		session.cancel()
		delete(c.sessions, id)
	}
	return nil
}

func (c *CLIClient) prompt(ctx context.Context, req ChatRequest, cwd, conversationID string) (PrintResult, error) {
	c.mu.Lock()
	if c.sessions == nil {
		c.sessions = make(map[string]*cliSession)
	}
	session := c.sessions[req.SessionID]
	if session == nil || req.SessionID == "" {
		session = &cliSession{}
		session.ctx, session.cancel = context.WithCancel(context.Background())
		if req.SessionID != "" {
			c.sessions[req.SessionID] = session
		}
	}
	c.mu.Unlock()
	if req.SessionID == "" {
		defer session.cancel()
	}
	if !session.mu.TryLock() {
		return PrintResult{}, fmt.Errorf("session already has an active turn")
	}
	defer session.mu.Unlock()
	if conversationID != "" && session.conversationID != conversationID {
		if session.process != nil {
			session.process.cancel()
			session.process = nil
		}
		session.conversationID = conversationID
	}
	if session.process != nil {
		select {
		case <-session.process.done:
			session.process = nil
		default:
		}
	}
	options := sessionOptions{cwd: cwd, model: req.Model, effort: req.Effort, plan: req.Plan, allowAll: req.DangerouslySkipPermissions, timeout: req.Timeout}
	if session.process != nil && session.options != options {
		session.process.cancel()
		session.process = nil
	}
	if session.process == nil {
		process, err := c.startSession(session.ctx, options, session.conversationID)
		if err != nil {
			return PrintResult{}, err
		}
		session.process = process
		session.options = options
	}
	process := session.process
	turnCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		turnCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	stopOnCancel := context.AfterFunc(turnCtx, process.cancel)
	defer stopOnCancel()
	input := struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}{Event: "user"}
	input.Message.Content = req.Message
	if err := json.NewEncoder(process.stdin).Encode(input); err != nil {
		process.cancel()
		session.process = nil
		return PrintResult{}, fmt.Errorf("write agy prompt: %w", err)
	}
	for event := range process.events {
		id := event.ConversationID
		if event.Result != nil && event.Result.ConversationID != "" {
			id = event.Result.ConversationID
		}
		if id != "" && (id != session.conversationID || event.Result != nil) {
			if c.Store != nil && req.SessionID != "" {
				if err := c.Store.Put(Session{ID: req.SessionID, Cwd: cwd, ConversationID: id, UpdatedAt: time.Now().UTC()}); err != nil {
					process.cancel()
					session.process = nil
					return PrintResult{}, err
				}
			}
			session.conversationID = id
		}
		if req.OnEvent != nil {
			req.OnEvent(event)
		}
		if event.Result != nil {
			result := *event.Result
			if result.ConversationID == "" {
				result.ConversationID = session.conversationID
			}
			if result.Status != "" && result.Status != "SUCCESS" {
				process.cancel()
				session.process = nil
				if result.Status == "CANCELED" || result.Status == "INTERRUPTED" {
					return PrintResult{}, context.Canceled
				}
				message := result.Error
				if message == "" {
					message = result.Response
				}
				return PrintResult{}, fmt.Errorf("agy turn %s: %s", result.Status, message)
			}
			return result, nil
		}
	}
	session.process = nil
	if turnCtx.Err() != nil {
		return PrintResult{}, turnCtx.Err()
	}
	if process.err != nil {
		return PrintResult{}, process.err
	}
	return PrintResult{}, fmt.Errorf("agy ended before reporting a result")
}

func (c *CLIClient) startSession(ctx context.Context, options sessionOptions, conversationID string) (*cliProcess, error) {
	args := []string{"--input-format", "stream-json", "--output-format", "stream-json"}
	if options.timeout > 0 {
		args = append(args, "--print-timeout", options.timeout.String())
	}
	if conversationID != "" {
		args = append(args, "--conversation", conversationID)
	} else {
		args = append(args, "--new-project")
	}
	if options.model != "" {
		args = append(args, "--model", options.model)
	}
	if options.effort != "" {
		args = append(args, "--effort", options.effort)
	}
	if options.plan {
		args = append(args, "--mode", "plan")
	}
	if options.allowAll {
		args = append(args, "--dangerously-skip-permissions")
	}
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, c.Binary, args...)
	cmd.Dir = options.cwd
	if c.NoBrowser {
		cmd.Env = noBrowserEnv()
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		stdin.Close()
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		stdin.Close()
		stdout.Close()
		return nil, err
	}
	process := &cliProcess{stdin: stdin, events: make(chan StreamEvent, 32), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(process.events)
		defer close(process.done)
		defer cancel()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(nil, maxOutputLineBytes)
		for scanner.Scan() {
			event, ok := DecodeStreamEvent(scanner.Bytes())
			if !ok {
				continue
			}
			select {
			case process.events <- event:
			case <-runCtx.Done():
			}
		}
		if scanner.Err() != nil {
			cancel()
		}
		process.err = cmd.Wait()
		if scanner.Err() != nil {
			process.err = scanner.Err()
		} else if process.err != nil && stderr.Len() > 0 {
			process.err = fmt.Errorf("%w: %s", process.err, stderr.String())
		}
	}()
	return process, nil
}
