package agent

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-errors/errors"
)

func ensureInGitignore(projectPath, entry string) error {
	gitignorePath := filepath.Join(projectPath, ".gitignore")

	file, err := os.Open(gitignorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return os.WriteFile(gitignorePath, []byte(entry+"\n"), 0644)
		}
		return errors.Errorf("failed to open .gitignore: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == entry {
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		return errors.Errorf("failed to read .gitignore: %w", err)
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return errors.Errorf("failed to open .gitignore for appending: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(entry + "\n"); err != nil {
		return errors.Errorf("failed to write to .gitignore: %w", err)
	}

	return nil
}
