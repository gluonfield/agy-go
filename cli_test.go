package agy

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestParseModels(t *testing.T) {
	got := ParseModels("Gemini 3.5 Flash (High)\n\nClaude Sonnet 4.6 (Thinking)\n")
	if len(got) != 2 || got[0].Name != "Gemini 3.5 Flash (High)" || got[1].Name != "Claude Sonnet 4.6 (Thinking)" {
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
  printf 'Gemini 3.5 Flash (High)\n'
  exit 0
fi
exit 1
`)
	client := NewCLIClient(agy, nil)
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Name != "Gemini 3.5 Flash (High)" {
		t.Fatalf("models = %#v", models)
	}
}

func TestCLIClientChatStoresSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake is unix-only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	cache := filepath.Join(home, ".gemini", "antigravity-cli", "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	agy := fakeAgy(t, `#!/bin/sh
printf '{"%s":"conv-1"}' "$PWD" > "$HOME/.gemini/antigravity-cli/cache/last_conversations.json"
printf 'hello\n'
`)
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := NewCLIClient(agy, store)
	resp, err := client.Chat(context.Background(), ChatRequest{
		SessionID: "session-1",
		Cwd:       cwd,
		Message:   "hi",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello" || resp.ConversationID != "conv-1" {
		t.Fatalf("resp = %#v", resp)
	}
	session, ok, err := store.Get("session-1")
	if err != nil || !ok || session.ConversationID != "conv-1" {
		t.Fatalf("stored = %#v ok=%v err=%v", session, ok, err)
	}
}

func TestCLIClientSerializesSameCWDConversationCapture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake is unix-only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli", "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	agy := fakeAgy(t, `#!/bin/sh
guard="$PWD/inflight"
if [ -e "$guard" ]; then
  echo overlap >&2
  exit 2
fi
touch "$guard"
trap 'rm -f "$guard"' EXIT
count_file="$PWD/count"
n=0
if [ -f "$count_file" ]; then n=$(cat "$count_file"); fi
n=$((n + 1))
printf '%s' "$n" > "$count_file"
sleep 0.1
printf '{"%s":"conv-%s"}' "$PWD" "$n" > "$HOME/.gemini/antigravity-cli/cache/last_conversations.json"
printf 'hello-%s\n' "$n"
`)
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := NewCLIClient(agy, store)

	type result struct {
		session string
		resp    ChatResponse
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, sessionID := range []string{"session-a", "session-b"} {
		sessionID := sessionID
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Chat(context.Background(), ChatRequest{
				SessionID: sessionID,
				Cwd:       cwd,
				Message:   "hi",
				Timeout:   time.Second,
			})
			results <- result{session: sessionID, resp: resp, err: err}
		}()
	}
	wg.Wait()
	close(results)

	seen := map[string]bool{}
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.resp.ConversationID == "" || got.resp.Text == "" {
			t.Fatalf("empty response: %#v", got.resp)
		}
		if seen[got.resp.ConversationID] {
			t.Fatalf("conversation reused: %#v", got.resp)
		}
		seen[got.resp.ConversationID] = true
		session, ok, err := store.Get(got.session)
		if err != nil || !ok || session.ConversationID != got.resp.ConversationID {
			t.Fatalf("stored %s = %#v ok=%v err=%v response=%#v", got.session, session, ok, err, got.resp)
		}
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
