package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureInGitignore(t *testing.T) {
	t.Run("creates .gitignore if it doesn't exist", func(t *testing.T) {
		tmpDir := t.TempDir()

		err := ensureInGitignore(tmpDir, ".prod")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		gitignorePath := filepath.Join(tmpDir, ".gitignore")
		content, err := os.ReadFile(gitignorePath)
		if err != nil {
			t.Fatalf("expected .gitignore to be created, got error: %v", err)
		}

		expected := ".prod\n"
		if string(content) != expected {
			t.Errorf("expected content %q, got %q", expected, string(content))
		}
	})

	t.Run("appends to existing .gitignore if entry doesn't exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		gitignorePath := filepath.Join(tmpDir, ".gitignore")

		existingContent := "node_modules/\n*.log\n"
		err := os.WriteFile(gitignorePath, []byte(existingContent), 0o644)
		if err != nil {
			t.Fatalf("failed to create test .gitignore: %v", err)
		}

		err = ensureInGitignore(tmpDir, ".prod")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		content, err := os.ReadFile(gitignorePath)
		if err != nil {
			t.Fatalf("failed to read .gitignore: %v", err)
		}

		expected := existingContent + ".prod\n"
		if string(content) != expected {
			t.Errorf("expected content %q, got %q", expected, string(content))
		}
	})

	t.Run("doesn't duplicate entry if it already exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		gitignorePath := filepath.Join(tmpDir, ".gitignore")

		existingContent := "node_modules/\n.prod\n*.log\n"
		err := os.WriteFile(gitignorePath, []byte(existingContent), 0o644)
		if err != nil {
			t.Fatalf("failed to create test .gitignore: %v", err)
		}

		err = ensureInGitignore(tmpDir, ".prod")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		content, err := os.ReadFile(gitignorePath)
		if err != nil {
			t.Fatalf("failed to read .gitignore: %v", err)
		}

		if string(content) != existingContent {
			t.Errorf("expected content to remain unchanged %q, got %q", existingContent, string(content))
		}
	})
}
