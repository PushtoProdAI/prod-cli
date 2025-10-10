package tokens

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
)

//go:embed assets/*.html
var assetsFS embed.FS

// templates holds parsed HTML templates
var templates *template.Template

func init() {
	// Parse all templates at initialization
	var err error
	templates, err = template.ParseFS(assetsFS, "assets/*.html")
	if err != nil {
		panic("failed to parse token templates: " + err.Error())
	}
}

// PurchaseHandlers holds dependencies for purchase-related HTTP handlers
type PurchaseHandlers struct {
	TokenClient *Client
	UIOutput    interface{}
}

// ServePurchaseSuccess serves the purchase success page and refreshes token balance
func (h *PurchaseHandlers) ServePurchaseSuccess(w http.ResponseWriter, _ *http.Request, ctx context.Context) {
	// Fetch updated token balance
	go func() {
		summary, err := h.TokenClient.GetSummary(ctx)
		if err != nil {
			slog.Warn("Failed to get token balance after purchase", "error", err)
			return
		}

		available := summary.PlanTokens + summary.BonusTokens - summary.UsedTokens

		// Update TUI status bar immediately with the new balance
		if h.UIOutput != nil {
			if teaWriter, ok := h.UIOutput.(interface{ UpdateTokenBalance(int) }); ok {
				teaWriter.UpdateTokenBalance(available)
			}
		}
	}()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "purchase-success.html", nil); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
	w.Write(buf.Bytes())
}

// ServePurchaseCancel serves the purchase cancellation page
func ServePurchaseCancel(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "purchase-cancel.html", nil); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
	w.Write(buf.Bytes())
}
