package tokens

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/go-errors/errors"
)

// PurchaseSession represents an active token purchase flow
type PurchaseSession struct {
	manager  *Manager
	ctx      context.Context
	out      io.Writer
	packages []TokenPackage
}

// NewPurchaseSession creates a new purchase session and displays available packages
func NewPurchaseSession(manager *Manager, ctx context.Context, out io.Writer) (*PurchaseSession, error) {
	ps := &PurchaseSession{
		manager: manager,
		ctx:     ctx,
		out:     out,
	}

	// Display packages immediately
	packages, err := ps.displayPackages()
	if err != nil {
		return nil, err
	}

	ps.packages = packages
	return ps, nil
}

// displayPackages shows available token packages to the user
func (ps *PurchaseSession) displayPackages() ([]TokenPackage, error) {
	fmt.Fprint(ps.out, "\n💎 Token Packages Available:\n")
	fmt.Fprint(ps.out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	packages, err := ps.manager.client.GetPackages(ps.ctx)
	if err != nil {
		fmt.Fprintf(ps.out, "❌ Failed to fetch token packages: %v\n", err)
		return nil, err
	}

	if len(packages) == 0 {
		fmt.Fprint(ps.out, "No token packages available at this time.\n")
		return nil, fmt.Errorf("no packages available")
	}

	// Display packages
	for i, pkg := range packages {
		if !pkg.Active {
			continue
		}
		fmt.Fprintf(ps.out, "\n%d. %s - %d tokens for $%.2f\n",
			i+1,
			pkg.Name,
			pkg.TokenCount,
			pkg.PriceDollars(),
		)
		if pkg.Description != "" {
			fmt.Fprintf(ps.out, "   %s\n", pkg.Description)
		}
	}

	fmt.Fprint(ps.out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	return packages, nil
}

// GetPackageOptions returns display options for package selection
func (ps *PurchaseSession) GetPackageOptions() []string {
	options := make([]string, 0)
	for _, pkg := range ps.packages {
		if pkg.Active {
			options = append(options, fmt.Sprintf("%s - %d tokens for $%.2f",
				pkg.Name,
				pkg.TokenCount,
				pkg.PriceDollars(),
			))
		}
	}
	return options
}

// ProcessSelection parses the user's selection and returns the selected package
func (ps *PurchaseSession) ProcessSelection(selection string) (*TokenPackage, error) {
	selection = strings.TrimSpace(selection)

	// Filter active packages
	activePackages := make([]TokenPackage, 0)
	for _, pkg := range ps.packages {
		if pkg.Active {
			activePackages = append(activePackages, pkg)
		}
	}

	// Parse selection index (TUI sends 0-based cursor index)
	var selectedIndex int
	if _, err := fmt.Sscanf(selection, "%d", &selectedIndex); err != nil {
		return nil, fmt.Errorf("invalid selection format")
	}

	// Validate index
	if selectedIndex < 0 || selectedIndex >= len(activePackages) {
		return nil, fmt.Errorf("selection out of range")
	}

	selectedPackage := activePackages[selectedIndex]

	fmt.Fprintf(ps.out, "\n🛒 You selected: %s (%d tokens for $%.2f)\n",
		selectedPackage.Name,
		selectedPackage.TokenCount,
		selectedPackage.PriceDollars(),
	)

	return &selectedPackage, nil
}

// CreateCheckout creates a Stripe checkout session and opens it in the browser
func (ps *PurchaseSession) CreateCheckout(packageID string) (string, error) {
	fmt.Fprint(ps.out, "🔗 Creating checkout session...\n")

	// Get access token
	accessToken, err := ps.manager.getAccessToken()
	if err != nil {
		return "", errors.WrapPrefix(err, "failed to get access token", 0)
	}

	// Start local HTTP server for success/cancel pages
	successURL := "http://localhost:8082/purchase-success"
	cancelURL := "http://localhost:8082/purchase-cancel"

	// Create purchase handlers with dependencies
	purchaseHandlers := &PurchaseHandlers{
		TokenClient: ps.manager.client,
		UIOutput:    ps.manager.uiOutput,
	}

	server := &http.Server{Addr: ":8082"}
	http.HandleFunc("/purchase-success", func(w http.ResponseWriter, r *http.Request) {
		purchaseHandlers.ServePurchaseSuccess(w, r, ps.ctx)
	})
	http.HandleFunc("/purchase-cancel", func(w http.ResponseWriter, r *http.Request) {
		ServePurchaseCancel(w, r)
	})

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Debug("purchase callback server error", "error", err)
		}
	}()

	// Shutdown server after 5 minutes (cleanup)
	go func() {
		time.Sleep(5 * time.Minute)
		server.Shutdown(context.Background())
	}()

	// Call the stripe-checkout Edge Function
	checkoutURL := ps.manager.supabaseURL + "/functions/v1/stripe-checkout"

	reqBody := map[string]any{
		"package_id":  packageID,
		"success_url": successURL,
		"cancel_url":  cancelURL,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", errors.WrapPrefix(err, "failed to marshal request", 0)
	}

	req, err := http.NewRequestWithContext(ps.ctx, "POST", checkoutURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", errors.WrapPrefix(err, "failed to create request", 0)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.WrapPrefix(err, "failed to execute request", 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", errors.Errorf("checkout request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		URL string `json:"url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", errors.WrapPrefix(err, "failed to decode response", 0)
	}

	fmt.Fprint(ps.out, "\n✅ Checkout session created!\n")
	fmt.Fprint(ps.out, "🌐 Opening browser to complete purchase...\n\n")

	// Open browser
	if err := openInBrowser(result.URL); err != nil {
		fmt.Fprintf(ps.out, "⚠️  Could not open browser automatically.\n")
		fmt.Fprintf(ps.out, "Please visit this URL to complete your purchase:\n%s\n\n", result.URL)
	} else {
		fmt.Fprintf(ps.out, "🔗 Checkout URL: %s\n\n", result.URL)
	}

	fmt.Fprint(ps.out, "💡 After completing payment:\n")
	fmt.Fprint(ps.out, "   • Your tokens will be credited automatically within seconds\n")
	fmt.Fprint(ps.out, "   • Your balance will update in the status bar as soon as tokens are added\n")
	fmt.Fprint(ps.out, "   • You can continue using the CLI while the purchase processes\n\n")

	return result.URL, nil
}

// openInBrowser opens the specified URL in the default browser
func openInBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default: // linux, freebsd, openbsd, netbsd
		cmd = "xdg-open"
		args = []string{url}
	}

	return exec.Command(cmd, args...).Start()
}
