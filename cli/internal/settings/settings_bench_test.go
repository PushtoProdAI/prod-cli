package settings

import (
	"testing"
)

// BenchmarkHasConsentWithCache benchmarks HasConsent with caching enabled (after consent set)
func BenchmarkHasConsentWithCache(b *testing.B) {
	// Pre-populate cache by setting consent
	SaveConsent(true)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := HasConsent()
		if err != nil {
			b.Fatalf("HasConsent failed: %v", err)
		}
	}
}

// BenchmarkHasConsentWithoutCache benchmarks HasConsent with cache invalidated (worst case)
func BenchmarkHasConsentWithoutCache(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Invalidate cache before each call to force file read
		InvalidateConsentCache()
		_, err := HasConsent()
		if err != nil {
			b.Fatalf("HasConsent failed: %v", err)
		}
	}
}

// BenchmarkErrorTrackingScenario simulates a realistic error tracking scenario
// where multiple operations check consent in sequence (after user has set consent)
func BenchmarkErrorTrackingScenario(b *testing.B) {
	// Set consent to enable caching
	SaveConsent(true)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate multiple consent checks that would happen during error capture
		// This represents: CaptureError + CaptureErrorWithContext + AddBreadcrumb + CaptureMessage
		for j := 0; j < 4; j++ {
			_, err := HasConsent()
			if err != nil {
				b.Fatalf("HasConsent failed: %v", err)
			}
		}
	}
}
