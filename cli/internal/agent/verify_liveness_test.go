package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// ACD.1: an app that *responds* is live — even behind an auth wall (401/403), a
// redirect to a login page, or a 404. Only connection failures, timeouts, and 5xx are
// not-live. (Previously any status >300 was treated as a failed deploy.)
func TestIsURLLiveAuthAndRedirectAreLive(t *testing.T) {
	a := &Activities{uiWriter: output.NewNoOpWriter()}

	for name, code := range map[string]int{
		"200 OK": http.StatusOK, "401 auth wall": http.StatusUnauthorized,
		"403 forbidden": http.StatusForbidden, "404 no root route": http.StatusNotFound,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		if err := a.isURLLive(context.Background(), srv.URL); err != nil {
			t.Errorf("%s should be live, got %v", name, err)
		}
		srv.Close()
	}

	// A 302 to a login page is a live app — and the redirect must actually be FOLLOWED
	// (not just judged as "302 < 500"), so assert the login handler ran.
	var hitLogin atomic.Bool
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitLogin.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer login.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, login.URL, http.StatusFound)
	}))
	defer redir.Close()
	if err := a.isURLLive(context.Background(), redir.URL); err != nil {
		t.Errorf("a 302→login should be live, got %v", err)
	}
	if !hitLogin.Load() {
		t.Error("the redirect to the login page should have been followed")
	}

	// 5xx (broken app) and an unreachable host are NOT live.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer broken.Close()
	if err := a.isURLLive(context.Background(), broken.URL); err == nil {
		t.Error("a 502 should be not-live")
	}
	if err := a.isURLLive(context.Background(), "http://127.0.0.1:1/dead"); err == nil {
		t.Error("an unreachable URL should be not-live")
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
