package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/go-errors/errors"
)

// deployRunTimeout bounds a headless deploy when the caller's context has no
// deadline, so an unexpected interactive prompt in the child can't hang forever.
const deployRunTimeout = 30 * time.Minute

// deployResult is what a deploy run captures from the JSON event stream.
type deployResult struct {
	Plan             map[string]any // the plan_approval_request event, if seen
	Status           string         // "success" | "failed" (deploy only)
	URL              string
	Error            string
	NeedsInteractive bool  // hit a prompt (env var / auth) we can't answer headlessly
	ScanErr          error // a stdout read error, if any
}

// processEvents reads prod's JSON event stream, drives the human-approval gate,
// and RETURNS as soon as the run reaches a terminal state, so the caller can
// close stdin and let the child exit (the child stays alive at its input loop
// after a deploy — it does not close stdout on its own).
//
//   - plan_approval_request: reply "approved" (confirm) or "rejected" (preview).
//     A preview is terminal here — after "rejected" the child cancels and exits.
//   - deployment_complete: terminal; capture status/url/error.
//   - env_var_prompt: a deploy needing interactive input can't be driven
//     headlessly — flag it and stop.
//
// Because the deploy only proceeds after an "approved" reply, a preview
// (confirm=false) can never deploy. Split out from runProd so it's unit-testable.
func processEvents(events io.Reader, stdin io.Writer, confirm bool) *deployResult {
	res := &deployResult{}
	sc := bufio.NewScanner(events)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue // skip non-JSON lines (plain logs)
		}
		switch ev["type"] {
		case "plan_approval_request":
			res.Plan = ev
			if confirm {
				_, _ = io.WriteString(stdin, "approved\n")
			} else {
				_, _ = io.WriteString(stdin, "rejected\n")
				return res // preview is terminal: the child cancels and exits
			}
		case "deployment_complete":
			res.Status, _ = ev["status"].(string)
			res.URL, _ = ev["url"].(string)
			res.Error, _ = ev["error"].(string)
			return res // terminal
		case "env_var_prompt":
			res.NeedsInteractive = true
			return res // can't answer headlessly
		}
	}
	res.ScanErr = sc.Err()
	return res
}

// runProd drives a `prod run` subprocess over the JSON event substrate in dir,
// approving or rejecting the plan per confirm, and returns the captured result.
// It reuses the exact, tested deploy path (no in-process re-wiring); the confirm
// gate is enforced here by replying to the approval event.
func runProd(ctx context.Context, prompt string, confirm bool, dir string) (*deployResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deployRunTimeout)
		defer cancel()
	}

	exe, err := os.Executable()
	if err != nil {
		return nil, errors.Errorf("cannot locate the prod binary: %w", err)
	}

	// `--` stops flag parsing so a prompt beginning with `-` can't smuggle a flag
	// into `prod run` (the prompt is untrusted agent input).
	cmd := exec.CommandContext(ctx, exe, "run", "--", prompt)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "PROD_JSON_MODE=true")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Errorf("failed to start deploy: %w", err)
	}

	res := processEvents(stdout, stdin, confirm)
	// The child blocks at its input loop after a terminal event; closing stdin
	// gives it EOF so it exits, and draining stdout keeps it from blocking on a
	// full pipe while it flushes and shuts down.
	_ = stdin.Close()
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()

	switch {
	case res.NeedsInteractive:
		return nil, errors.Errorf("this deploy needs interactive input (e.g. environment-variable values); run it from the CLI: prod %q", prompt)
	case res.ScanErr != nil:
		return nil, errors.Errorf("failed reading deploy output: %w", res.ScanErr)
	case res.Plan == nil && res.Status == "":
		// Nothing structured at all — a real failure (no LLM, couldn't start).
		if waitErr != nil {
			return nil, errors.Errorf("deploy produced no result (is an LLM configured? run `prod doctor`): %w", waitErr)
		}
		return nil, errors.Errorf("deploy produced no plan or result")
	case confirm && res.Status == "":
		// We approved but never saw a completion — surface the failure.
		if waitErr != nil {
			return nil, errors.Errorf("deploy did not complete: %w", waitErr)
		}
		return nil, errors.Errorf("deploy did not complete")
	}
	return res, nil
}

// planSummary is the human-reviewable slice of a plan surfaced to the agent.
type planSummary struct {
	Action                  string  `json:"action,omitempty"`
	Platform                string  `json:"platform,omitempty"`
	Shape                   string  `json:"shape,omitempty"` // web | mcp-server | worker | cron
	Summary                 string  `json:"summary,omitempty"`
	EstimatedMonthlyCostUSD float64 `json:"estimatedMonthlyCostUsd,omitempty"`
}

func summarizePlan(ev map[string]any) *planSummary {
	if ev == nil {
		return nil
	}
	ps := &planSummary{}
	ps.Action, _ = ev["action"].(string)
	ps.Platform, _ = ev["platform"].(string)
	ps.Shape, _ = ev["shape"].(string)
	ps.Summary, _ = ev["summary"].(string)
	if p, ok := ev["pricing"].(map[string]any); ok {
		ps.EstimatedMonthlyCostUSD, _ = p["total"].(float64)
	}
	return ps
}
