package analyzer

import (
	"os"
	"testing"
)

func csharpAnalyzer(dir string) *CSharpAnalyzer {
	return NewCSharpAnalyzer(projectFS{FS: os.DirFS(dir), rootPath: dir}).(*CSharpAnalyzer)
}

func TestCSharpAnalyzer_CanHandle(t *testing.T) {
	t.Run("csproj present", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"App.csproj": `<Project Sdk="Microsoft.NET.Sdk"></Project>`})
		if ok, err := csharpAnalyzer(dir).CanHandle(); err != nil || !ok {
			t.Fatalf("CanHandle = %v, %v; want true", ok, err)
		}
	})
	t.Run("sln present", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"App.sln": "Microsoft Visual Studio Solution File"})
		if ok, err := csharpAnalyzer(dir).CanHandle(); err != nil || !ok {
			t.Fatalf("CanHandle = %v, %v; want true", ok, err)
		}
	})
	t.Run("bare .cs file is NOT enough", func(t *testing.T) {
		// A stray .cs isn't a deployable project; requiring a manifest avoids mislabeling non-.NET
		// repos that happen to carry an incidental C# source file.
		dir := writeRubyFixture(t, map[string]string{"Program.cs": "class Program {}"})
		if ok, _ := csharpAnalyzer(dir).CanHandle(); ok {
			t.Error("a bare .cs file (no .csproj/.sln) should NOT be handled as C#")
		}
	})
}

func TestCSharpAnalyzer_ASPNetCore(t *testing.T) {
	dir := writeRubyFixture(t, map[string]string{
		"WidgetApi.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net9.0</TargetFramework>
    <Nullable>enable</Nullable>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Npgsql" Version="8.0.3" />
    <PackageReference Include="Microsoft.EntityFrameworkCore" Version="9.0.0" />
  </ItemGroup>
</Project>
`,
		"Program.cs": `var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();

app.MapGet("/health", () =>
{
    var db = Environment.GetEnvironmentVariable("DATABASE_URL");
    return "ok" + db;
});

app.MapPost("/widgets", () => "created");

var key = builder.Configuration["API_KEY"];

app.Run();
`,
	})

	spec, err := csharpAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Language != "csharp" {
		t.Errorf("Language = %q, want csharp", spec.Language)
	}
	// The assembly name `dotnet publish` emits is the .csproj filename without extension (no
	// explicit <AssemblyName>). It must reach spec.Name so the Dockerfile ENTRYPOINT is correct.
	if spec.Name != "WidgetApi" {
		t.Errorf("Name = %q, want WidgetApi (.csproj filename)", spec.Name)
	}
	if !hasFramework(spec.ServiceRequirements, "aspnet") {
		t.Errorf("expected an aspnet framework marker; got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServicePostgres) {
		t.Errorf("Npgsql should imply Postgres; got %+v", spec.ServiceRequirements)
	}
	if !hasEnv(spec.EnvVars, "DATABASE_URL") || !hasEnv(spec.EnvVars, "API_KEY") {
		t.Errorf("expected Environment.GetEnvironmentVariable and Configuration env vars; got %+v", spec.EnvVars)
	}
	if !hasRoute(spec.Routes, "GET", "/health") {
		t.Errorf("expected GET /health from app.MapGet; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "POST", "/widgets") {
		t.Errorf("expected POST /widgets from app.MapPost; got %+v", spec.Routes)
	}
	// A web app must not be mislabeled a worker.
	if spec.DetectedShape == "worker" {
		t.Errorf("an ASP.NET Core web app should not be detected as a worker")
	}
	// EF migrations run via a separate SDK step; the chiseled runtime can't, so no MigrationCommand.
	if spec.MigrationCommand != "" {
		t.Errorf("expected empty MigrationCommand (EF runs via a separate SDK step); got %q", spec.MigrationCommand)
	}
	// EF Core on the project should still be reported as a configured tool.
	foundTool := false
	for _, tool := range spec.MigrationContext.ORMTools {
		if tool == "entityframeworkcore" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("expected entityframeworkcore in configured ORM tools; got %+v", spec.MigrationContext.ORMTools)
	}
}

func TestCSharpAnalyzer_AssemblyNameOverride(t *testing.T) {
	// An explicit <AssemblyName> wins over the .csproj filename — this is exactly what
	// `dotnet publish` emits (<AssemblyName>.dll), and the Dockerfile ENTRYPOINT depends on it.
	dir := writeRubyFixture(t, map[string]string{
		"Some.Project.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <AssemblyName>CustomApp</AssemblyName>
  </PropertyGroup>
</Project>
`,
	})
	spec, err := csharpAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Name != "CustomApp" {
		t.Errorf("Name = %q, want CustomApp (<AssemblyName>)", spec.Name)
	}
}

func TestCSharpAnalyzer_AspNetViaPackageReference(t *testing.T) {
	// A non-Web SDK project that references Microsoft.AspNetCore.* is still ASP.NET Core.
	dir := writeRubyFixture(t, map[string]string{
		"Svc.csproj": `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="Microsoft.AspNetCore.App" Version="2.2.8" />
    <PackageReference Include="StackExchange.Redis" Version="2.7.0" />
  </ItemGroup>
</Project>
`,
	})
	spec, err := csharpAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !hasFramework(spec.ServiceRequirements, "aspnet") {
		t.Errorf("Microsoft.AspNetCore.* should imply aspnet; got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServiceRedis) {
		t.Errorf("StackExchange.Redis should imply Redis; got %+v", spec.ServiceRequirements)
	}
}

// The C# analyzer must not claim a Node/Python/Go/Ruby/Rust/Java project.
func TestCSharpAnalyzer_NoFalseMatch(t *testing.T) {
	cases := map[string]map[string]string{
		"node":   {"package.json": `{"name":"x"}`, "index.js": "console.log(1)"},
		"python": {"requirements.txt": "flask\n", "app.py": "print(1)"},
		"go":     {"go.mod": "module x\n", "main.go": "package main\nfunc main(){}"},
		"ruby":   {"Gemfile": "source 'https://rubygems.org'\n", "app.rb": "puts 1"},
		"rust":   {"Cargo.toml": "[package]\nname = \"x\"\n", "src/main.rs": "fn main(){}"},
		"java":   {"pom.xml": "<project></project>", "src/App.java": "class App {}"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeRubyFixture(t, files)
			if ok, _ := csharpAnalyzer(dir).CanHandle(); ok {
				t.Errorf("C# analyzer should not handle a %s project", name)
			}
		})
	}
}
