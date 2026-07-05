package agent

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// --yes drives a multi-state flow to completion without any input.
func TestDriveOneShotAutoApproveCompletes(t *testing.T) {
	a := &Agent{}
	calls := 0
	var s1, s2 stateFn
	s2 = func(_ context.Context, _ string, _ io.Writer) (stateFn, error) { calls++; return nil, nil }
	s1 = func(_ context.Context, _ string, _ io.Writer) (stateFn, error) { calls++; return s2, nil }
	a.sm.currentState = s1

	var out strings.Builder
	a.DriveOneShot(context.Background(), "go", &out, strings.NewReader(""), true)

	if !a.IsComplete() {
		t.Error("flow should have completed")
	}
	if calls != 2 {
		t.Errorf("ran %d states, want 2", calls)
	}
}

// --yes must NOT spin forever on a state that waits for input it can't supply.
func TestDriveOneShotAutoApproveStuckFailsFast(t *testing.T) {
	a := &Agent{}
	var stuck stateFn
	stuck = func(_ context.Context, _ string, _ io.Writer) (stateFn, error) { return stuck, nil }
	a.sm.currentState = stuck

	var out strings.Builder
	done := make(chan struct{})
	go func() {
		a.DriveOneShot(context.Background(), "go", &out, strings.NewReader(""), true)
		close(done)
	}()

	select {
	case <-done:
		if !strings.Contains(out.String(), "can't be provided with --yes") {
			t.Errorf("expected a fail-fast message, got %q", out.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DriveOneShot spun on a stuck state instead of failing fast")
	}
}

// Interactive: answers a waiting prompt from stdin, then completes.
func TestDriveOneShotInteractiveAnswersFromInput(t *testing.T) {
	a := &Agent{}
	answered := false
	var wait stateFn
	wait = func(_ context.Context, in string, _ io.Writer) (stateFn, error) {
		if in == "y" {
			answered = true
			return nil, nil
		}
		return wait, nil // still waiting
	}
	a.sm.currentState = wait

	var out strings.Builder
	a.DriveOneShot(context.Background(), "", &out, strings.NewReader("y\n"), false)

	if !answered {
		t.Error("should have answered the prompt with input from stdin")
	}
	if !a.IsComplete() {
		t.Error("flow should have completed")
	}
}

// readThenBlock yields its data then blocks (a TTY never sends EOF), so a driver
// that reads one line too many hangs — which is exactly the post-deploy bug.
type readThenBlock struct{ r *strings.Reader }

func (b *readThenBlock) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == io.EOF {
		select {} // block forever, like a terminal waiting for the user to type
	}
	return n, err
}

// Regression: after the terminal step (deploy done → done() → nil in one-shot),
// the interactive driver must EXIT, not block reading another line from the TTY.
func TestDriveOneShotInteractiveExitsAtTerminal(t *testing.T) {
	a := &Agent{}
	var wait stateFn
	wait = func(_ context.Context, in string, _ io.Writer) (stateFn, error) {
		if in == "y" {
			return a.done(), nil // real flow cascades to a terminal returning done()
		}
		return wait, nil
	}
	a.sm.currentState = wait

	var out strings.Builder
	done := make(chan struct{})
	go func() {
		// Only "y" is available; a TTY would then block. The fix (done()==nil in
		// one-shot) must make the driver exit right after "y".
		a.DriveOneShot(context.Background(), "", &out, &readThenBlock{strings.NewReader("y\n")}, false)
		close(done)
	}()

	select {
	case <-done:
		if !a.IsComplete() {
			t.Error("should be complete after the terminal step")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hung reading a second line after the deploy completed (the item-5 bug)")
	}
}

// Interactive with no input (piped/non-TTY) stops instead of hanging.
func TestDriveOneShotInteractiveStopsOnEOF(t *testing.T) {
	a := &Agent{}
	var wait stateFn
	wait = func(_ context.Context, _ string, _ io.Writer) (stateFn, error) { return wait, nil }
	a.sm.currentState = wait

	var out strings.Builder
	done := make(chan struct{})
	go func() {
		a.DriveOneShot(context.Background(), "", &out, strings.NewReader(""), false)
		close(done)
	}()

	select {
	case <-done:
		// returned on EOF rather than hanging — correct
	case <-time.After(2 * time.Second):
		t.Fatal("DriveOneShot hung with no input; should stop on EOF")
	}
}
