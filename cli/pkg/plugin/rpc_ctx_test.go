package plugin

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"testing"
	"time"
)

type slowSvc struct{}

// Sleep blocks for d milliseconds — a stand-in for a hung/runaway plugin RPC.
func (slowSvc) Sleep(d int, reply *int) error {
	time.Sleep(time.Duration(d) * time.Millisecond)
	*reply = d
	return nil
}

// callCtx must return as soon as the context is done, instead of blocking on a plugin that
// never replies — the DoS bound for a hung provider plugin.
func TestCallCtxHonorsContext(t *testing.T) {
	srv := rpc.NewServer()
	if err := srv.RegisterName("Plugin", slowSvc{}); err != nil {
		t.Fatal(err)
	}
	c1, c2 := net.Pipe()
	go srv.ServeConn(c2)
	client := rpc.NewClient(c1)
	defer client.Close()

	// A call that completes before the deadline returns normally.
	var out int
	if err := callCtx(context.Background(), client, "Plugin.Sleep", 1, &out); err != nil {
		t.Fatalf("fast call errored: %v", err)
	}

	// A call slower than the deadline returns ctx.DeadlineExceeded, not a hang.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := callCtx(ctx, client, "Plugin.Sleep", 10000, &out)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("slow call should return DeadlineExceeded, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Error("callCtx did not return promptly on context expiry")
	}
}
