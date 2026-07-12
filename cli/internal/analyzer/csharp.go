package analyzer

import (
	"encoding/xml"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-errors/errors"
)

// C# env access: Environment.GetEnvironmentVariable("X") plus the ASP.NET configuration indexers
// builder.Configuration["X"] / Configuration["X"]. The var name lands in one of the capture groups;
// scanFileForCandidates picks the first non-empty one. Configuration keys may carry ':' (the
// hierarchical separator, e.g. "ConnectionStrings:Default") and '.', so that class is broader.
const csharpEnvVarRegex = `Environment\.GetEnvironmentVariable\(\s*"([A-Za-z_][A-Za-z0-9_]*)"|` +
	`(?:builder\.)?Configuration\[\s*"([A-Za-z_][A-Za-z0-9_.:]*)"`

// HTTP routes across ASP.NET Core minimal APIs (`app.MapGet("/…")` and friends) and MVC/attribute
// routing (`[HttpGet("/…")]`, bare `[HttpPost]`, and `[Route("…")]`). CSharpRouteProcessor maps each
// alternative to a verb. Capture groups, left to right:
//
//	1,2 minimal API: verb, path
//	3,4 attribute verb: verb, optional path
//	5   [Route("…")]: path (verb defaults to GET)
const csharpRouteRegex = `\.Map(Get|Post|Put|Delete)\(\s*"([^"]*)"|` +
	`\[Http(Get|Post|Put|Delete)(?:\(\s*"([^"]*)")?|` +
	`\[Route\(\s*"([^"]*)"`

// csharpServiceMarkers maps a NuGet package (matched as a lowercased substring of a PackageReference
// Include) to the backing service it implies. The EF provider packages carry the driver name, so a
// single substring covers both the raw driver and the EF-provider form.
var csharpServiceMarkers = []struct {
	marker  string
	service ServiceRequirement
}{
	{"npgsql", ServicePostgres},                         // Npgsql, Npgsql.EntityFrameworkCore.PostgreSQL
	{"entityframeworkcore.postgresql", ServicePostgres}, // Microsoft.EntityFrameworkCore.PostgreSQL
	{"mysql", ServiceMySQL},                             // MySql.Data, Pomelo.EntityFrameworkCore.MySql
	{"stackexchange.redis", ServiceRedis},               // StackExchange.Redis
	{"caching.stackexchangeredis", ServiceRedis},        // Microsoft.Extensions.Caching.StackExchangeRedis
}

// CSharpAnalyzer implements Analyzer for .NET projects, ASP.NET Core first. The csharp.dockerfile
// template does the build (`dotnet publish`) and runs the published assembly, so BuildCommand and
// StartCommand are left empty — the Dockerfile drives everything.
type CSharpAnalyzer struct {
	ProjectFS projectFS
}

// NewCSharpAnalyzer creates a C#/.NET analyzer instance.
func NewCSharpAnalyzer(projectFS projectFS) Analyzer {
	return &CSharpAnalyzer{ProjectFS: projectFS}
}

// CanHandle reports whether this looks like a .NET project: a *.csproj or a *.sln at the project
// root. Manifest-only by design — a bare .cs file is not enough (plenty of non-.NET repos carry an
// incidental C# source file), mirroring Ruby/Rust/Java precision.
func (c *CSharpAnalyzer) CanHandle() (bool, error) {
	entries, err := fs.ReadDir(c.ProjectFS, ".")
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".csproj") || strings.HasSuffix(name, ".sln") {
			return true, nil
		}
	}
	return false, nil
}

// Analyze produces the project spec: ASP.NET Core framework detection + backing services from the
// .csproj PackageReferences, env/route candidates scanned from .cs source, and the deterministic
// assembly name (which the Dockerfile ENTRYPOINT depends on).
func (c *CSharpAnalyzer) Analyze() (*ProjectSpec, error) {
	proj := c.primaryProject()
	packages := c.packages(proj)

	// bin/ and obj/ are the .NET build outputs; .vs is Visual Studio's cache.
	ignoreDirs := []string{"bin", "obj", ".vs", ".git", "node_modules"}
	exts := []string{".cs"}

	envVars, err := walkProjectForCandidates(c.ProjectFS, exts, ignoreDirs, regexp.MustCompile(csharpEnvVarRegex), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan C# env vars: %w", err)
	}

	routes, err := walkProjectForRoutes(c.ProjectFS, exts, ignoreDirs, regexp.MustCompile(csharpRouteRegex), NewCSharpRouteProcessor(), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan C# routes: %w", err)
	}

	services := c.detectServices(packages)

	// Framework marker tells DetectAgentShape this app serves HTTP so it isn't mislabeled a
	// worker, and mirrors the "framework" convention used by the other analyzers.
	isASPNET := c.isASPNET(proj, packages)
	if isASPNET {
		services = append(services, ServiceRequirement{Type: "framework", Provider: "aspnet"})
	}

	migrationContext := c.collectMigrationContext(packages)

	detectedShape := DetectAgentShape(packages, isASPNET)

	return &ProjectSpec{
		Name:                c.projectName(proj),
		Language:            "csharp",
		ServiceRequirements: services,
		// The csharp.dockerfile hard-codes `dotnet publish` and an ENTRYPOINT on the published
		// assembly, so there's no advisory build/start command to surface here.
		BuildCommand: "",
		StartCommand: "",
		// No MigrationCommand: EF Core migrations (`dotnet ef database update`) need the SDK + EF
		// tools, which the chiseled runtime image lacks. Migrations run via a separate
		// `dotnet ef migrations bundle`/SDK step; setting a command that can't execute in the
		// runtime image would only mislead. See collectMigrationContext.
		EnvVars:          envVars,
		Routes:           routes,
		MigrationContext: migrationContext,
		DetectedShape:    detectedShape,
	}, nil
}

// csprojXML is the minimal .csproj shape we parse. The SDK can be declared either as an attribute on
// <Project Sdk="…"> or as a child <Sdk Name="…"/>; both forms are captured. PropertyGroup and
// ItemGroup repeat, so they're slices.
type csprojXML struct {
	XMLName xml.Name `xml:"Project"`
	SDKAttr string   `xml:"Sdk,attr"`
	SDKElem []struct {
		Name string `xml:"Name,attr"`
	} `xml:"Sdk"`
	PropertyGroups []struct {
		AssemblyName string `xml:"AssemblyName"`
	} `xml:"PropertyGroup"`
	ItemGroups []struct {
		PackageReferences []struct {
			Include string `xml:"Include,attr"`
		} `xml:"PackageReference"`
	} `xml:"ItemGroup"`
}

// sdks returns the declared SDKs, lowercased (attribute form + element form).
func (p *csprojXML) sdks() []string {
	var out []string
	if s := strings.ToLower(strings.TrimSpace(p.SDKAttr)); s != "" {
		out = append(out, s)
	}
	for _, e := range p.SDKElem {
		if s := strings.ToLower(strings.TrimSpace(e.Name)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// assemblyName returns the explicit <AssemblyName>, if any (first non-empty across PropertyGroups).
func (p *csprojXML) assemblyName() string {
	for _, pg := range p.PropertyGroups {
		if an := strings.TrimSpace(pg.AssemblyName); an != "" {
			return an
		}
	}
	return ""
}

// includes returns every PackageReference Include, lowercased.
func (p *csprojXML) includes() []string {
	var out []string
	for _, ig := range p.ItemGroups {
		for _, pr := range ig.PackageReferences {
			if inc := strings.ToLower(strings.TrimSpace(pr.Include)); inc != "" {
				out = append(out, inc)
			}
		}
	}
	return out
}

// csprojEntry pairs a parsed .csproj with its path (relative to the project FS root).
type csprojEntry struct {
	path string
	proj *csprojXML
}

// primaryProject locates and parses the project's primary .csproj. A root-level .csproj wins (the
// common single-project layout); otherwise the first .csproj found walking the tree (sorted for
// determinism) is used — covering a repo whose root carries only a .sln with the project in a
// subdirectory. Returns nil when nothing parses.
func (c *CSharpAnalyzer) primaryProject() *csprojEntry {
	// Prefer a root-level .csproj: deterministic and the overwhelmingly common case.
	if entries, err := fs.ReadDir(c.ProjectFS, "."); err == nil {
		var rootProjects []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".csproj") {
				rootProjects = append(rootProjects, e.Name())
			}
		}
		if len(rootProjects) > 0 {
			sort.Strings(rootProjects)
			if proj := c.parseCsproj(rootProjects[0]); proj != nil {
				return &csprojEntry{path: rootProjects[0], proj: proj}
			}
		}
	}

	// Fall back to the first .csproj anywhere in the tree (sorted). This handles a .sln-only root.
	var found []string
	_ = fs.WalkDir(c.ProjectFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "bin", "obj", ".vs", ".git", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".csproj") {
			found = append(found, path)
		}
		return nil
	})
	if len(found) == 0 {
		return nil
	}
	sort.Strings(found)
	if proj := c.parseCsproj(found[0]); proj != nil {
		return &csprojEntry{path: found[0], proj: proj}
	}
	return nil
}

// parseCsproj reads and unmarshals a .csproj; returns nil on any error (missing/malformed).
func (c *CSharpAnalyzer) parseCsproj(path string) *csprojXML {
	data, err := fs.ReadFile(c.ProjectFS, path)
	if err != nil {
		return nil
	}
	var proj csprojXML
	if err := xml.Unmarshal(data, &proj); err != nil {
		return nil
	}
	return &proj
}

// packages returns the lowercased PackageReference Include names declared by the primary project.
func (c *CSharpAnalyzer) packages(entry *csprojEntry) []string {
	if entry == nil || entry.proj == nil {
		return nil
	}
	return entry.proj.includes()
}

// projectName derives the name of the assembly `dotnet publish` emits, which the Dockerfile
// ENTRYPOINT (`dotnet <Name>.dll`) depends on. MSBuild names the output assembly after
// <AssemblyName> when set, otherwise after the .csproj filename without its extension
// ($(MSBuildProjectName)). Both are deterministic — critical, because the chiseled runtime has no
// shell to fall back on a find. Falls back to the project directory basename only when no .csproj
// parses (e.g. a .sln-only root we couldn't resolve).
func (c *CSharpAnalyzer) projectName(entry *csprojEntry) string {
	if entry != nil && entry.proj != nil {
		if an := entry.proj.assemblyName(); an != "" {
			return an
		}
		base := filepath.Base(entry.path)
		return strings.TrimSuffix(base, ".csproj")
	}
	return filepath.Base(c.ProjectFS.rootPath)
}

// isASPNET reports whether this is an ASP.NET Core web app: the Web SDK (Microsoft.NET.Sdk.Web) or a
// Microsoft.AspNetCore.* package reference.
func (c *CSharpAnalyzer) isASPNET(entry *csprojEntry, packages []string) bool {
	if entry != nil && entry.proj != nil {
		for _, sdk := range entry.proj.sdks() {
			if strings.Contains(sdk, "microsoft.net.sdk.web") {
				return true
			}
		}
	}
	for _, p := range packages {
		if strings.HasPrefix(p, "microsoft.aspnetcore") {
			return true
		}
	}
	return false
}

// detectServices maps known DB/cache package substrings to backing-service requirements.
func (c *CSharpAnalyzer) detectServices(packages []string) []ServiceRequirement {
	var services []ServiceRequirement
	seen := map[ServiceRequirement]bool{}
	for _, m := range csharpServiceMarkers {
		if pkgContains(packages, m.marker) && !seen[m.service] {
			seen[m.service] = true
			services = append(services, m.service)
		}
	}
	return services
}

// collectMigrationContext gathers EF Core / FluentMigrator signals so the planner can decide whether
// migrations will run. No MigrationCommand is set (the chiseled runtime lacks the SDK + EF tools);
// EF Core keeps its migrations under a project-level `Migrations/` folder — capitalized, often in a
// subproject — not a top-level `migrations/` dir, so FilterConfiguredMigrationTools has a csharp
// case that includes the tool on dependency presence alone (see migration.go).
func (c *CSharpAnalyzer) collectMigrationContext(packages []string) MigrationContext {
	migrationContext := MigrationContext{
		MigrationFiles: []string{},
		ORMTools:       []string{},
		ConfigFiles:    make(map[string]string),
		PackageScripts: make(map[string]string),
	}

	detectedTools := DetectORMTools(packages, "csharp")
	migrationFiles, _ := FindMigrationFiles(c.ProjectFS.rootPath)
	migrationContext.MigrationFiles = migrationFiles
	migrationContext.ORMTools = FilterConfiguredMigrationTools(detectedTools, migrationFiles, c.ProjectFS.rootPath)

	return migrationContext
}

// pkgContains reports whether any package name contains the given marker substring.
func pkgContains(packages []string, marker string) bool {
	for _, p := range packages {
		if strings.Contains(p, marker) {
			return true
		}
	}
	return false
}

// CSharpRouteProcessor turns ASP.NET Core minimal-API and attribute-routing matches into
// RouteCandidates, mapping each to an HTTP verb and normalizing the path.
type CSharpRouteProcessor struct{}

// NewCSharpRouteProcessor creates a C# route processor.
func NewCSharpRouteProcessor() *CSharpRouteProcessor { return &CSharpRouteProcessor{} }

func (p *CSharpRouteProcessor) ProcessMatch(match RouteMatch, filePath string) []RouteCandidate {
	// Groups (see csharpRouteRegex): [0]=map verb, [1]=map path, [2]=attr verb, [3]=attr path,
	// [4]=[Route] path.
	g := match.CaptureGroups
	if len(g) < 5 {
		return nil
	}

	var method, routePath string
	switch {
	case g[0] != "": // app.MapGet("/…")
		method = strings.ToUpper(g[0])
		routePath = g[1]
	case g[2] != "": // [HttpGet("/…")] or bare [HttpGet]
		method = strings.ToUpper(g[2])
		routePath = g[3] // may be empty for a bare attribute
	case g[4] != "": // [Route("…")]
		method = "GET"
		routePath = g[4]
	default:
		return nil
	}

	// A bare [HttpGet]/[Route] with no explicit path maps to the controller root; default it.
	if routePath == "" {
		routePath = "/"
	}
	if !strings.HasPrefix(routePath, "/") {
		routePath = "/" + routePath
	}
	if strings.Contains(routePath, " ") || len(routePath) > 100 {
		return nil
	}

	return []RouteCandidate{{
		Method:  method,
		Path:    routePath,
		File:    filePath,
		Line:    match.Line,
		Context: match.Context,
	}}
}
