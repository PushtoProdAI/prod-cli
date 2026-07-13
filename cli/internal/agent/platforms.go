package agent

import (
	"hash/crc32"
	"io"
	"os"
	"strings"

	"github.com/pushtoprodai/prod-cli/internal/auth"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/aca"
	"github.com/pushtoprodai/prod-cli/internal/deployment/aws"
	"github.com/pushtoprodai/prod-cli/internal/deployment/flyio"
	"github.com/pushtoprodai/prod-cli/internal/deployment/gcprun"
	"github.com/pushtoprodai/prod-cli/internal/deployment/heroku"
	"github.com/pushtoprodai/prod-cli/internal/deployment/modal"
	"github.com/pushtoprodai/prod-cli/internal/deployment/netlify"
	"github.com/pushtoprodai/prod-cli/internal/deployment/render"
	"github.com/pushtoprodai/prod-cli/internal/deployment/vercel"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// PlatformSpec is the single registration for a deploy target. The dispatch
// switches (createDeployable / getAuthProvider / getProjectDetector), the
// platform-string matcher, and the interactive menu all derive from the catalog
// of these, so adding a cloud is one registration instead of ~10 hand-edited
// switches. Lives in internal/agent because its factories reference agent-internal
// types (*Activities, ProjectDetector, Platform); the enum stays the serialized
// identity, the catalog is an in-memory lookup keyed by it.
type PlatformSpec struct {
	Platform Platform
	Name     string   // display name, e.g. "Google Cloud Run"
	Aliases  []string // lowercase strings the LLM/menu may produce, e.g. "cloud run", "gcp"

	// NewDeployable builds the platform's Deployable for a spec.
	NewDeployable func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error)
	// NewAuthProvider builds the platform's credential provider (self-contained;
	// reads creds from the environment).
	NewAuthProvider func(out io.Writer) auth.AuthProvider
	// NewDetector builds the existing-project detector. Nil ⇒ noopProjectDetector
	// (for platforms whose deploy is idempotent and needs no pre-detection).
	NewDetector func(a *Activities) ProjectDetector

	// DomainSuffix is the platform's default hostname suffix (e.g. ".run.app").
	// Drives framework host allow-lists (Django ALLOWED_HOSTS / CSRF_TRUSTED_ORIGINS).
	DomainSuffix string
	// SupportsRollback is false for platforms whose Rollback isn't implemented yet;
	// the deploy path then shows a friendly "not supported" message instead of failing
	// mid-workflow.
	SupportsRollback bool
	// ManagedContainer is true for build-image → push-to-registry → create-managed-
	// service clouds (App Runner, Cloud Run, Azure Container Apps). They share one
	// generic deploy workflow instead of a per-platform clone.
	ManagedContainer bool
	// Shapes are the deploy shapes this platform can serve. Empty ⇒ web-only (the
	// back-compatible default — every built-in cloud). A provider plugin populates this
	// from its declared Meta.Shapes so the container workflow knows it may return a
	// URL-less worker/agent without tripping the "returned no URL" assert.
	Shapes []deployment.DeployShape
	// Experimental marks a target that hasn't been validated end-to-end (e.g. Modal).
	// The menu appends "(experimental)" so the caveat is visible at selection time.
	Experimental bool
}

// platformCatalog is the registered set, in menu order. Seeded once by
// registerPlatforms (no scattered init(), so order is deterministic).
var platformCatalog []PlatformSpec

// platformByEnum indexes the catalog by Platform for O(1) lookup.
var platformByEnum = map[Platform]PlatformSpec{}

// RegisterPlatform adds a platform to the catalog. Called once per platform from
// registerPlatforms (and, later, from plugin discovery).
func RegisterPlatform(s PlatformSpec) {
	platformCatalog = append(platformCatalog, s)
	platformByEnum[s.Platform] = s
}

// LookupPlatform returns the spec for a platform.
func LookupPlatform(p Platform) (PlatformSpec, bool) {
	s, ok := platformByEnum[p]
	return s, ok
}

// SupportsShape reports whether a platform can serve the given deploy shape. An empty
// Shapes set means web-only (the default for every built-in cloud), so only ShapeWeb —
// and ShapeMCPServer, which is also HTTP-serving and safe for any web platform — are
// allowed; a worker/cron shape needs an explicit declaration. An unregistered platform
// is web-only. This gates the container workflow's URL-less early-return so a web-only
// cloud that somehow received a worker shape falls through to the existing path rather
// than silently succeeding with no URL.
func (p Platform) SupportsShape(s deployment.DeployShape) bool {
	spec, ok := LookupPlatform(p)
	if !ok || len(spec.Shapes) == 0 {
		return s.HTTPShaped() // web-only default
	}
	for _, sh := range spec.Shapes {
		if sh == s {
			return true
		}
	}
	return false
}

// RegisteredPlatforms returns the catalog in menu order.
func RegisteredPlatforms() []PlatformSpec { return platformCatalog }

// pluginPlatformBase starts the reserved Platform-value range for external provider
// plugins — far above the built-in iota range, so a plugin value never collides with
// a built-in.
const pluginPlatformBase Platform = 1 << 20

// IsPlugin reports whether a Platform value belongs to an external provider plugin.
func IsPlugin(p Platform) bool { return p >= pluginPlatformBase }

// pluginPlatform derives a plugin's Platform value deterministically from its name, so
// a persisted DeployPlan.Platform resumes to the same plugin across restarts.
func pluginPlatform(name string) Platform {
	return pluginPlatformBase + Platform(crc32.ChecksumIEEE([]byte(name)))
}

// DisplayName is the human-readable platform name from the catalog (e.g. "Google
// Cloud Run", or a plugin's name), falling back to the generated enum string. Use it
// wherever a platform is shown to a user or handed to the LLM; a plugin's raw
// String() is only "Platform(1048xxx)".
func (p Platform) DisplayName() string {
	if s, ok := LookupPlatform(p); ok && s.Name != "" {
		return s.Name
	}
	return p.String()
}

// PlatformByString matches a natural-language / menu string (case-insensitive)
// against each spec's Name and Aliases.
func PlatformByString(s string) (Platform, bool) {
	q := strings.ToLower(strings.TrimSpace(s))
	if q == "" {
		return UnknownPlatform, false
	}
	for _, spec := range platformCatalog {
		if strings.ToLower(spec.Name) == q {
			return spec.Platform, true
		}
		for _, a := range spec.Aliases {
			if a == q {
				return spec.Platform, true
			}
		}
	}
	return UnknownPlatform, false
}

// A single init() in this file (not scattered across packages) seeds the catalog
// deterministically — registerPlatforms registers in a fixed order.
func init() { registerPlatforms() }

// registerPlatforms seeds the catalog in a fixed (menu) order.
func registerPlatforms() {
	if len(platformCatalog) > 0 {
		return // idempotent
	}
	RegisterPlatform(PlatformSpec{
		Platform: FlyIO, Name: "Fly.io", Aliases: []string{"fly", "fly.io", "flyio"},
		DomainSuffix: ".fly.dev", SupportsRollback: true,
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
			return flyio.NewFlyioQueuedDeployment(a.flyClient, spec, dockerGen, a.uiWriter), nil
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider { return auth.NewFlyAuth(out) },
		NewDetector:     func(a *Activities) ProjectDetector { return NewFlyIOProjectDetector(a.flyClient, a.uiWriter) },
	})
	RegisterPlatform(PlatformSpec{
		Platform: Render, Name: "Render", Aliases: []string{"render"},
		DomainSuffix: ".onrender.com", SupportsRollback: true,
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
			return render.NewQueuedDeployment(a.renderClient, spec, dockerGen, true, a.uiWriter), nil
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider {
			renderClient := render.NewHTTPRenderClient(os.Getenv("RENDER_API_KEY"), output.NewNoOpWriter())
			return auth.NewRenderAuth(renderClient, out)
		},
		NewDetector: func(a *Activities) ProjectDetector { return NewRenderProjectDetector(a.renderClient, a.uiWriter) },
	})
	RegisterPlatform(PlatformSpec{
		Platform: Vercel, Name: "Vercel", Aliases: []string{"vercel"},
		DomainSuffix: ".vercel.app", SupportsRollback: true,
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			adapter := vercel.NewDefaultVercelDeploymentAdapter(a.uiWriter, a.llmClient)
			return adapter.GenerateArtifacts(spec, deployment.StrategyVercel)
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider { return auth.NewVercelAuth(out) },
		NewDetector:     func(a *Activities) ProjectDetector { return NewVercelProjectDetector(a.uiWriter) },
	})
	RegisterPlatform(PlatformSpec{
		Platform: Netlify, Name: "Netlify", Aliases: []string{"netlify"},
		DomainSuffix: ".netlify.app", SupportsRollback: true,
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			adapter := netlify.NewDefaultNetlifyDeploymentAdapter(a.uiWriter, a.llmClient)
			return adapter.GenerateArtifacts(spec, deployment.StrategyNetlify)
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider {
			return auth.NewNetlifyAuth(netlify.NewCLINetlifyClient(), out)
		},
		NewDetector: func(a *Activities) ProjectDetector { return NewNetlifyProjectDetector(a.uiWriter) },
	})
	RegisterPlatform(PlatformSpec{
		Platform: Heroku, Name: "Heroku", Aliases: []string{"heroku"},
		DomainSuffix: ".herokuapp.com", SupportsRollback: true,
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			adapter := heroku.NewDefaultHerokuDeploymentAdapter(a.uiWriter, a.llmClient)
			return adapter.GenerateArtifacts(spec, deployment.StrategyHeroku)
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider {
			return auth.NewHerokuAuth(heroku.NewHerokuClient("", output.NewNoOpWriter()), out)
		},
		NewDetector: func(a *Activities) ProjectDetector { return NewHerokuProjectDetector(a.uiWriter) },
	})
	RegisterPlatform(PlatformSpec{
		Platform: AWS, Name: "AWS", Aliases: []string{"aws", "amazon"},
		DomainSuffix: ".awsapprunner.com", SupportsRollback: true, ManagedContainer: true,
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
			return aws.NewAppRunnerDeployment(spec, dockerGen, a.history, a.uiWriter), nil
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider { return auth.NewAWSAuth(out) },
		NewDetector:     func(a *Activities) ProjectDetector { return NewAWSProjectDetector(a.uiWriter) },
	})
	RegisterPlatform(PlatformSpec{
		Platform: GoogleCloudRun, Name: "Google Cloud Run", DomainSuffix: ".run.app", SupportsRollback: true, ManagedContainer: true,
		Aliases: []string{"google cloud run", "cloud run", "gcp", "gcp run", "gcprun", "googlecloudrun", "google cloud"},
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
			return gcprun.NewCloudRunDeployment(spec, dockerGen, a.uiWriter), nil
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider { return auth.NewGCPAuth(out) },
		// Cloud Run deploys are idempotent (create-or-update); no pre-detection needed.
		NewDetector: nil,
	})
	RegisterPlatform(PlatformSpec{
		Platform: Azure, Name: "Azure Container Apps",
		Aliases:      []string{"azure container apps", "azure", "container apps", "aca", "azure aca"},
		DomainSuffix: ".azurecontainerapps.io", SupportsRollback: true, ManagedContainer: true,
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
			return aca.NewContainerAppsDeployment(spec, dockerGen, a.uiWriter), nil
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider { return auth.NewAzureAuth(out) },
		// Container Apps deploys are idempotent (create-or-update); no pre-detection.
		NewDetector: nil,
	})
	// Modal — serverless, Python-native, GPU-capable. Deploys via the `modal` CLI
	// (no container registry), so it's not a ManagedContainer. EXPERIMENTAL: not yet
	// validated against a live Modal account.
	RegisterPlatform(PlatformSpec{
		Platform: Modal, Name: "Modal", Aliases: []string{"modal"},
		DomainSuffix: ".modal.run", SupportsRollback: false, Experimental: true,
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			return modal.NewModalDeployment(spec, a.uiWriter), nil
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider { return auth.NewModalAuth(out) },
		// modal deploy is idempotent (create-or-update the app); no pre-detection.
		NewDetector: nil,
	})
}
