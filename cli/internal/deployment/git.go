package deployment

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-errors/errors"
)

func InitializeGitRepo(buildContext string) error {
	gitDir := filepath.Join(buildContext, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		cmd := exec.Command("git", "init")
		cmd.Dir = buildContext
		if err := cmd.Run(); err != nil {
			return errors.Errorf("failed to initialize git: %w", err)
		}
	}
	return nil
}

func ConfigureGitUser(buildContext string) error {
	cmd := exec.Command("git", "config", "user.email")
	cmd.Dir = buildContext
	output, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		cmd = exec.Command("git", "config", "user.email", "deploy@prod-cli.local")
		cmd.Dir = buildContext
		if err := cmd.Run(); err != nil {
			return errors.Errorf("failed to set git user.email: %w", err)
		}
	}

	cmd = exec.Command("git", "config", "user.name")
	cmd.Dir = buildContext
	output, err = cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		cmd = exec.Command("git", "config", "user.name", "Prod CLI Deploy")
		cmd.Dir = buildContext
		if err := cmd.Run(); err != nil {
			return errors.Errorf("failed to set git user.name: %w", err)
		}
	}

	return nil
}

func GitAddAll(buildContext string) error {
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = buildContext
	if err := cmd.Run(); err != nil {
		return errors.Errorf("failed to add files: %w", err)
	}
	return nil
}

func GitCommit(buildContext, message string) error {
	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = buildContext
	output, err := cmd.CombinedOutput()
	if err != nil {
		if !strings.Contains(string(output), "nothing to commit") {
			return errors.Errorf("failed to commit: %w", err)
		}
	}
	return nil
}
