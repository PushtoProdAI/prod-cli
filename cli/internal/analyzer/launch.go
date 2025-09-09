package analyzer

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type LauncherFile struct {
	Name    string
	Content string
}

type LaunchContext struct {
	Launchers []LauncherFile
	Readme    string
}

func findLauncherFiles(root string) ([]string, error) {
	var launchFiles []string

	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		ext := filepath.Ext(name)

		// Python launcher heuristics
		if ext == ".py" {
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			src := string(content)
			if strings.Contains(src, "if __name__ == \"__main__\"") ||
				strings.Contains(src, "uvicorn.run") ||
				strings.Contains(src, "app.run") {
				launchFiles = append(launchFiles, path)
			}
		}

		// Shell script launcher heuristics
		if ext == ".sh" && (name == "docker-entrypoint.sh" || name == "entrypoint.sh") {
			launchFiles = append(launchFiles, path)
		}

		return nil
	})

	return launchFiles, nil
}

// TODO: we are doing something similar in the env var code, we can probably consolidate
func readSnippet(path string, maxLines int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		count++
		if count >= maxLines {
			break
		}
	}
	return strings.Join(lines, "\n"), scanner.Err()
}

func getReadmeContents(fsys fs.FS) (string, error) {
	readmeCandidates := []string{
		"README.md",
		"Readme.md",
		"readme.md",
		"README.MD",
		"README.markdown",
		"readme.markdown",
		"README.txt",
		"readme.txt",
		"README",
		"readme",
	}
	for _, candidate := range readmeCandidates {
		data, err := fs.ReadFile(fsys, candidate)
		if err == nil {
			return string(data), nil
		}
	}
	return "", fs.ErrNotExist
}
