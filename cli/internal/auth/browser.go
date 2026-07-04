package auth

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/go-errors/errors"
)

// Embed all assets as a filesystem
//
//go:embed assets/*.html
var assetsFS embed.FS

// TemplateData holds the data for template replacement
type TemplateData struct {
	SupabaseURL     string
	SupabaseAnonKey string
}

// LoginWithBrowser performs browser-based authentication using embedded HTML
func (sa *SupabaseAuth) LoginWithBrowser(ctx context.Context) error {
	// Start local callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errors.Errorf("failed to start callback server: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	serverURL := fmt.Sprintf("http://localhost:%d", port)

	// Create channels for communication
	sessionChan := make(chan *Session, 1)
	errorChan := make(chan error, 1)

	// Setup HTTP server with embedded templates
	server := &http.Server{
		Handler: sa.createHandler(sessionChan, errorChan),
	}

	// Start server in background
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errorChan <- errors.Errorf("server error: %w", err)
		}
	}()
	defer server.Shutdown(ctx)

	// Open browser to local login page
	loginURL := fmt.Sprintf("%s/login", serverURL)

	fmt.Fprintln(sa.out, "🌐 Opening browser for authentication...")
	fmt.Fprintln(sa.out, "If the browser doesn't open automatically, please visit:")
	fmt.Fprintf(sa.out, "  %s\n\n", loginURL)

	if err := openBrowser(loginURL); err != nil {
		fmt.Fprintf(sa.out, "⚠️  Could not open browser automatically: %v\n", err)
	}

	// Wait for authentication
	fmt.Fprintln(sa.out, "⏳ Waiting for authentication...")

	select {
	case session := <-sessionChan:
		// Save session
		if err := sa.store.SaveSession(session); err != nil {
			return errors.Errorf("failed to save session: %w", err)
		}

		fmt.Fprintln(sa.out, "\n✅ Authentication successful!")
		if session.User != nil && session.User.Email != "" {
			fmt.Fprintf(sa.out, "👤 Logged in as: %s\n", session.User.Email)
		}
		return nil

	case err := <-errorChan:
		return errors.Errorf("authentication failed: %w", err)

	case <-ctx.Done():
		return errors.Errorf("authentication cancelled")

	case <-time.After(5 * time.Minute):
		return errors.Errorf("authentication timeout")
	}
}

// createHandler creates the HTTP handler for the auth pages
func (sa *SupabaseAuth) createHandler(sessionChan chan *Session, errorChan chan error) http.Handler {
	// Create a sub-filesystem for cleaner paths
	assets, _ := fs.Sub(assetsFS, "assets")

	// Parse all templates at once
	templates := template.Must(template.New("").Funcs(template.FuncMap{
		"config": func() TemplateData {
			return TemplateData{
				SupabaseURL:     sa.config.SupabaseURL,
				SupabaseAnonKey: sa.config.SupabaseAnonKey,
			}
		},
	}).ParseFS(assets, "*.html"))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			// Special case: Receive tokens from JavaScript
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			sa.handleAuthCallback(w, r, sessionChan, errorChan)
			return

		case "/":
			// Redirect root to login
			http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
			return

		case "/login", "/signup", "/callback", "/reset", "/reset-password":
			// Serve the appropriate HTML template
			templateName := r.URL.Path[1:] + ".html"

			var buf bytes.Buffer
			if err := templates.ExecuteTemplate(&buf, templateName, TemplateData{
				SupabaseURL:     sa.config.SupabaseURL,
				SupabaseAnonKey: sa.config.SupabaseAnonKey,
			}); err != nil {
				http.Error(w, "Template error", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(buf.Bytes())

		default:
			http.NotFound(w, r)
		}
	})
}

// handleAuthCallback handles the POST request with tokens from the browser
func (sa *SupabaseAuth) handleAuthCallback(w http.ResponseWriter, r *http.Request, sessionChan chan *Session, errorChan chan error) {
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}

	if err := json.NewDecoder(r.Body).Decode(&tokens); err != nil {
		errorChan <- errors.Errorf("failed to parse tokens: %w", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Create session
	session := &Session{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second),
	}

	// Fetch user info from Supabase using the access token
	user, err := sa.getUserInfo(tokens.AccessToken)
	if err == nil {
		session.User = user
	}

	sessionChan <- session
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success": true}`))
}

// getUserInfo fetches user information from Supabase
func (sa *SupabaseAuth) getUserInfo(accessToken string) (*User, error) {
	url := fmt.Sprintf("%s/auth/v1/user", sa.config.SupabaseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Set("apikey", sa.config.SupabaseAnonKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("failed to get user info: %s", resp.Status)
	}

	var userResp struct {
		ID           string         `json:"id"`
		Email        string         `json:"email"`
		AppMetadata  map[string]any `json:"app_metadata"`
		UserMetadata map[string]any `json:"user_metadata"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&userResp); err != nil {
		return nil, err
	}

	return &User{
		ID:    userResp.ID,
		Email: userResp.Email,
	}, nil
}

// openBrowser opens the default browser to the specified URL
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		return errors.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return exec.Command(cmd, args...).Start()
}
