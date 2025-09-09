package analyzer

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
)

type RouteCandidate struct {
	Method  string
	Path    string
	File    string
	Line    int
	Context string
}

// RouteMatch represents a raw regex match that needs to be processed
type RouteMatch struct {
	FullMatch     string   // The complete matched text
	CaptureGroups []string // All capture groups from the regex
	Line          int      // Line number where the match was found
	Context       string   // Surrounding code context
}

// RouteProcessor defines how language-specific route processing should work
type RouteProcessor interface {
	// ProcessMatch takes a raw regex match and converts it to RouteCandidate(s)
	// Returns multiple candidates if one match represents multiple routes
	ProcessMatch(match RouteMatch, filePath string) []RouteCandidate
}

func walkProjectForRoutes(
	root projectFS,
	extensions []string,
	ignoreDirs []string,
	re *regexp.Regexp,
	processor RouteProcessor,
	minContextLines int,
	maxContextLines int,
) ([]RouteCandidate, error) {
	if re == nil {
		return nil, errors.New("regex must not be nil")
	}

	// Use map to track unique routes and prevent duplicates
	// Key combines method and path - each unique route appears only once
	uniqueRoutes := make(map[string]RouteCandidate)

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

		fileRoutes, err := scanFileForRoutes(path, re, processor, minContextLines, maxContextLines)
		if err != nil {
			return err
		}

		// Add routes to map - keep all routes since TUI will handle display deduplication
		for _, route := range fileRoutes {
			// Create unique key combining method, path, file, and line for true uniqueness
			key := fmt.Sprintf("%s %s %s:%d", route.Method, route.Path, route.File, route.Line)
			uniqueRoutes[key] = route
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Convert map back to slice
	routes := make([]RouteCandidate, 0, len(uniqueRoutes))
	for _, route := range uniqueRoutes {
		routes = append(routes, route)
	}

	return routes, nil
}

func scanFileForRoutes(filePath string, re *regexp.Regexp, processor RouteProcessor, minContextLines, maxContextLines int) ([]RouteCandidate, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var routes []RouteCandidate
	lines := strings.Split(string(content), "\n")
	fullContent := string(content)

	matches := re.FindAllStringSubmatch(fullContent, -1)
	matchIndices := re.FindAllStringSubmatchIndex(fullContent, -1)

	for i, match := range matches {
		// Calculate line number and context
		matchStart := matchIndices[i][0]
		lineNum := strings.Count(fullContent[:matchStart], "\n") + 1
		contextBefore := int(math.Max(0, float64(lineNum-1-minContextLines)))
		contextAfter := int(math.Min(float64(len(lines)), float64(lineNum-1+maxContextLines+1)))
		context := strings.Join(lines[contextBefore:contextAfter], "\n")

		// Handle Django empty path case: check if this is path('') by looking at the source
		var routeMatch RouteMatch
		if len(match) < 2 && strings.Contains(match[0], "path('") {
			matchEnd := matchIndices[i][1]
			// Check if the next few characters contain '',) which indicates empty path
			if matchEnd < len(fullContent) {
				nextChars := fullContent[matchEnd : matchEnd+int(math.Min(10, float64(len(fullContent)-matchEnd)))]
				if strings.Contains(nextChars, "'',") {
					routeMatch = RouteMatch{
						FullMatch:     match[0] + "'',", // Complete the match
						CaptureGroups: []string{""},     // Empty string as capture group
						Line:          lineNum,
						Context:       context,
					}
				}
			}
		} else if len(match) >= 2 {
			// Normal case with capture groups
			routeMatch = RouteMatch{
				FullMatch:     match[0],
				CaptureGroups: match[1:], // Skip the full match
				Line:          lineNum,
				Context:       context,
			}
		}

		// Let the processor handle the language-specific logic
		if processor != nil && (len(routeMatch.CaptureGroups) > 0 || routeMatch.FullMatch != "") {
			processedRoutes := processor.ProcessMatch(routeMatch, filePath)
			routes = append(routes, processedRoutes...)
		}
	}

	return routes, nil
}

// DefaultRouteProcessor provides basic route processing for most languages (Node.js, etc.)
type DefaultRouteProcessor struct{}

func NewDefaultRouteProcessor() *DefaultRouteProcessor {
	return &DefaultRouteProcessor{}
}

func (p *DefaultRouteProcessor) ProcessMatch(match RouteMatch, filePath string) []RouteCandidate {
	// Extract method and path from capture groups
	capturedValues := make([]string, 0)
	for _, group := range match.CaptureGroups {
		if group != "" {
			capturedValues = append(capturedValues, group)
		}
	}

	if len(capturedValues) == 0 {
		return nil
	}

	method := ""
	routePath := ""

	// Try to determine which is method and which is path
	for _, val := range capturedValues {
		upperVal := strings.ToUpper(val)

		// Check if this looks like an HTTP method
		if p.isHTTPMethod(upperVal) {
			method = upperVal
		} else if p.isValidRoutePath(val) {
			routePath = val
		} else if routePath == "" && method != "" && p.isValidRoutePath(val) {
			routePath = val
		}
	}

	// If we have a path but no method, default to GET
	if method == "" && len(capturedValues) == 1 {
		routePath = capturedValues[0]
		if p.isValidRoutePath(routePath) {
			method = "GET"
		} else {
			return nil // Skip invalid route path
		}
	}

	// Validate the final route
	if routePath == "" || !p.isValidRoutePath(routePath) {
		return nil
	}

	return []RouteCandidate{{
		Method:  method,
		Path:    routePath,
		File:    filePath,
		Line:    match.Line,
		Context: match.Context,
	}}
}

// isHTTPMethod checks if a string is a valid HTTP method
func (p *DefaultRouteProcessor) isHTTPMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "ALL":
		return true
	default:
		return false
	}
}

// isValidRoutePath checks if a string looks like a valid HTTP route path
func (p *DefaultRouteProcessor) isValidRoutePath(path string) bool {
	if path == "" {
		return false // Empty paths not allowed in default processor
	}

	// Must start with / for absolute paths
	if !strings.HasPrefix(path, "/") {
		return false
	}

	// Must not contain spaces (route paths shouldn't have spaces)
	if strings.Contains(path, " ") {
		return false
	}

	// Should not be extremely long (likely not a route)
	if len(path) > 100 {
		return false
	}

	return true
}
