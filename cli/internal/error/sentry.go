package error

import (
	"log/slog"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/config"
)

// client provides opt-in error tracking. It is enabled only when the user sets
// PROD_SENTRY_DSN (their own Sentry); otherwise every method is a no-op.
type client struct {
	initialized bool
}

// new creates a new error tracking client
func newClient() *client {
	return &client{
		initialized: false,
	}
}

// initialize sets up Sentry with the provided DSN
// This should be called early in the application lifecycle
func (c *client) initialize() error {
	if c.initialized {
		return nil // Already initialized
	}

	// Opt-in only: prod never reports to the maintainer. Error tracking is enabled
	// solely when the user points it at THEIR OWN Sentry via PROD_SENTRY_DSN.
	dsn := os.Getenv("PROD_SENTRY_DSN")
	if dsn == "" {
		return nil // telemetry disabled — the default for the local-first binary
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn: dsn,
		// Set sample rate for performance monitoring
		TracesSampleRate: 0.1,
		// Set the environment (can be overridden by caller if needed)
		Environment: config.GetEnvironment(),
		// Enable debug mode in development
		Debug: false,
		// Disable sending personally identifiable information by default
		SendDefaultPII: false,
		// Set release information if available
		// Release: version, // Can be set by caller
		// Remove server_name and geographic data for privacy since this runs on user machines
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			event.ServerName = ""
			// Remove user IP address and any geographic information
			if event.User.IPAddress != "" {
				event.User.IPAddress = ""
			}
			return event
		},
	})
	if err != nil {
		return errors.WrapPrefix(err, "failed to initialize Sentry", 0)
	}

	c.initialized = true
	return nil
}

// captureError sends an error to the user's own Sentry. It's a no-op unless the
// user opted in via PROD_SENTRY_DSN (c.initialized). Tracking-process errors are
// logged, not returned, so they never disrupt the application flow.
func (c *client) captureError(err error) {
	if !c.initialized {
		slog.Debug("Error tracking: Sentry  not initialized")
		return
	}

	// Capture the error with Sentry
	sentry.CaptureException(err)
}

// captureErrorWithContext sends an error to Sentry with additional context
func (c *client) captureErrorWithContext(err error, context map[string]any) {
	if !c.initialized {
		slog.Debug("Error tracking: Sentry  not initialized")
		return
	}

	// Configure scope with additional context
	sentry.WithScope(func(scope *sentry.Scope) {
		// Add context data
		for key, value := range context {
			// Convert interface{} to map[string]interface{} for Sentry context
			if contextMap, ok := value.(map[string]any); ok {
				scope.SetContext(key, contextMap)
			} else {
				// If not a map, create a simple context with the value
				scope.SetContext(key, map[string]any{"value": value})
			}
		}

		// Capture the error
		sentry.CaptureException(err)
	})
}

// captureMessage sends a message to Sentry (for non-error events)
func (c *client) captureMessage(message string, level sentry.Level) {
	if !c.initialized {
		slog.Debug("Error tracking: Sentry  not initialized")
		return
	}

	// Set level in scope and capture message
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(level)
		sentry.CaptureMessage(message)
	})
}

// flush waits for all events to be sent to Sentry
// Should be called before application exits
func (c *client) flush() {
	if !c.initialized {
		return
	}

	// Wait up to 5 seconds for events to be sent
	sentry.Flush(5 * time.Second)
}

// addBreadcrumb adds a breadcrumb to help with debugging
func (c *client) addBreadcrumb(message, category string, level sentry.Level) {
	if !c.initialized {
		slog.Debug("Error tracking: Sentry  not initialized")
		return
	}

	sentry.AddBreadcrumb(&sentry.Breadcrumb{
		Message:   message,
		Category:  category,
		Level:     level,
		Timestamp: time.Now(),
	})
}
