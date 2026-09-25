package agy

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPersistentTurnsAndCancelledProcessResume(t *testing.T) {
	binary := fakeAgy(t, `#!/bin/sh
while IFS= read -r input
do
  case "$input" in
    *stall*)
      continue
      ;;
  esac
  printf '{"event":"result","result":{"status":"SUCCESS","conversation_id":"conv-%s","response":"%s"}}\n' "$$" "$$"
done
`)
	client := NewCLIClient(binary, nil)
	t.Cleanup(func() { _ = client.Close() })
	req := ChatRequest{SessionID: "session", Cwd: t.TempDir(), Message: "first", Effort: "low", Timeout: time.Second}
	first, err := client.Chat(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Message = "second"
	second, err := client.Chat(t.Context(), req)
	if err != nil || second.Text != first.Text {
		t.Fatalf("second turn = %#v, %v; want the same native process", second, err)
	}
	req.Message = "stall"
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.Chat(ctx, req); err != context.DeadlineExceeded {
		t.Fatalf("cancelled turn error = %v", err)
	}
	req.Message = "resumed"
	req.ConversationID = first.ConversationID
	resumed, err := client.Chat(t.Context(), req)
	if err != nil || resumed.Text == first.Text {
		t.Fatalf("resumed process = %#v, %v", resumed, err)
	}
}

func TestNativeResultErrorIsNotReportedAsSuccess(t *testing.T) {
	binary := fakeAgy(t, `#!/bin/sh
read input
printf '{"event":"result","result":{"status":"ERROR","response":"Native authentication failed"}}\n'
`)
	client := NewCLIClient(binary, nil)
	_, err := client.Chat(t.Context(), ChatRequest{Cwd: t.TempDir(), Message: "hello"})
	if err == nil || !strings.Contains(err.Error(), "Native authentication failed") {
		t.Fatalf("native error = %v", err)
	}
}

func TestFirstTurnCancellationRetainsNativeConversation(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary := fakeAgy(t, `#!/bin/sh
printf '{"event":"init","conversation_id":"native-created"}\n'
while IFS= read -r input
do
  case "$input" in
    *stall*)
      continue
      ;;
  esac
  printf '{"event":"result","result":{"status":"SUCCESS","response":"%s"}}\n' "$*"
done
`)
	client := NewCLIClient(binary, store)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	req := ChatRequest{SessionID: "session", Cwd: t.TempDir(), Message: "stall"}
	req.OnEvent = func(event StreamEvent) {
		if event.Event == "init" {
			cancel()
		}
	}
	if _, err := client.Chat(ctx, req); err != context.Canceled {
		t.Fatalf("cancelled first turn = %v", err)
	}
	saved, ok, err := store.Get("session")
	if err != nil || !ok || saved.ConversationID != "native-created" {
		t.Fatalf("initial native identity = %#v, %v", saved, err)
	}
	client.CloseSession("session")
	req.OnEvent = nil
	req.Message = "resume"
	response, err := client.Chat(t.Context(), req)
	if err != nil || !strings.Contains(response.Text, "--conversation native-created") {
		t.Fatalf("resume after cancelled first turn = %#v, %v", response, err)
	}
}

func TestIdleProcessExitRestartsBeforeSubmittingNextTurn(t *testing.T) {
	binary := fakeAgy(t, `#!/bin/sh
read input
printf '{"event":"result","result":{"status":"SUCCESS","conversation_id":"native-created","response":"%s"}}\n' "$*"
`)
	client := NewCLIClient(binary, nil)
	t.Cleanup(func() { _ = client.Close() })
	req := ChatRequest{SessionID: "session", Cwd: t.TempDir(), Message: "first"}
	if _, err := client.Chat(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	<-client.sessions["session"].process.done
	req.Message = "second"
	response, err := client.Chat(t.Context(), req)
	if err != nil || !strings.Contains(response.Text, "--conversation native-created") {
		t.Fatalf("restart idle native process = %#v, %v", response, err)
	}
}

func TestPrintTimeoutIsPassedOnlyWhenConfigured(t *testing.T) {
	binary := fakeAgy(t, `#!/bin/sh
read input
printf '{"event":"result","result":{"status":"SUCCESS","response":"%s"}}\n' "$*"
`)
	client := NewCLIClient(binary, nil)
	unlimited, err := client.Chat(t.Context(), ChatRequest{Cwd: t.TempDir(), Message: "hi"})
	if err != nil || strings.Contains(unlimited.Text, "--print-timeout") {
		t.Fatalf("unconfigured timeout args = %q, %v", unlimited.Text, err)
	}
	limited, err := client.Chat(t.Context(), ChatRequest{Cwd: t.TempDir(), Message: "hi", Timeout: 90 * time.Second})
	if err != nil || !strings.Contains(limited.Text, "--print-timeout 1m30s") {
		t.Fatalf("configured timeout args = %q, %v", limited.Text, err)
	}
}
