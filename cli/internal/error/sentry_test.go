package error

import (
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-errors/errors"
)

func TestNewClient(t *testing.T) {
	// newClient should still be available for internal testing
	client := newClient()
	if client == nil {
		t.Fatal("newClient() returned nil")
	}

	if client.initialized {
		t.Error("Client should not be initialized on creation")
	}
}

// Telemetry is OFF by default: with no PROD_SENTRY_DSN, Initialize is a no-op and
// the client is never marked initialized (nothing phones home).
func TestGlobalInitializeDisabledByDefault(t *testing.T) {
	t.Setenv("PROD_SENTRY_DSN", "")
	sentryClient = nil
	once = sync.Once{}

	if err := Initialize(); err != nil {
		t.Errorf("Initialize should be a no-op nil without a DSN, got: %v", err)
	}
	if sentryClient == nil {
		t.Fatal("global client should be set")
	}
	if sentryClient.initialized {
		t.Error("client must NOT be initialized without PROD_SENTRY_DSN (telemetry off by default)")
	}

	sentryClient = nil
	once = sync.Once{}
}

// A user's OWN Sentry DSN opts in.
func TestGlobalInitializeOptIn(t *testing.T) {
	t.Setenv("PROD_SENTRY_DSN", "SENTRY_DSN_REDACTED")
	sentryClient = nil
	once = sync.Once{}

	if err := Initialize(); err != nil {
		t.Errorf("Initialize with a valid DSN failed: %v", err)
	}
	if sentryClient == nil || !sentryClient.initialized {
		t.Error("client should be initialized when PROD_SENTRY_DSN is set")
	}

	sentryClient = nil
	once = sync.Once{}
}

func TestCaptureErrorViaGlobal(t *testing.T) {
	Initialize() // no DSN in the test env → disabled

	// Must not panic and must no-op when telemetry is disabled.
	CaptureError(errors.Errorf("test error"))
}

func TestCaptureMessageViaGlobal(t *testing.T) {
	Initialize() // Initialize with empty DSN for testing

	// Test message capture (no return value now)
	CaptureMessage("test message", sentry.LevelInfo)
}

func TestGlobalFunctions(t *testing.T) {
	// Test global functions don't panic when not initialized (no return values now)
	CaptureError(errors.Errorf("test error"))
	CaptureMessage("test message", sentry.LevelInfo)

	// These should not panic
	Flush()
	AddBreadcrumb("test", "test", sentry.LevelInfo)
}

func TestFlushFunctions(t *testing.T) {
	// Initialize
	err := Initialize()
	if err != nil {
		t.Errorf("Initialize failed: %v", err)
	}

	// Test regular flush function
	Flush()

	// Test FlushWithTimeout function
	FlushWithTimeout(100 * time.Millisecond)

	// Reset for other tests
	sentryClient = nil
	once = sync.Once{}
}
