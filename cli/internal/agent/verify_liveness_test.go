package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// Worker/cron shapes must skip the HTTP probe — otherwise a healthy non-HTTP
// deploy is auto-failed (and auto-rolled-back) by isURLLive. A dead URL would
// error if probed, so returning nil proves it was skipped.
func TestVerifyLivenessSkipsNonHTTPShapes(t *testing.T) {
	a := &Activities{uiWriter: output.NewNoOpWriter()}
	for _, shape := range []deployment.DeployShape{deployment.ShapeWorker, deployment.ShapeCron} {
		if err := a.verifyLiveness(context.Background(), shape, "http://127.0.0.1:1/dead"); err != nil {
			t.Errorf("%s should skip the HTTP probe, got %v", shape, err)
		}
	}
}

// web / mcp-server (and any unset shape, treated as web) are URL-probed: a live
// URL passes, a 5xx fails (which drives the existing auto-rollback).
func TestVerifyLivenessProbesHTTPShapes(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer up.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer down.Close()

	a := &Activities{uiWriter: output.NewNoOpWriter()}
	for _, shape := range []deployment.DeployShape{deployment.ShapeWeb, deployment.ShapeMCPServer, ""} {
		if err := a.verifyLiveness(context.Background(), shape, up.URL); err != nil {
			t.Errorf("%q against a live URL should pass, got %v", shape, err)
		}
		if err := a.verifyLiveness(context.Background(), shape, down.URL); err == nil {
			t.Errorf("%q against a 500 URL should fail (→ rollback), got nil", shape)
		}
	}
}
