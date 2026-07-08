package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRubyFixture(t *testing.T, files map[string]string) string {
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

func rubyAnalyzer(dir string) *RubyAnalyzer {
	return NewRubyAnalyzer(projectFS{FS: os.DirFS(dir), rootPath: dir}).(*RubyAnalyzer)
}

func hasService(svcs []ServiceRequirement, want ServiceRequirement) bool {
	for _, s := range svcs {
		if s == want {
			return true
		}
	}
	return false
}

func hasFramework(svcs []ServiceRequirement, provider string) bool {
	for _, s := range svcs {
		if s.Type == "framework" && s.Provider == provider {
			return true
		}
	}
	return false
}

func hasRoute(routes []RouteCandidate, method, path string) bool {
	for _, r := range routes {
		if r.Method == method && r.Path == path {
			return true
		}
	}
	return false
}

func hasEnv(vars []EnvVarCandidate, name string) bool {
	for _, v := range vars {
		if v.VarName == name {
			return true
		}
	}
	return false
}

func TestRubyAnalyzer_CanHandle(t *testing.T) {
	t.Run("Gemfile present", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"Gemfile": "source 'https://rubygems.org'\n"})
		if ok, err := rubyAnalyzer(dir).CanHandle(); err != nil || !ok {
			t.Fatalf("CanHandle = %v, %v; want true", ok, err)
		}
	})
	t.Run("gemspec present", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"mygem.gemspec": "Gem::Specification.new\n"})
		if ok, _ := rubyAnalyzer(dir).CanHandle(); !ok {
			t.Error("a *.gemspec should be handled")
		}
	})
	t.Run("bare .rb file", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"app.rb": "puts 'hi'\n"})
		if ok, _ := rubyAnalyzer(dir).CanHandle(); !ok {
			t.Error("a bare .rb file should be handled")
		}
	})
	t.Run("no ruby files", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"README.md": "hi"})
		if ok, _ := rubyAnalyzer(dir).CanHandle(); ok {
			t.Error("a non-Ruby project should not be handled")
		}
	})
}

func TestRubyAnalyzer_Rails(t *testing.T) {
	dir := writeRubyFixture(t, map[string]string{
		"Gemfile": `source 'https://rubygems.org'
gem 'rails', '~> 7.1'
gem 'pg'
gem 'redis'
gem 'puma'
`,
		"config/application.rb": `require_relative "boot"
module WidgetApp
  class Application < Rails::Application
  end
end
`,
		"config/routes.rb": `Rails.application.routes.draw do
  root "home#index"
  get "/health", to: "health#show"
  resources :users
end
`,
		"app/models/config.rb": `class Config
  API_KEY = ENV["API_KEY"]
  DB = ENV.fetch("DATABASE_URL")
end
`,
		"db/migrate/20230101000000_create_users.rb": "class CreateUsers < ActiveRecord::Migration[7.1]\nend\n",
	})

	spec, err := rubyAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Language != "ruby" {
		t.Errorf("Language = %q, want ruby", spec.Language)
	}
	if spec.Name != "WidgetApp" {
		t.Errorf("Name = %q, want WidgetApp (Rails module name)", spec.Name)
	}
	if !hasFramework(spec.ServiceRequirements, "rails") {
		t.Errorf("expected a rails framework marker; got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServicePostgres) {
		t.Errorf("pg gem should imply Postgres; got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServiceRedis) {
		t.Errorf("redis gem should imply Redis; got %+v", spec.ServiceRequirements)
	}
	if spec.MigrationCommand == "" {
		t.Errorf("Rails app should have a migration command")
	}
	if !hasEnv(spec.EnvVars, "API_KEY") || !hasEnv(spec.EnvVars, "DATABASE_URL") {
		t.Errorf("expected ENV[] and ENV.fetch vars; got %+v", spec.EnvVars)
	}
	if !hasRoute(spec.Routes, "GET", "/") {
		t.Errorf("root route should map to GET /; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "GET", "/health") {
		t.Errorf("expected GET /health; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "GET", "/users") {
		t.Errorf("resources :users should yield GET /users; got %+v", spec.Routes)
	}
	// A Rails web app must not be mislabeled a worker.
	if spec.DetectedShape == "worker" {
		t.Errorf("a Rails web app should not be detected as a worker")
	}
	// ActiveRecord migrations under db/migrate should be recognized as a configured tool.
	foundTool := false
	for _, tool := range spec.MigrationContext.ORMTools {
		if tool == "rails" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("expected rails in configured ORM tools; got %+v", spec.MigrationContext.ORMTools)
	}
}

func TestRubyAnalyzer_Sinatra(t *testing.T) {
	dir := writeRubyFixture(t, map[string]string{
		"Gemfile": `source 'https://rubygems.org'
gem 'sinatra'
gem 'mysql2'
`,
		"app.rb": `require 'sinatra'

get '/' do
  'hello'
end

post '/webhook' do
  ENV['SECRET_TOKEN']
end
`,
	})

	spec, err := rubyAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Language != "ruby" {
		t.Errorf("Language = %q, want ruby", spec.Language)
	}
	if !hasFramework(spec.ServiceRequirements, "sinatra") {
		t.Errorf("expected a sinatra framework marker; got %+v", spec.ServiceRequirements)
	}
	if hasFramework(spec.ServiceRequirements, "rails") {
		t.Errorf("a Sinatra app must not be detected as Rails")
	}
	if !hasService(spec.ServiceRequirements, ServiceMySQL) {
		t.Errorf("mysql2 gem should imply MySQL; got %+v", spec.ServiceRequirements)
	}
	if !hasRoute(spec.Routes, "GET", "/") {
		t.Errorf("expected GET /; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "POST", "/webhook") {
		t.Errorf("expected POST /webhook; got %+v", spec.Routes)
	}
	if !hasEnv(spec.EnvVars, "SECRET_TOKEN") {
		t.Errorf("expected SECRET_TOKEN env var; got %+v", spec.EnvVars)
	}
	if spec.StartCommand == "" {
		t.Errorf("Sinatra app should have a start command")
	}
}

// The Ruby analyzer must not claim a Node/Python/Go project.
func TestRubyAnalyzer_NoFalseMatch(t *testing.T) {
	cases := map[string]map[string]string{
		"node":   {"package.json": `{"name":"x"}`, "index.js": "console.log(1)"},
		"python": {"requirements.txt": "flask\n", "app.py": "print(1)"},
		"go":     {"go.mod": "module x\n", "main.go": "package main\nfunc main(){}"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeRubyFixture(t, files)
			if ok, _ := rubyAnalyzer(dir).CanHandle(); ok {
				t.Errorf("Ruby analyzer should not handle a %s project", name)
			}
		})
	}
}
