package analyzer

import (
	"os"
	"testing"
)

func elixirAnalyzer(dir string) *ElixirAnalyzer {
	return NewElixirAnalyzer(projectFS{FS: os.DirFS(dir), rootPath: dir}).(*ElixirAnalyzer)
}

func TestElixirAnalyzer_CanHandle(t *testing.T) {
	t.Run("mix.exs present", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"mix.exs": "defmodule App.MixProject do\nend\n"})
		if ok, err := elixirAnalyzer(dir).CanHandle(); err != nil || !ok {
			t.Fatalf("CanHandle = %v, %v; want true", ok, err)
		}
	})
	t.Run("bare .ex file is NOT enough", func(t *testing.T) {
		// A stray .ex/.exs isn't a deployable project; requiring mix.exs avoids mislabeling
		// non-Elixir repos that happen to carry an incidental script.
		dir := writeRubyFixture(t, map[string]string{"script.exs": "IO.puts(\"hi\")\n"})
		if ok, _ := elixirAnalyzer(dir).CanHandle(); ok {
			t.Error("a bare .ex/.exs file (no mix.exs) should NOT be handled as Elixir")
		}
	})
}

func TestElixirAnalyzer_Phoenix(t *testing.T) {
	dir := writeRubyFixture(t, map[string]string{
		"mix.exs": `defmodule WidgetApp.MixProject do
  use Mix.Project

  def project do
    [
      app: :widget_app,
      version: "0.1.0",
      elixir: "~> 1.17",
      deps: deps()
    ]
  end

  def application do
    [mod: {WidgetApp.Application, []}]
  end

  defp deps do
    [
      {:phoenix, "~> 1.7.0"},
      {:phoenix_ecto, "~> 4.4"},
      {:ecto_sql, "~> 3.10"},
      {:postgrex, ">= 0.0.0"},
      {:redix, "~> 1.2"},
      {:bandit, "~> 1.2"}
    ]
  end
end
`,
		"lib/widget_app_web/router.ex": `defmodule WidgetAppWeb.Router do
  use WidgetAppWeb, :router

  scope "/", WidgetAppWeb do
    get "/health", PageController, :health
    post "/widgets", WidgetController, :create
    live "/dashboard", DashboardLive
  end
end
`,
		"lib/widget_app/config.ex": `defmodule WidgetApp.Config do
  def api_key, do: System.get_env("API_KEY")
  def db_url, do: System.fetch_env!("DATABASE_URL")
end
`,
		"priv/repo/migrations/20230101000000_create_widgets.exs": "defmodule WidgetApp.Repo.Migrations.CreateWidgets do\nend\n",
	})

	spec, err := elixirAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Language != "elixir" {
		t.Errorf("Language = %q, want elixir", spec.Language)
	}
	// The app name (from mix.exs `app:`) must be the OTP app atom — it drives the release copy path.
	if spec.Name != "widget_app" {
		t.Errorf("Name = %q, want widget_app (mix.exs app:)", spec.Name)
	}
	if !hasFramework(spec.ServiceRequirements, "phoenix") {
		t.Errorf("expected a phoenix framework marker; got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServicePostgres) {
		t.Errorf("ecto_sql/postgrex should imply Postgres; got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServiceRedis) {
		t.Errorf("redix should imply Redis; got %+v", spec.ServiceRequirements)
	}
	if !hasEnv(spec.EnvVars, "API_KEY") || !hasEnv(spec.EnvVars, "DATABASE_URL") {
		t.Errorf("expected System.get_env and System.fetch_env! vars; got %+v", spec.EnvVars)
	}
	if !hasRoute(spec.Routes, "GET", "/health") {
		t.Errorf("expected GET /health; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "POST", "/widgets") {
		t.Errorf("expected POST /widgets; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "GET", "/dashboard") {
		t.Errorf("expected live route to map to GET /dashboard; got %+v", spec.Routes)
	}
	// A Phoenix web app must not be mislabeled a worker.
	if spec.DetectedShape == "worker" {
		t.Errorf("a Phoenix web app should not be detected as a worker")
	}
	// Ecto migrations run via the release's bin/migrate, not a Mix command, so no MigrationCommand.
	if spec.MigrationCommand != "" {
		t.Errorf("expected empty MigrationCommand (Ecto runs via bin/migrate); got %q", spec.MigrationCommand)
	}
	// Ecto (with priv/repo/migrations present) should be reported as a configured migration tool.
	foundEcto := false
	for _, tool := range spec.MigrationContext.ORMTools {
		if tool == "ecto" || tool == "ecto_sql" {
			foundEcto = true
		}
	}
	if !foundEcto {
		t.Errorf("expected ecto in configured ORM tools; got %+v", spec.MigrationContext.ORMTools)
	}
}

func TestElixirAnalyzer_PlugOnly(t *testing.T) {
	dir := writeRubyFixture(t, map[string]string{
		"mix.exs": `defmodule Thin.MixProject do
  use Mix.Project

  def project do
    [app: :thin, version: "0.1.0", deps: deps()]
  end

  defp deps do
    [
      {:plug_cowboy, "~> 2.6"}
    ]
  end
end
`,
		"lib/thin.ex": `defmodule Thin do
  use Plug.Router
  plug :match
  plug :dispatch

  get "/" do
    send_resp(conn, 200, "ok")
  end
end
`,
	})

	spec, err := elixirAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Name != "thin" {
		t.Errorf("Name = %q, want thin", spec.Name)
	}
	if !hasFramework(spec.ServiceRequirements, "plug") {
		t.Errorf("expected a plug framework marker; got %+v", spec.ServiceRequirements)
	}
	if hasFramework(spec.ServiceRequirements, "phoenix") {
		t.Errorf("a Plug-only app must not be detected as Phoenix")
	}
	if !hasRoute(spec.Routes, "GET", "/") {
		t.Errorf("expected GET /; got %+v", spec.Routes)
	}
	if spec.DetectedShape == "worker" {
		t.Errorf("a Plug web app should not be detected as a worker")
	}
}

// The Elixir analyzer must not claim a Node/Python/Go/Ruby/Rust/Java/C# project.
func TestElixirAnalyzer_NoFalseMatch(t *testing.T) {
	cases := map[string]map[string]string{
		"node":   {"package.json": `{"name":"x"}`, "index.js": "console.log(1)"},
		"python": {"requirements.txt": "flask\n", "app.py": "print(1)"},
		"go":     {"go.mod": "module x\n", "main.go": "package main\nfunc main(){}"},
		"ruby":   {"Gemfile": "source 'https://rubygems.org'\n", "app.rb": "puts 1"},
		"rust":   {"Cargo.toml": "[package]\nname = \"x\"\n", "src/main.rs": "fn main(){}"},
		"java":   {"pom.xml": "<project></project>", "App.java": "class App {}"},
		"csharp": {"App.csproj": "<Project></Project>", "Program.cs": "class P {}"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeRubyFixture(t, files)
			if ok, _ := elixirAnalyzer(dir).CanHandle(); ok {
				t.Errorf("Elixir analyzer should not handle a %s project", name)
			}
		})
	}
}
