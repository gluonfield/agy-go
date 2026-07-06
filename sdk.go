package agy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type SDKClient struct {
	Python string
	APIKey string
	Store  *Store
}

func NewSDKClient(python, apiKey string, store *Store) *SDKClient {
	if strings.TrimSpace(python) == "" {
		python = "python3"
	}
	return &SDKClient{Python: python, APIKey: apiKey, Store: store}
}

func (c *SDKClient) AuthStatus(ctx context.Context) (AuthStatus, error) {
	if strings.TrimSpace(c.APIKey) == "" && strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) == "" {
		return AuthStatus{Authenticated: false, Method: "api_key", Reason: "GEMINI_API_KEY is not set"}, nil
	}
	if _, err := c.run(ctx, sdkRunRequest{Message: "Reply exactly: OK", TimeoutSeconds: 30}); err != nil {
		return AuthStatus{}, err
	}
	return AuthStatus{
		Authenticated: true,
		Method:        "api_key",
		Models: []Model{
			{Name: "gemini-3.5-flash"},
			{Name: "gemini-2.5-pro"},
			{Name: "gemini-2.5-flash"},
		},
	}, nil
}

func (c *SDKClient) ListModels(context.Context) ([]Model, error) {
	return []Model{
		{Name: "gemini-3.5-flash"},
		{Name: "gemini-2.5-pro"},
		{Name: "gemini-2.5-flash"},
	}, nil
}

func (c *SDKClient) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
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

	sessionID := strings.TrimSpace(req.SessionID)
	conversationID := strings.TrimSpace(req.ConversationID)
	saveDir := ""
	if c.Store != nil && sessionID != "" {
		if stored, ok, err := c.Store.Get(sessionID); err != nil {
			return ChatResponse{}, err
		} else if ok {
			if conversationID == "" {
				conversationID = stored.ConversationID
			}
			saveDir = stored.SaveDir
		}
		if saveDir == "" {
			var err error
			saveDir, err = c.Store.SessionDir(sessionID)
			if err != nil {
				return ChatResponse{}, err
			}
		}
	}

	out, err := c.run(ctx, sdkRunRequest{
		Cwd:                        cwd,
		SaveDir:                    saveDir,
		AppDataDir:                 saveDir,
		ConversationID:             conversationID,
		Message:                    req.Message,
		Model:                      req.Model,
		APIKey:                     c.APIKey,
		SystemInstructions:         req.SystemInstructions,
		Plan:                       req.Plan,
		DangerouslySkipPermissions: req.DangerouslySkipPermissions,
		TimeoutSeconds:             int(timeout(req.Timeout).Seconds()),
	})
	if err != nil {
		return ChatResponse{}, err
	}
	resp := ChatResponse{
		Text:           strings.TrimSpace(out.Text),
		ConversationID: strings.TrimSpace(out.ConversationID),
		PlanPath:       strings.TrimSpace(out.PlanPath),
		PlanText:       out.PlanText,
	}
	if c.Store != nil && sessionID != "" {
		_ = c.Store.Put(Session{
			ID:             sessionID,
			Cwd:            cwd,
			ConversationID: resp.ConversationID,
			SaveDir:        saveDir,
			UpdatedAt:      time.Now().UTC(),
		})
	}
	return resp, nil
}

type sdkRunRequest struct {
	Cwd                        string `json:"cwd,omitempty"`
	SaveDir                    string `json:"save_dir,omitempty"`
	AppDataDir                 string `json:"app_data_dir,omitempty"`
	ConversationID             string `json:"conversation_id,omitempty"`
	Message                    string `json:"message"`
	Model                      string `json:"model,omitempty"`
	APIKey                     string `json:"api_key,omitempty"`
	SystemInstructions         string `json:"system_instructions,omitempty"`
	Plan                       bool   `json:"plan,omitempty"`
	DangerouslySkipPermissions bool   `json:"allow_all,omitempty"`
	TimeoutSeconds             int    `json:"timeout_seconds,omitempty"`
}

type sdkRunResponse struct {
	Text           string `json:"text"`
	ConversationID string `json:"conversation_id,omitempty"`
	PlanPath       string `json:"plan_path,omitempty"`
	PlanText       string `json:"plan_text,omitempty"`
}

func (c *SDKClient) run(ctx context.Context, req sdkRunRequest) (sdkRunResponse, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout(time.Duration(req.TimeoutSeconds)*time.Second))
	defer cancel()

	body, err := json.Marshal(req)
	if err != nil {
		return sdkRunResponse{}, err
	}
	cmd := exec.CommandContext(runCtx, c.Python, "-c", sdkRunner)
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = os.Environ()
	if strings.TrimSpace(c.APIKey) != "" {
		cmd.Env = append(cmd.Env, "GEMINI_API_KEY="+c.APIKey)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return sdkRunResponse{}, runCtx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return sdkRunResponse{}, fmt.Errorf("%w: %s", err, msg)
		}
		return sdkRunResponse{}, err
	}
	var out sdkRunResponse
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return sdkRunResponse{}, fmt.Errorf("decode SDK response: %w: %s", err, strings.TrimSpace(stdout.String()))
	}
	return out, nil
}

func timeout(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultTimeout
	}
	return d
}

const sdkRunner = `
import asyncio
import json
import os
import sys

async def main():
    req = json.load(sys.stdin)
    from google import antigravity
    from google.antigravity import types
    from google.antigravity.hooks import policy

    cwd = req.get("cwd") or ""
    if cwd:
        os.chdir(cwd)
    save_dir = req.get("save_dir") or None
    if save_dir:
        os.makedirs(save_dir, exist_ok=True)
    kwargs = {
        "workspaces": [cwd] if cwd else None,
        "save_dir": save_dir,
        "app_data_dir": req.get("app_data_dir") or save_dir,
        "conversation_id": req.get("conversation_id") or None,
        "system_instructions": req.get("system_instructions") or None,
        "model": req.get("model") or None,
        "api_key": req.get("api_key") or None,
    }
    if req.get("allow_all"):
        kwargs["policies"] = [policy.allow_all()]
        kwargs["capabilities"] = antigravity.CapabilitiesConfig()
    kwargs = {k: v for k, v in kwargs.items() if v is not None}

    async with antigravity.Agent(antigravity.LocalAgentConfig(**kwargs)) as agent:
        prompt = req.get("message") or ""
        if req.get("plan"):
            prompt = [types.SlashCommand(name=types.BuiltinSlashCommandName.PLAN), prompt]
        response = await agent.chat(prompt)
        text = await response.text()
        plan_path = ""
        plan_text = ""
        if req.get("plan"):
            path_response = await agent.chat(
                "What is the absolute path of the implementation plan artifact you just created? "
                "Return only the absolute path as plain text."
            )
            plan_path = (await path_response.text()).strip().strip(chr(96)).strip("\"").strip("'")
            if os.path.exists(plan_path):
                with open(plan_path, "r", encoding="utf-8") as f:
                    plan_text = f.read()
        print(json.dumps({
            "text": text,
            "conversation_id": agent.conversation_id or "",
            "plan_path": plan_path,
            "plan_text": plan_text,
        }))

asyncio.run(main())
`

func SDKPackageInstalled(python string) bool {
	if strings.TrimSpace(python) == "" {
		python = "python3"
	}
	cmd := exec.Command(python, "-c", "import google.antigravity")
	return cmd.Run() == nil
}
