package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeRunner(t *testing.T) {
	tmp := t.TempDir()
	bin, log := filepath.Join(tmp, "opencode"), filepath.Join(tmp, "log")
	os.WriteFile(bin, []byte(`#!/bin/sh
in=$(cat)
echo "ARGS $*" >> `+log+`
echo "ENV $OPENCODE_CONFIG_CONTENT" >> `+log+`
echo "PWD $PWD" >> `+log+`
echo "STDIN $in" >> `+log+`
case "$*" in *"--model bad/x"*)
  echo '{"type":"error","sessionID":"s9","error":{"type":"provider.no-route","message":"Model unavailable: bad/x"}}'; exit 0;; esac
echo '{"type":"step_start","sessionID":"s1","part":{"id":"p0","type":"step-start"}}'
echo '{"type":"tool_use","sessionID":"s1","part":{"id":"p1","tool":"read","state":{"status":"completed","input":{"path":"a.go"}}}}'
echo '{"type":"step_finish","sessionID":"s1","part":{"id":"p2","reason":"tool-calls","tokens":{"input":10,"output":3,"reasoning":1,"cache":{"read":5}}}}'
echo '{"type":"text","sessionID":"s1","part":{"id":"p3","text":"Xin chào"}}'
echo '{"type":"step_finish","sessionID":"s1","part":{"id":"p4","reason":"stop","tokens":{"input":2,"output":2,"reasoning":0,"cache":{"read":0}}}}'
`), 0o755)
	hist := []HistoryItem{{Role: "user", Content: "câu cũ"}, {Role: "assistant", Content: "trả lời cũ"}}
	req := RunRequest{Bin: bin, System: "HƯỚNG DẪN", History: hist, Prompt: "mới", WorkDir: tmp, Model: "p/m"}
	var text strings.Builder
	res, err := opencodeRunner{}.Run(context.Background(), req, func(e Event) {
		if e.Type == "text" {
			text.WriteString(e.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Xin chào" || res.Text != "Xin chào" || res.SessionID != "s1" || res.Usage.InputTokens != 17 || res.Usage.OutputTokens != 6 {
		t.Fatalf("text=%q res=%+v", text.String(), res)
	}
	if len(res.Tools) != 1 || res.Tools[0].Summary != "Đọc a.go" {
		t.Fatalf("tools = %+v", res.Tools)
	}
	raw, _ := os.ReadFile(log)
	got := string(raw)
	for _, want := range []string{"run --standalone --format json --model p/m", `"bash":"deny"`, `"edit":"deny"`, "HƯỚNG DẪN", "câu cũ", "PWD " + tmp} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}

	// going on in the session, allowed to edit: only the new message
	os.Remove(log)
	req.SessionID, req.Write = "s1", true
	if _, err := (opencodeRunner{}).Run(context.Background(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(log)
	got = string(raw)
	if !strings.Contains(got, "--session s1") || !strings.Contains(got, `"edit":"allow"`) || !strings.Contains(got, `"bash":"deny"`) || strings.Contains(got, "câu cũ") {
		t.Fatalf("resume:\n%s", got)
	}

	// full access: --auto, nothing denied
	os.Remove(log)
	req.FullAccess = true
	if _, err := (opencodeRunner{}).Run(context.Background(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(log)
	if got = string(raw); !strings.Contains(got, "--auto") || strings.Contains(got, "permission") {
		t.Fatalf("full access:\n%s", got)
	}

	// no tools at all (a channel run)
	os.Remove(log)
	req.FullAccess, req.NoTools = false, true
	if _, err := (opencodeRunner{}).Run(context.Background(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if raw, _ = os.ReadFile(log); !strings.Contains(string(raw), `{"*":"deny"}`) {
		t.Fatalf("no tools:\n%s", raw)
	}

	// an error event and no answer: the turn fails with its message
	req.NoTools, req.Model = false, "bad/x"
	if _, err := (opencodeRunner{}).Run(context.Background(), req, func(Event) {}); err == nil || !strings.Contains(err.Error(), "Model unavailable") {
		t.Fatalf("err = %v", err)
	}
}
