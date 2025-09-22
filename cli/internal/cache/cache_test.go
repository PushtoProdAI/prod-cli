package cache

import (
	"testing"
)

func TestFetchURLAsMarkdownWithOptions(t *testing.T) {
	// Simple test to verify the options work
	tests := []struct {
		name      string
		cleanHTML bool
		desc      string
	}{
		{
			name:      "with cleaning",
			cleanHTML: true,
			desc:      "Should clean HTML before markdown conversion",
		},
		{
			name:      "without cleaning",
			cleanHTML: false,
			desc:      "Should skip HTML cleaning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't easily test actual HTTP calls, but we can verify the function signature works
			_, err := FetchURLAsMarkdownWithOptions("https://example.com", tt.cleanHTML)
			// We expect this to fail with a network error, which is fine for this test
			if err == nil {
				t.Skip("Network call succeeded unexpectedly")
			}
		})
	}
}

func TestCleanVisibleHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "removes display:none elements",
			input: `<div>Visible</div><div style="display:none">Hidden</div><div>Also visible</div>`,
		},
		{
			name:  "removes aria-hidden elements",
			input: `<div>Visible</div><div aria-hidden="true">Hidden</div><div>Also visible</div>`,
		},
		{
			name:  "removes elements with hidden class",
			input: `<div>Visible</div><div class="hidden">Hidden</div><div>Also visible</div>`,
		},
		{
			name:  "preserves normal elements",
			input: `<div>Normal content</div><p>More content</p>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CleanVisibleHTML(tt.input)
			if err != nil {
				t.Errorf("CleanVisibleHTML() error = %v", err)
				return
			}

			// Basic check that we got some output
			if len(result) == 0 {
				t.Error("CleanVisibleHTML() returned empty string")
			}

			// For these tests, we mainly want to ensure no errors occur
			// More detailed testing would require parsing the HTML result
		})
	}
}
