package error

import (
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

var (
	// Global client instance
	sentryClient *client
	once         sync.Once
)

// Initialize sets up the global error tracking client
func Initialize() error {
	var err error
	once.Do(func() {
		sentryClient = newClient()
		err = sentryClient.initialize()
	})
	return err
}

// CaptureError captures an error using the global client
func CaptureError(err error) {
	if sentryClient == nil {
		return // Not initialized, silently skip
	}
	sentryClient.captureError(err)
}

// CaptureErrorWithContext captures an error with context using the global client
func CaptureErrorWithContext(err error, context map[string]any) {
	if sentryClient == nil {
		return
	}
	sentryClient.captureErrorWithContext(err, context)
}

// CaptureMessage captures a message using the global client
func CaptureMessage(message string, level sentry.Level) {
	if sentryClient == nil {
		return
	}
	sentryClient.captureMessage(message, level)
}

// Flush waits for all events to be sent to Sentry
func Flush() {
	if sentryClient != nil {
		sentryClient.flush()
	}
}

// FlushWithTimeout waits for all events to be sent with a custom timeout
func FlushWithTimeout(timeout time.Duration) {
	if sentryClient != nil && sentryClient.initialized {
		sentry.Flush(timeout)
	}
}

// AddBreadcrumb adds a breadcrumb for debugging
func AddBreadcrumb(message, category string, level sentry.Level) {
	if sentryClient == nil {
		return
	}
	sentryClient.addBreadcrumb(message, category, level)
}
