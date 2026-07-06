package agy

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestSDKClientChatReturnsStorePutError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake is unix-only")
	}
	python := fakeAgy(t, `#!/bin/sh
chmod 500 "$AGY_STORE_DIR"
printf '{"text":"ok","conversation_id":"conv-1"}'
`)
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(store.Dir, 0o700) }()
	t.Setenv("AGY_STORE_DIR", store.Dir)
	client := NewSDKClient(python, "key", store)
	_, err = client.Chat(context.Background(), ChatRequest{
		SessionID: "session-1",
		Cwd:       t.TempDir(),
		Message:   "hi",
		Timeout:   time.Second,
	})
	if err == nil {
		t.Fatal("expected store error")
	}
}
