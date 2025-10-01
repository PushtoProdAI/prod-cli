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

func TestGlobalInitialize(t *testing.T) {
	// Test with empty DSN (should still work for testing)
	err := Initialize()
	if err != nil {
		t.Errorf("Initialize with empty DSN failed: %v", err)
	}

	if sentryClient == nil {
		t.Error("Global client should be initialized")
	}

	if !sentryClient.initialized {
		t.Error("Client should be marked as initialized")
	}

	// Reset for other tests
	sentryClient = nil
	once = sync.Once{}
}

func TestCaptureErrorViaGlobal(t *testing.T) {
	Initialize() // Initialize with empty DSN for testing

	// This test verifies that consent is checked
	// The actual consent status depends on user settings
	testErr := errors.Errorf("test error")

	// This should not panic and should handle consent gracefully (no return value now)
	CaptureError(testErr)
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
