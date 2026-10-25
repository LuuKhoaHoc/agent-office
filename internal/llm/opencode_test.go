package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeCLI(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	os.WriteFile(bin, []byte(`#!/bin/sh
case "$1" in
--version) echo "opencode v2.1.4";;
models) [ -n "$NONE" ] || printf 'anthropic/claude-x\n9router/combo\nsome warning line\n';;
run) cat > `+filepath.Join(dir, "stdin")+`; echo "$OPENCODE_CONFIG_CONTENT" > `+filepath.Join(dir, "env")+`
  echo '{"type":"text","sessionID":"s","part":{"text":"pong"}}'
  echo '{"type":"step_finish","sessionID":"s","part":{"tokens":{"input":3,"output":1,"reasoning":1,"cache":{"read":2}}}}';;
esac
`), 0o755)
	c := &opencodeCLI{bin: bin}
	ck, err := c.Check(context.Background())
	if err != nil || ck.Version != "opencode v2.1.4" || strings.Join(ck.Models, ",") != "anthropic/claude-x,9router/combo" {
		t.Fatalf("check = %+v %v", ck, err)
	}
	if _, err := OpenCodeModels(context.Background(), bin, []string{"NONE=1"}); !errors.Is(err, ErrNeedsLogin) {
		t.Fatalf("no models err = %v", err)
	}
	res, err := c.Complete(context.Background(), Request{System: "S", Prompt: "ping", Model: "9router/combo"})
	if err != nil || res.Text != "pong" || res.InputTokens != 5 || res.OutputTokens != 2 {
		t.Fatalf("complete = %+v %v", res, err)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "env"))
	if !strings.Contains(string(env), `"bash":"deny"`) || !strings.Contains(string(env), `"edit":"deny"`) {
		t.Fatalf("a completion must not edit or run commands: %s", env)
	}
}
