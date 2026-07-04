// Package registry is prod's pluggable container-registry adapter. prod builds a
// container image locally and pushes it to a registry the USER owns (Docker Hub,
// GHCR, or any generic registry), then hands the resulting image reference to the
// deploy target (e.g. Render). There is no hosted middleman and no ECR — you
// choose whatever registry you already use, via configuration.
package registry

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
)

// projectNameRe matches a single, lowercase container-repository path component:
// letters/digits separated by '.', '_' or '-'. This rejects slashes, colons,
// '@', spaces, and empty names, so a project name can't produce a malformed or
// unexpected image reference.
var projectNameRe = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// tagRe matches a valid container image tag.
var tagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// nonComponentRe matches runs of characters not allowed in a repository component.
var nonComponentRe = regexp.MustCompile(`[^a-z0-9]+`)

// Sanitize turns an arbitrary project name (from package.json, a directory name,
// an npm scope like "@org/pkg", etc.) into a valid container-repository
// component: lowercased, scope/path stripped, runs of disallowed characters
// collapsed to a single '-', and trimmed. Returns "app" if nothing valid remains.
// The result always satisfies the adapter's project-name rules, so callers
// should Sanitize a raw project name before passing it to Credentials/Ref to
// avoid failing validation only after a build.
func Sanitize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:] // strip npm scope / path segments
	}
	name = strings.Trim(nonComponentRe.ReplaceAllString(name, "-"), "-")
	if name == "" {
		return "app"
	}
	return name
}

// Credentials authenticate a push to a container registry.
type Credentials struct {
	URL        string // registry host used for tagging, e.g. "docker.io", "ghcr.io"
	AuthServer string // Docker auth ServerAddress (may differ from URL — Docker Hub uses an index URL)
	Repository string // namespaced repository, e.g. "alice/my-app"
	Username   string
	Token      string
}

// String redacts the token so credentials never leak via %v/%+v logging.
func (c Credentials) String() string {
	return fmt.Sprintf("Credentials{URL:%s AuthServer:%s Repository:%s Username:%s Token:<redacted>}",
		c.URL, c.AuthServer, c.Repository, c.Username)
}

// GoString redacts the token for %#v as well.
func (c Credentials) GoString() string { return c.String() }

// Registry is a container registry the user owns. prod pushes locally-built
// images here and deploys from the resulting reference.
type Registry interface {
	// Name is the short identifier used in config/output (e.g. "dockerhub").
	Name() string
	// Credentials returns push credentials for the given project's repository,
	// or an error if the project name isn't a valid repository component.
	Credentials(project string) (Credentials, error)
	// Ref returns the fully-qualified, pullable image reference for project:tag
	// (e.g. "docker.io/alice/my-app:1720000000"), or an error if project or tag
	// is invalid.
	Ref(project, tag string) (string, error)
}

// Config selects and configures a registry. Zero value is invalid; use FromEnv.
type Config struct {
	Kind      string // "dockerhub" | "ghcr" | "generic"
	Host      string // required for "generic"
	Namespace string // user/org; defaults to Username for dockerhub
	Username  string
	Token     string
}

// FromEnv builds a Registry from configuration. getenv is injected for testing.
//
//	PROD_REGISTRY           dockerhub | ghcr | generic   (default: dockerhub)
//	PROD_REGISTRY_HOST      registry host (generic only, e.g. registry.gitlab.com)
//	PROD_REGISTRY_NAMESPACE user/org namespace (defaults to the username on Docker Hub)
//	PROD_REGISTRY_USERNAME  registry username
//	PROD_REGISTRY_TOKEN     registry password / access token
func FromEnv(getenv func(string) string) (Registry, error) {
	cfg := Config{
		Kind:      strings.ToLower(strings.TrimSpace(getenv("PROD_REGISTRY"))),
		Host:      strings.TrimSpace(getenv("PROD_REGISTRY_HOST")),
		Namespace: strings.TrimSpace(getenv("PROD_REGISTRY_NAMESPACE")),
		Username:  strings.TrimSpace(getenv("PROD_REGISTRY_USERNAME")),
		// Token is intentionally not trimmed: trimming a secret could silently
		// corrupt a valid token, and usernames never carry significant whitespace.
		Token: getenv("PROD_REGISTRY_TOKEN"),
	}
	if cfg.Kind == "" {
		cfg.Kind = "dockerhub"
	}
	return New(cfg)
}

// New builds a Registry from an explicit Config.
func New(cfg Config) (Registry, error) {
	if cfg.Username == "" || cfg.Token == "" {
		return nil, errors.Errorf("registry requires PROD_REGISTRY_USERNAME and PROD_REGISTRY_TOKEN")
	}
	// Registries require lowercase namespaces (Docker Hub, GHCR, and most others).
	ns := strings.ToLower(cfg.Namespace)

	switch cfg.Kind {
	case "dockerhub", "docker", "":
		if ns == "" {
			ns = strings.ToLower(cfg.Username)
		}
		return &hostRegistry{
			name: "dockerhub", host: "docker.io",
			// Docker Hub's canonical auth ServerAddress is the v1 index URL.
			authServer: "https://index.docker.io/v1/",
			namespace:  ns, username: cfg.Username, token: cfg.Token,
		}, nil
	case "ghcr":
		if ns == "" {
			return nil, errors.Errorf("ghcr requires PROD_REGISTRY_NAMESPACE (your GitHub user or org)")
		}
		return &hostRegistry{name: "ghcr", host: "ghcr.io", authServer: "ghcr.io", namespace: ns, username: cfg.Username, token: cfg.Token}, nil
	case "generic":
		if cfg.Host == "" {
			return nil, errors.Errorf("generic registry requires PROD_REGISTRY_HOST")
		}
		if ns == "" {
			return nil, errors.Errorf("generic registry requires PROD_REGISTRY_NAMESPACE")
		}
		host := normalizeHost(cfg.Host)
		return &hostRegistry{name: "generic", host: host, authServer: host, namespace: ns, username: cfg.Username, token: cfg.Token}, nil
	default:
		return nil, errors.Errorf("unknown registry %q (want: dockerhub, ghcr, generic)", cfg.Kind)
	}
}

// hostRegistry implements Registry for any host/namespace-based registry.
type hostRegistry struct {
	name       string
	host       string
	authServer string
	namespace  string
	username   string
	token      string
}

func (r *hostRegistry) Name() string { return r.name }

// repository validates and normalizes project into "<namespace>/<project>".
func (r *hostRegistry) repository(project string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(project))
	if !projectNameRe.MatchString(p) {
		return "", errors.Errorf("invalid project name %q: use lowercase letters, digits, and . _ - (no '/', ':', '@', or spaces)", project)
	}
	return r.namespace + "/" + p, nil
}

func (r *hostRegistry) Credentials(project string) (Credentials, error) {
	repo, err := r.repository(project)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{
		URL:        r.host,
		AuthServer: r.authServer,
		Repository: repo,
		Username:   r.username,
		Token:      r.token,
	}, nil
}

func (r *hostRegistry) Ref(project, tag string) (string, error) {
	repo, err := r.repository(project)
	if err != nil {
		return "", err
	}
	if !tagRe.MatchString(tag) {
		return "", errors.Errorf("invalid image tag %q", tag)
	}
	return fmt.Sprintf("%s/%s:%s", r.host, repo, tag), nil
}

// normalizeHost strips any scheme, path, and trailing slash from a registry host,
// preserving an optional port.
func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i] // drop any path segment
	}
	return strings.ToLower(h)
}
