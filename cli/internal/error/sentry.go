package error

import (
	"context"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/config"
	"github.com/meroxa/prod/cli/internal/settings"
)

// provides error tracking functionality with consent checks
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

	err := sentry.Init(sentry.ClientOptions{
		Dsn: config.SentryDSN,
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

// captureError sends an error to Sentry if the user has consented to error tracking
// Errors in the tracking process are logged rather than returned to avoid disrupting application flow
func (c *client) captureError(err error) {
	// Check if user has consented to error tracking
	hasConsent, consentErr := settings.HasConsent()
	if consentErr != nil {
		// If we can't check consent, don't send the error and log the issue
		slog.Debug("Error tracking: failed to check consent", "error", consentErr)
		return
	}

	if !hasConsent {
		// User hasn't consented, silently skip
		return
	}

	if !c.initialized {
		slog.Debug("Error tracking: Sentry  not initialized")
		return
	}

	// Capture the error with Sentry
	sentry.CaptureException(err)
}

// captureErrorWithContext sends an error to Sentry with additional context
func (c *client) captureErrorWithContext(err error, context map[string]any) {
	// Check consent first
	hasConsent, consentErr := settings.HasConsent()
	if consentErr != nil {
		slog.Debug("Error tracking: failed to check consent", "error", consentErr)
		return
	}

	if !hasConsent {
		// User hasn't consented, silently skip
		return
	}

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
	// Check consent
	hasConsent, consentErr := settings.HasConsent()
	if consentErr != nil {
		slog.Debug("Error tracking: failed to check consent", "error", consentErr)
		return
	}

	if !hasConsent {
		// User hasn't consented, silently skip
		return
	}

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
	// Check consent
	hasConsent, consentErr := settings.HasConsent()
	if consentErr != nil {
		slog.Debug("Error tracking: failed to check consent", "error", consentErr)
		return
	}

	if !hasConsent {
		// User hasn't consented, silently skip
		return
	}

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

// withContext returns a context with the current hub for concurrent operations
func (c *client) withContext(ctx context.Context) context.Context {
	if !c.initialized {
		return ctx
	}

	return sentry.SetHubOnContext(ctx, sentry.CurrentHub().Clone())
}
