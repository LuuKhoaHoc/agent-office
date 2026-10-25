package chat

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/proctrack"
	"github.com/tung1998/agent-office/internal/storage"
)

// opencodeRunner drives OpenCode (`opencode run`): the prompt on stdin, JSON
// events out, the chat resumed with --session. OpenCode asks no one in this
// mode, so what the run may not do is denied in its config (llm.OpenCodeAccess).
// ponytail: ExtraDirs, office MCP tools and the user's MCP choice are not
// passed yet (OpenCode loads the person's own opencode.json servers).
type opencodeRunner struct{}

func (opencodeRunner) Run(ctx context.Context, req RunRequest, emit func(Event)) (res RunResult, _ error) {
	bin := firstNonEmpty(req.Bin, "opencode")
	args := llm.OpenCodeArgs(req.Model, req.SessionID)
	env := os.Environ()
	if req.FullAccess {
		args = append(args, "--auto") // approve what is not denied
	} else {
		env = append(env, llm.OpenCodeAccess(req.Write, req.NoTools, req.NoSubagents))
	}
	prompt, images := codexPrompt(req.Prompt, req.Attachments)
	for _, p := range images {
		args = append(args, "--file", p)
	}
	if req.SessionID == "" {
		prompt = req.System + "\n\n" + transcript(req.History, prompt)
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, bin, args...)
	ownGroup(cmd)
	// OpenCode takes its project from $PWD, which an explicit Env does not reset
	cmd.Dir, cmd.Env = req.WorkDir, append(env, "PWD="+req.WorkDir)
	cmd.Stdin = strings.NewReader(strings.ToValidUTF8(prompt, "\uFFFD"))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("không chạy được %s: %w", bin, err)
	}
	defer proctrack.Track(ctx, cmd.Process.Pid)()
	res.Usage.Model = req.Model
	var answer strings.Builder
	var failure, stepErr string
	seen := map[string]bool{}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev llm.OpenCodeEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		res.SessionID = cmp.Or(ev.SessionID, res.SessionID)
		p := ev.Part
		switch ev.Type {
		case "text":
			if p.Text != "" {
				if answer.Len() > 0 {
					answer.WriteString("\n\n")
				}
				answer.WriteString(p.Text)
				emit(Event{Type: "text", Text: p.Text})
			}
		case "tool_use":
			if p.State.Status == "error" {
				stepErr = p.Tool + ": " + p.State.Error
			}
			if !seen[p.ID] {
				seen[p.ID] = true
				tc := storage.ToolCall{Name: p.Tool, Summary: toolSummary(p.Tool, p.State.Input)}
				res.Tools = append(res.Tools, tc)
				emit(Event{Type: "tool", Tool: &tc})
			}
		case "step_finish":
			res.Usage.InputTokens += p.Tokens.Input + p.Tokens.Cache.Read
			res.Usage.OutputTokens += p.Tokens.Output + p.Tokens.Reasoning
		case "error":
			failure = cmp.Or(ev.Error.Message, ev.Error.Type)
		}
	}
	scanErr := sc.Err()
	io.Copy(io.Discard, out)
	werr := cmd.Wait()
	res.Usage.DurationMS = time.Since(start).Milliseconds()
	res.Text = strings.TrimSpace(answer.String())
	switch {
	case scanErr != nil:
		return res, fmt.Errorf("opencode: đọc output lỗi: %w", scanErr)
	case res.Text != "":
		return res, nil
	case failure != "":
		return res, fmt.Errorf("opencode: %s", truncate(failure, 500))
	case werr != nil:
		return res, fmt.Errorf("opencode: %s", truncate(cmp.Or(strings.TrimSpace(stderr.String()), werr.Error()), 500))
	case stepErr != "":
		return res, errors.New("opencode kết thúc lượt mà không trả lời: " + truncate(stepErr, 300))
	}
	return res, errors.New("opencode kết thúc lượt mà không trả lời")
}
