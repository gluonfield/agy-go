package agy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseModels(t *testing.T) {
	got := ParseModels("gemini-3.6-flash-high\tGemini 3.6 Flash (High)\n\nclaude-sonnet-4-6\tClaude Sonnet 4.6 (Thinking)\n")
	want := []Model{
		{ID: "gemini-3.6-flash-high", Name: "Gemini 3.6 Flash (High)"},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6 (Thinking)"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
}

func TestParseModelsWithoutDisplayColumn(t *testing.T) {
	got := ParseModels("Gemini 3.6 Flash (High)\n")
	if len(got) != 1 || got[0] != (Model{ID: "Gemini 3.6 Flash (High)", Name: "Gemini 3.6 Flash (High)"}) {
		t.Fatalf("models = %#v", got)
	}
}

func TestPlanPath(t *testing.T) {
	got := PlanPath("created [plan.md](file:///tmp/agy%20plan/plan.md)")
	if got != "/tmp/agy plan/plan.md" {
		t.Fatalf("path = %q", got)
	}
}

func TestCLIClientListModels(t *testing.T) {
	agy := fakeAgy(t, `#!/bin/sh
if [ "$1" = "models" ]; then
  printf 'gemini-3.6-flash-high\tGemini 3.6 Flash (High)\n'
  exit 0
fi
exit 1
`)
	client := NewCLIClient(agy, nil)
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0] != (Model{ID: "gemini-3.6-flash-high", Name: "Gemini 3.6 Flash (High)"}) {
		t.Fatalf("models = %#v", models)
	}
}

func TestCLIClientChatReadsStreamedTurn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake is unix-only")
	}
	agy := fakeAgy(t, `#!/bin/sh
printf '{"event":"step_update","step_update":{"step_index":3,"state":"ACTIVE","step_type":"tool","tool_name":"view_file","tool_info":{"name":"view_file","parameters":{"AbsolutePath":"/tmp/a"}}}}\n'
printf '{"event":"result","result":{"conversation_id":"conv-1","status":"SUCCESS","response":"hello","usage":{"input_tokens":8851,"output_tokens":67,"thinking_tokens":62,"cache_read_tokens":8141,"total_tokens":8918}}}\n'
`)
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := NewCLIClient(agy, store)
	var steps []StepUpdate
	resp, err := client.Chat(context.Background(), ChatRequest{
		SessionID: "session-1",
		Cwd:       t.TempDir(),
		Message:   "hi",
		Timeout:   time.Second,
		OnEvent: func(event StreamEvent) {
			if event.StepUpdate != nil {
				steps = append(steps, *event.StepUpdate)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello" || resp.ConversationID != "conv-1" {
		t.Fatalf("resp = %#v", resp)
	}
	want := Usage{InputTokens: 8851, OutputTokens: 67, ThinkingTokens: 62, CacheReadTokens: 8141, TotalTokens: 8918}
	if resp.Usage != want {
		t.Fatalf("usage = %#v, want %#v", resp.Usage, want)
	}
	session, ok, err := store.Get("session-1")
	if err != nil || !ok || session.ConversationID != "conv-1" {
		t.Fatalf("stored = %#v ok=%v err=%v", session, ok, err)
	}
	if len(steps) != 1 || steps[0].ToolName != "view_file" || steps[0].State != StepStateActive {
		t.Fatalf("steps = %#v", steps)
	}
	if steps[0].ToolInfo == nil || steps[0].ToolInfo.Parameters["AbsolutePath"] != "/tmp/a" {
		t.Fatalf("tool info = %#v", steps[0].ToolInfo)
	}
}

// The CLI reports the conversation it used in its own response, so concurrent
// sessions sharing a working directory no longer read each other's state.
func TestCLIClientConcurrentSameCWDSessionsKeepOwnConversations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake is unix-only")
	}
	cwd := t.TempDir()
	agy := fakeAgy(t, `#!/bin/sh
printf '{"event":"result","result":{"conversation_id":"conv-%s","status":"SUCCESS","response":"hello-%s"}}\n' "$$" "$$"
`)
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := NewCLIClient(agy, store)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, sessionID := range []string{"session-a", "session-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Chat(context.Background(), ChatRequest{
				SessionID: sessionID,
				Cwd:       cwd,
				Message:   "hi",
				Timeout:   time.Second,
			})
			if err != nil {
				errs <- err
				return
			}
			session, ok, storeErr := store.Get(sessionID)
			if storeErr != nil || !ok || session.ConversationID != resp.ConversationID {
				errs <- fmt.Errorf("%s stored %#v ok=%v err=%v, want %q", sessionID, session, ok, storeErr, resp.ConversationID)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, sessionID := range []string{"session-a", "session-b"} {
		session, _, _ := store.Get(sessionID)
		if seen[session.ConversationID] {
			t.Fatalf("conversation reused: %#v", session)
		}
		seen[session.ConversationID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("conversations = %#v", seen)
	}
}

func fakeAgy(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agy")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCLIClientNoBrowserShadowsURLOpeners(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake is unix-only")
	}
	agy := fakeAgy(t, `#!/bin/sh
command -v open
command -v xdg-open
open https://example.com && xdg-open https://example.com && printf 'no browser\n'
`)
	client := NewCLIClient(agy, nil)
	client.NoBrowser = true
	out, err := client.run(context.Background(), "", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 3 || lines[2] != "no browser" {
		t.Fatalf("out = %q", out)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(cache, "agy-go", "no-browser")
	for _, opener := range lines[:2] {
		if filepath.Dir(opener) != shim {
			t.Fatalf("opener %q not shadowed by %q", opener, shim)
		}
	}
}
