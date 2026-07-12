package analyzer

import (
	"encoding/xml"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
)

// Java env access: System.getenv("X") and Spring's @Value("${X}") / @Value("${X:default}")
// placeholder. The var name lands in one of the two capture groups; scanFileForCandidates picks
// the first non-empty one. Spring property names may carry dots/dashes (e.g. server.port), so the
// placeholder class is broader than the getenv one.
const javaEnvVarRegex = `System\.getenv\(\s*"([A-Za-z_][A-Za-z0-9_]*)"|` +
	`@Value\(\s*"\$\{([A-Za-z_][A-Za-z0-9_.\-]*)(?::[^}]*)?\}"`

// HTTP routes across Spring MVC / WebFlux annotations. The annotation name and the path each land
// in a capture group; JavaRouteProcessor maps the annotation to a verb (@RequestMapping defaults
// to GET). Both class-level and method-level mappings match — a class-level @RequestMapping("/api")
// still surfaces a usable route prefix for liveness/health picking.
const javaRouteRegex = `@(GetMapping|PostMapping|PutMapping|DeleteMapping|PatchMapping|RequestMapping)\s*\(\s*(?:value\s*=\s*)?\{?\s*"([^"]*)"`

// javaFrameworkMarkers maps a dependency/plugin substring to its provider label. Order matters:
// Spring Boot is checked first so a Spring app that transitively pulls another marker still wins.
var javaFrameworkMarkers = []struct {
	marker   string
	provider string
}{
	{"spring-boot", "spring-boot"},
	{"org.springframework.boot", "spring-boot"},
	{"quarkus", "quarkus"},
	{"micronaut", "micronaut"},
}

// javaServiceMarkers maps a dependency substring to the backing service it implies. A JPA starter
// is treated as Postgres (the common default); an explicit driver still wins if both appear.
var javaServiceMarkers = []struct {
	marker  string
	service ServiceRequirement
}{
	{"postgresql", ServicePostgres},
	{"spring-boot-starter-data-jpa", ServicePostgres},
	{"mysql", ServiceMySQL},
	{"lettuce", ServiceRedis},
	{"spring-data-redis", ServiceRedis},
	{"spring-boot-starter-data-redis", ServiceRedis},
}

// JavaAnalyzer implements Analyzer for JVM projects, Spring Boot first. It detects the build tool
// (Maven vs Gradle) to pick the build command; the java.dockerfile template runs that command in a
// JDK stage, locates the fat jar, and runs it on a JRE, so BuildCommand is both advisory (plan
// display) and the actual builder step.
type JavaAnalyzer struct {
	ProjectFS projectFS
}

// NewJavaAnalyzer creates a Java analyzer instance.
func NewJavaAnalyzer(projectFS projectFS) Analyzer {
	return &JavaAnalyzer{ProjectFS: projectFS}
}

// CanHandle reports whether this looks like a JVM project: a Maven pom.xml or a Gradle
// build.gradle / build.gradle.kts at the root. Manifest-only by design — a bare .java file is not
// enough (plenty of non-Java repos carry an incidental source file), mirroring Ruby/Rust precision.
func (j *JavaAnalyzer) CanHandle() (bool, error) {
	for _, f := range []string{"pom.xml", "build.gradle", "build.gradle.kts"} {
		if _, err := fs.Stat(j.ProjectFS, f); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// Analyze produces the project spec: build tool → build command, framework/service markers from
// declared dependencies, and env/route candidates scanned from source.
func (j *JavaAnalyzer) Analyze() (*ProjectSpec, error) {
	deps := j.dependencies()

	ignoreDirs := []string{"target", "build", ".gradle", ".git", ".idea", "node_modules"}
	exts := []string{".java", ".kt"}

	envVars, err := walkProjectForCandidates(j.ProjectFS, exts, ignoreDirs, regexp.MustCompile(javaEnvVarRegex), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Java env vars: %w", err)
	}

	routes, err := walkProjectForRoutes(j.ProjectFS, exts, ignoreDirs, regexp.MustCompile(javaRouteRegex), NewJavaRouteProcessor(), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Java routes: %w", err)
	}

	services := j.detectServices(deps)

	// Framework marker mirrors the "framework" convention used elsewhere and tells
	// DetectAgentShape this app serves HTTP so it isn't mislabeled a worker.
	framework := j.detectFramework(deps)
	if framework != "" {
		services = append(services, ServiceRequirement{Type: "framework", Provider: framework})
	}

	buildCommand := j.buildCommand()

	migrationContext := j.collectMigrationContext(deps)

	detectedShape := DetectAgentShape(deps, framework != "")

	return &ProjectSpec{
		Name:                j.projectName(),
		Language:            "java",
		ServiceRequirements: services,
		BuildCommand:        buildCommand,
		// The fat jar runs via `java -jar` in the runtime stage; the template hard-codes that, so
		// StartCommand is advisory (plan display).
		StartCommand: "java -jar app.jar",
		// No MigrationCommand: Spring Boot auto-runs Flyway/Liquibase from the classpath on startup,
		// so a separate migrate step would double-run. See collectMigrationContext.
		EnvVars:          envVars,
		Routes:           routes,
		MigrationContext: migrationContext,
		DetectedShape:    detectedShape,
	}, nil
}

// isMaven reports whether the project is Maven-built (pom.xml present). Absent that, CanHandle has
// already guaranteed a Gradle build file, so callers treat "not Maven" as Gradle.
func (j *JavaAnalyzer) isMaven() bool {
	_, err := fs.Stat(j.ProjectFS, "pom.xml")
	return err == nil
}

// buildCommand returns the command that produces the runnable artifact. Maven builds a package
// jar; Gradle's Spring Boot plugin builds a fat jar via `bootJar`. The wrapper (mvnw/gradlew) is
// preferred when present — it pins the build-tool version — falling back to a system `mvn`/`gradle`.
func (j *JavaAnalyzer) buildCommand() string {
	if j.isMaven() {
		if _, err := fs.Stat(j.ProjectFS, "mvnw"); err == nil {
			return "./mvnw -q -DskipTests package"
		}
		return "mvn -q -DskipTests package"
	}
	if _, err := fs.Stat(j.ProjectFS, "gradlew"); err == nil {
		return "./gradlew bootJar"
	}
	return "gradle bootJar"
}

// mavenPOM is the minimal pom.xml shape we parse: the project's own artifactId plus the group/
// artifact of the parent, dependencies, and build plugins (used for framework/service markers).
// encoding/xml binds `xml:"artifactId"` to the DIRECT child of <project>, so the parent's and
// dependencies' nested artifactIds don't clobber the project name.
type mavenPOM struct {
	ArtifactID string `xml:"artifactId"`
	Parent     struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
	} `xml:"parent"`
	Dependencies struct {
		Dependency []struct {
			GroupID    string `xml:"groupId"`
			ArtifactID string `xml:"artifactId"`
		} `xml:"dependency"`
	} `xml:"dependencies"`
	Build struct {
		Plugins struct {
			Plugin []struct {
				GroupID    string `xml:"groupId"`
				ArtifactID string `xml:"artifactId"`
			} `xml:"plugin"`
		} `xml:"plugins"`
	} `xml:"build"`
}

// parsePOM reads and unmarshals pom.xml; returns nil on any error (missing/malformed).
func (j *JavaAnalyzer) parsePOM() *mavenPOM {
	data, err := fs.ReadFile(j.ProjectFS, "pom.xml")
	if err != nil {
		return nil
	}
	var pom mavenPOM
	if err := xml.Unmarshal(data, &pom); err != nil {
		return nil
	}
	return &pom
}

// dependencies returns lowercased coordinate tokens (group ids and artifact ids) declared by the
// build. Framework/service/migration detection matches substrings against these — robust across
// Maven's XML and Gradle's DSL without a full model of either.
func (j *JavaAnalyzer) dependencies() []string {
	if j.isMaven() {
		return j.mavenDependencies()
	}
	return j.gradleDependencies()
}

func (j *JavaAnalyzer) mavenDependencies() []string {
	pom := j.parsePOM()
	if pom == nil {
		return nil
	}
	var tokens []string
	add := func(s string) {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			tokens = append(tokens, s)
		}
	}
	add(pom.Parent.GroupID)
	add(pom.Parent.ArtifactID)
	for _, d := range pom.Dependencies.Dependency {
		add(d.GroupID)
		add(d.ArtifactID)
	}
	for _, p := range pom.Build.Plugins.Plugin {
		add(p.GroupID)
		add(p.ArtifactID)
	}
	return tokens
}

// gradleQuotedString matches any single- or double-quoted string in a Gradle build file — enough
// to catch `implementation 'group:name:ver'`, the Kotlin DSL `implementation("group:name")`, and
// plugin ids `id 'org.springframework.boot'`.
var gradleQuotedString = regexp.MustCompile(`["']([^"']+)["']`)

func (j *JavaAnalyzer) gradleDependencies() []string {
	var tokens []string
	for _, name := range []string{"build.gradle", "build.gradle.kts"} {
		data, err := fs.ReadFile(j.ProjectFS, name)
		if err != nil {
			continue
		}
		for _, m := range gradleQuotedString.FindAllStringSubmatch(string(data), -1) {
			quoted := strings.ToLower(m[1])
			// A dependency coordinate is `group:name[:version]`; split so each part is a token.
			// A plugin id like "org.springframework.boot" has no colon and is kept whole.
			for _, part := range strings.Split(quoted, ":") {
				if part = strings.TrimSpace(part); part != "" {
					tokens = append(tokens, part)
				}
			}
		}
	}
	return tokens
}

// projectName is the Maven <artifactId> or the Gradle rootProject.name, falling back to the
// project directory basename.
func (j *JavaAnalyzer) projectName() string {
	if j.isMaven() {
		if pom := j.parsePOM(); pom != nil && strings.TrimSpace(pom.ArtifactID) != "" {
			return strings.TrimSpace(pom.ArtifactID)
		}
		return filepath.Base(j.ProjectFS.rootPath)
	}
	// Gradle: rootProject.name usually lives in settings.gradle[.kts], sometimes build.gradle.
	re := regexp.MustCompile(`rootProject\.name\s*=\s*["']([^"']+)["']`)
	for _, name := range []string{"settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts"} {
		if data, err := fs.ReadFile(j.ProjectFS, name); err == nil {
			if m := re.FindStringSubmatch(string(data)); m != nil {
				return m[1]
			}
		}
	}
	return filepath.Base(j.ProjectFS.rootPath)
}

// detectServices maps known DB/cache dependency substrings to backing-service requirements.
func (j *JavaAnalyzer) detectServices(deps []string) []ServiceRequirement {
	var services []ServiceRequirement
	seen := map[ServiceRequirement]bool{}
	for _, m := range javaServiceMarkers {
		if depContains(deps, m.marker) && !seen[m.service] {
			seen[m.service] = true
			services = append(services, m.service)
		}
	}
	return services
}

// detectFramework returns the provider label of the first recognized web-framework marker.
func (j *JavaAnalyzer) detectFramework(deps []string) string {
	for _, f := range javaFrameworkMarkers {
		if depContains(deps, f.marker) {
			return f.provider
		}
	}
	return ""
}

// collectMigrationContext gathers Flyway/Liquibase signals. Spring Boot auto-runs them on startup,
// so the analyzer sets no MigrationCommand; the detected tools are still reported for the planner.
// Their scripts live under src/main/resources (db/migration, db/changelog) rather than a top-level
// migrations/ dir, so FilterConfiguredMigrationTools has a java-specific case (see migration.go).
func (j *JavaAnalyzer) collectMigrationContext(deps []string) MigrationContext {
	migrationContext := MigrationContext{
		MigrationFiles: []string{},
		ORMTools:       []string{},
		ConfigFiles:    make(map[string]string),
		PackageScripts: make(map[string]string),
	}

	detectedTools := DetectORMTools(deps, "java")
	migrationFiles, _ := FindMigrationFiles(j.ProjectFS.rootPath)
	migrationContext.MigrationFiles = migrationFiles
	migrationContext.ORMTools = FilterConfiguredMigrationTools(detectedTools, migrationFiles, j.ProjectFS.rootPath)

	return migrationContext
}

// depContains reports whether any dependency token contains the given marker substring.
func depContains(deps []string, marker string) bool {
	for _, d := range deps {
		if strings.Contains(d, marker) {
			return true
		}
	}
	return false
}

// JavaRouteProcessor turns Spring mapping-annotation matches into RouteCandidates, mapping the
// annotation to an HTTP verb (@RequestMapping → GET by default) and normalizing the path.
type JavaRouteProcessor struct{}

// NewJavaRouteProcessor creates a Java route processor.
func NewJavaRouteProcessor() *JavaRouteProcessor { return &JavaRouteProcessor{} }

// javaAnnotationMethod maps a Spring mapping annotation to its HTTP verb.
var javaAnnotationMethod = map[string]string{
	"GetMapping":    "GET",
	"PostMapping":   "POST",
	"PutMapping":    "PUT",
	"DeleteMapping": "DELETE",
	"PatchMapping":  "PATCH",
	// @RequestMapping without an explicit method defaults to GET for liveness purposes.
	"RequestMapping": "GET",
}

func (p *JavaRouteProcessor) ProcessMatch(match RouteMatch, filePath string) []RouteCandidate {
	if len(match.CaptureGroups) < 2 {
		return nil
	}
	annotation := match.CaptureGroups[0]
	routePath := match.CaptureGroups[1]

	method, ok := javaAnnotationMethod[annotation]
	if !ok {
		return nil
	}

	// Spring paths are usually absolute; normalize a relative one and drop implausible values.
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
