package analyzer

import (
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"

	"github.com/joho/godotenv"
)

type EnvVarCandidate struct {
	VarName string
	File    string
	Line    int
	Context string
}

func walkProjectForCandidates(
	root projectFS,
	extensions []string,
	ignoreDirs []string,
	re *regexp.Regexp,
	minContextLines int,
	maxContextLines int,
) ([]EnvVarCandidate, error) {
	if re == nil {
		return nil, errors.New("regex must not be nil")
	}

	// Use map to track unique environment variables and prevent duplicates
	// Key is just the variable name - each variable appears only once regardless of file
	uniqueCandidates := make(map[string]EnvVarCandidate)

	// it is was easier to use the string path here, but we can potentially find a way to use the FS itself
	err := filepath.WalkDir(root.rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			for _, ignore := range ignoreDirs {
				if strings.Contains(path, ignore) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		matchesExt := false
		for _, ext := range extensions {
			if strings.HasSuffix(path, ext) {
				matchesExt = true
				break
			}
		}
		if !matchesExt {
			return nil
		}

		fileCandidates, err := scanFileForCandidates(path, re, minContextLines, maxContextLines)
		if err != nil {
			return err
		}

		// Add candidates to map for deduplication
		for _, candidate := range fileCandidates {
			// Use only variable name as key - keeps first occurrence of each variable
			if _, exists := uniqueCandidates[candidate.VarName]; !exists {
				uniqueCandidates[candidate.VarName] = candidate
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Convert map back to slice
	candidates := make([]EnvVarCandidate, 0, len(uniqueCandidates))
	for _, candidate := range uniqueCandidates {
		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

func scanFileForCandidates(path string, re *regexp.Regexp, minContextLines, maxContextLines int) ([]EnvVarCandidate, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var candidates []EnvVarCandidate
	lines := strings.Split(string(content), "\n")
	fullContent := string(content)

	// Find all matches in the full content (handles multi-line patterns)
	matches := re.FindAllStringSubmatch(fullContent, -1)
	matchIndices := re.FindAllStringSubmatchIndex(fullContent, -1)

	for i, match := range matches {
		if len(match) < 2 {
			continue
		}

		// Find the first non-empty capture group (variable name)
		varName := ""
		for j := 1; j < len(match); j++ {
			if match[j] != "" {
				varName = match[j]
				break
			}
		}
		matchStart := matchIndices[i][0]

		// Find which line this match is on
		lineNum := strings.Count(fullContent[:matchStart], "\n") + 1

		// Calculate context window
		contextBefore := int(math.Max(0, float64(lineNum-1-minContextLines)))
		contextAfter := int(math.Min(float64(len(lines)), float64(lineNum-1+maxContextLines+1)))

		context := strings.Join(lines[contextBefore:contextAfter], "\n")

		log.Printf("Name: %s, File: %s, Line: %d", varName, path, lineNum)

		candidates = append(candidates, EnvVarCandidate{
			VarName: varName,
			File:    path,
			Line:    lineNum,
			Context: context,
		})
	}

	return candidates, nil
}

func ParseEnvFile(path, fileName string) (map[string]string, error) {
	fullPath := filepath.Join(path, fileName)

	// Check if file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		// No file: just return empty map, no error
		return map[string]string{}, nil
	}

	envMap, err := godotenv.Read(fullPath)
	if err != nil {
		return nil, errors.Errorf("failed to read env file %s: %w", fullPath, err)
	}

	return envMap, nil
}
