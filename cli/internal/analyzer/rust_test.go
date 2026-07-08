package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRustFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func rustAnalyzer(dir string) *RustAnalyzer {
	return NewRustAnalyzer(projectFS{FS: os.DirFS(dir), rootPath: dir}).(*RustAnalyzer)
}

func TestRustAnalyzer_CanHandle(t *testing.T) {
	t.Run("Cargo.toml present", func(t *testing.T) {
		dir := writeRustFixture(t, map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n"})
		if ok, err := rustAnalyzer(dir).CanHandle(); err != nil || !ok {
			t.Fatalf("CanHandle = %v, %v; want true", ok, err)
		}
	})
	t.Run("bare .rs file is NOT enough", func(t *testing.T) {
		// A stray Rust source file isn't a deployable crate; requiring Cargo.toml avoids
		// mislabeling non-Rust repos that happen to carry an incidental .rs.
		dir := writeRustFixture(t, map[string]string{"main.rs": "fn main() {}\n"})
		if ok, _ := rustAnalyzer(dir).CanHandle(); ok {
			t.Error("a bare .rs file (no Cargo.toml) should NOT be handled as Rust")
		}
	})
	t.Run("no rust files", func(t *testing.T) {
		dir := writeRustFixture(t, map[string]string{"README.md": "hi"})
		if ok, _ := rustAnalyzer(dir).CanHandle(); ok {
			t.Error("a non-Rust project should not be handled")
		}
	})
}

func TestRustAnalyzer_Axum(t *testing.T) {
	dir := writeRustFixture(t, map[string]string{
		"Cargo.toml": `[package]
name = "widget-api"
version = "0.1.0"
edition = "2021"

[[bin]]
name = "server"

[dependencies]
axum = "0.7"
tokio = { version = "1", features = ["full"] }
sqlx = { version = "0.7", features = ["postgres", "runtime-tokio"] }
redis = "0.25"
`,
		"src/main.rs": `use axum::{routing::get, Router};

#[tokio::main]
async fn main() {
    let db = std::env::var("DATABASE_URL").unwrap();
    let secret = env::var("API_KEY").unwrap();
    let app = Router::new()
        .route("/", get(root))
        .route("/health", get(health));
    let _ = (app, db, secret);
}
`,
		"migrations/20230101000000_init.sql": "CREATE TABLE users (id serial);\n",
	})

	spec, err := rustAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Language != "rust" {
		t.Errorf("Language = %q, want rust", spec.Language)
	}
	if spec.Name != "server" {
		t.Errorf("Name = %q, want server ([[bin]] name wins)", spec.Name)
	}
	if !hasFramework(spec.ServiceRequirements, "axum") {
		t.Errorf("expected an axum framework marker; got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServicePostgres) {
		t.Errorf("sqlx crate should imply Postgres; got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServiceRedis) {
		t.Errorf("redis crate should imply Redis; got %+v", spec.ServiceRequirements)
	}
	if !hasEnv(spec.EnvVars, "DATABASE_URL") || !hasEnv(spec.EnvVars, "API_KEY") {
		t.Errorf("expected std::env::var and env::var vars; got %+v", spec.EnvVars)
	}
	if !hasRoute(spec.Routes, "GET", "/") {
		t.Errorf("expected GET /; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "GET", "/health") {
		t.Errorf("expected GET /health; got %+v", spec.Routes)
	}
	// A web app must not be mislabeled a worker.
	if spec.DetectedShape == "worker" {
		t.Errorf("an axum web app should not be detected as a worker")
	}
	// A SQLx migrations/ directory should be recognized as a configured tool.
	foundTool := false
	for _, tool := range spec.MigrationContext.ORMTools {
		if tool == "sqlx" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("expected sqlx in configured ORM tools; got %+v", spec.MigrationContext.ORMTools)
	}
}

func TestRustAnalyzer_Actix(t *testing.T) {
	dir := writeRustFixture(t, map[string]string{
		"Cargo.toml": `[package]
name = "actix-svc"
version = "0.1.0"

[dependencies]
actix-web = "4"
tokio-postgres = "0.7"
`,
		"src/main.rs": `use actix_web::{get, post, web, App, HttpServer, Responder};

#[get("/")]
async fn index() -> impl Responder { "hi" }

#[post("/webhook")]
async fn webhook() -> impl Responder {
    let _ = std::env::var("SECRET_TOKEN");
    "ok"
}
`,
	})

	spec, err := rustAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Language != "rust" {
		t.Errorf("Language = %q, want rust", spec.Language)
	}
	if spec.Name != "actix-svc" {
		t.Errorf("Name = %q, want actix-svc ([package] name)", spec.Name)
	}
	if !hasFramework(spec.ServiceRequirements, "actix") {
		t.Errorf("expected an actix framework marker; got %+v", spec.ServiceRequirements)
	}
	if hasFramework(spec.ServiceRequirements, "axum") {
		t.Errorf("an actix app must not be detected as axum")
	}
	if !hasService(spec.ServiceRequirements, ServicePostgres) {
		t.Errorf("tokio-postgres crate should imply Postgres; got %+v", spec.ServiceRequirements)
	}
	if !hasRoute(spec.Routes, "GET", "/") {
		t.Errorf("expected GET / from #[get]; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "POST", "/webhook") {
		t.Errorf("expected POST /webhook from #[post]; got %+v", spec.Routes)
	}
	if !hasEnv(spec.EnvVars, "SECRET_TOKEN") {
		t.Errorf("expected SECRET_TOKEN env var; got %+v", spec.EnvVars)
	}
}

// The Rust analyzer must not claim a Node/Python/Go/Ruby project.
func TestRustAnalyzer_NoFalseMatch(t *testing.T) {
	cases := map[string]map[string]string{
		"node":   {"package.json": `{"name":"x"}`, "index.js": "console.log(1)"},
		"python": {"requirements.txt": "flask\n", "app.py": "print(1)"},
		"go":     {"go.mod": "module x\n", "main.go": "package main\nfunc main(){}"},
		"ruby":   {"Gemfile": "source 'https://rubygems.org'\n", "app.rb": "puts 1"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeRustFixture(t, files)
			if ok, _ := rustAnalyzer(dir).CanHandle(); ok {
				t.Errorf("Rust analyzer should not handle a %s project", name)
			}
		})
	}
}
