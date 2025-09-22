package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown/v2"
	"golang.org/x/net/html"
)

const (
	// urlCacheTTL defines how long URL content is cached
	urlCacheTTL = 24 * time.Hour // Cache URLs for 24 hours
)

// getCacheDir returns the cache directory path (~/.prod/cache)
func getCacheDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	cacheDir := filepath.Join(homeDir, ".prod", "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}
	return cacheDir, nil
}

// getCacheFileName generates a cache file name based on URL hash and content type
func getCacheFileName(url, extension string) string {
	hash := sha256.Sum256([]byte(url))
	return hex.EncodeToString(hash[:]) + "." + extension
}

// isCacheValid checks if the cache file exists and is not expired
func isCacheValid(filePath string) bool {
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < urlCacheTTL
}

// fetchURL is a generic function to fetch URL content with caching
func fetchURL(url, extension string, processor func(string) (string, error)) (string, error) {
	// Get cache directory and file path
	cacheDir, err := getCacheDir()
	if err != nil {
		// If cache setup fails, fallback to direct fetch
		return fetchURLDirect(url, processor)
	}

	cacheFile := filepath.Join(cacheDir, getCacheFileName(url, extension))

	// Check if we have a valid cached version
	if isCacheValid(cacheFile) {
		cached, err := os.ReadFile(cacheFile)
		if err == nil {
			return string(cached), nil
		}
		// If reading cache fails, continue to fetch fresh
	}

	// Fetch fresh content
	content, err := fetchURLDirect(url, processor)
	if err != nil {
		return "", err
	}

	// Save to cache (ignore errors to not break functionality)
	_ = os.WriteFile(cacheFile, []byte(content), 0644)

	return content, nil
}

// FetchURL fetches raw HTML content from a URL with caching
func FetchURL(url string) (string, error) {
	return fetchURL(url, "html", func(content string) (string, error) {
		return content, nil // No processing for raw content
	})
}

// FetchURLAsMarkdown fetches a URL and converts it to markdown with filesystem caching
func FetchURLAsMarkdown(url string) (string, error) {
	return FetchURLAsMarkdownWithOptions(url, false)
}

// FetchURLAsMarkdownWithOptions fetches a URL and converts it to markdown with optional HTML cleaning
func FetchURLAsMarkdownWithOptions(url string, cleanHTML bool) (string, error) {
	return fetchURL(url, "md", func(content string) (string, error) {
		processedContent := content

		// Clean HTML to remove hidden elements if requested
		if cleanHTML {
			cleanedHTML, err := CleanVisibleHTML(content)
			if err != nil {
				// If cleaning fails, proceed with original content
				processedContent = content
			} else {
				processedContent = cleanedHTML
			}
		}

		return md.ConvertString(processedContent)
	})
}

// fetchURLDirect performs the fetch logic without caching and applies a processor function
func fetchURLDirect(url string, processor func(string) (string, error)) (string, error) {
	// Fetch the page
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read body: %w", err)
	}

	// Apply processor function to transform content
	if processor != nil {
		return processor(string(body))
	}

	return string(body), nil
}

// CleanVisibleHTML removes hidden or deprecated elements from HTML before markdown conversion.
func CleanVisibleHTML(input string) (string, error) {
	// Parse the HTML
	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return "", err
	}

	// Remove hidden elements
	removeHiddenElements(doc)

	// Render back to string
	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// removeHiddenElements recursively removes hidden elements from the HTML tree
func removeHiddenElements(n *html.Node) {
	// Process children first (reverse order to avoid issues with removal)
	var children []*html.Node
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		children = append(children, child)
	}

	for _, child := range children {
		removeHiddenElements(child)
	}

	// Check if current node should be removed
	if n.Type == html.ElementNode && shouldRemoveElement(n) {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}
}

// shouldRemoveElement checks if an element should be removed based on hiding attributes
func shouldRemoveElement(n *html.Node) bool {
	for _, attr := range n.Attr {
		switch strings.ToLower(attr.Key) {
		case "style":
			lowerStyle := strings.ToLower(attr.Val)
			if strings.Contains(lowerStyle, "display:none") ||
				strings.Contains(lowerStyle, "visibility:hidden") {
				return true
			}
		case "aria-hidden":
			if attr.Val == "true" {
				return true
			}
		case "class":
			lowerClass := strings.ToLower(attr.Val)
			if strings.Contains(lowerClass, "hidden") ||
				strings.Contains(lowerClass, "sr-only") {
				return true
			}
		}
	}
	return false
}
