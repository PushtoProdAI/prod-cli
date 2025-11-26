package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-errors/errors"
)

// parseDiffString converts a unified diff string into structured DiffLine data
func parseDiffString(diffStr string) []DiffLine {
	if diffStr == "" {
		return nil
	}

	lines := strings.Split(diffStr, "\n")
	var diffLines []DiffLine

	for _, line := range lines {
		var lineType string

		if strings.HasPrefix(line, "@@") {
			lineType = "header"
		} else if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			lineType = "fileheader"
		} else if strings.HasPrefix(line, "+") {
			lineType = "added"
		} else if strings.HasPrefix(line, "-") {
			lineType = "removed"
		} else {
			lineType = "context"
		}

		diffLines = append(diffLines, DiffLine{
			Type:    lineType,
			Content: line,
		})
	}

	return diffLines
}

// findLatestBackup finds the most recent backup file for a given config filename
func findLatestBackup(prodDir, configFilename string) (string, error) {
	entries, err := os.ReadDir(prodDir)
	if err != nil {
		return "", errors.Errorf("failed to read .prod directory: %w", err)
	}

	var backups []string
	prefix := configFilename + "."
	suffix := ".bak"

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			backups = append(backups, name)
		}
	}

	if len(backups) == 0 {
		return "", errors.Errorf("no backup files found for %s", configFilename)
	}

	// Sort backups by filename (which includes timestamp) to get the latest
	sort.Strings(backups)
	latestBackup := backups[len(backups)-1]

	return filepath.Join(prodDir, latestBackup), nil
}
