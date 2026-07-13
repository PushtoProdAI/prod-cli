package cloudflare

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

const buildTimeout = 10 * time.Minute

// CloudflareQueuedDeployment deploys a built static site to Cloudflare Pages via direct upload.
type CloudflareQueuedDeployment struct {
	client CloudflareClient
	spec   *deployment.DeploymentSpec
	writer io.Writer
}

var (
	_ deployment.Deployable = (*CloudflareQueuedDeployment)(nil)
	_ deployment.Destroyer  = (*CloudflareQueuedDeployment)(nil)
)

// NewCloudflareQueuedDeployment builds a Cloudflare Pages deployable for a project spec.
func NewCloudflareQueuedDeployment(client CloudflareClient, spec *deployment.DeploymentSpec, writer io.Writer) *CloudflareQueuedDeployment {
	if writer == nil {
		writer = io.Discard
	}
	return &CloudflareQueuedDeployment{client: client, spec: spec, writer: writer}
}

// Deploy builds the static site (if needed), ensures the Pages project exists, and direct-uploads
// the output directory.
func (d *CloudflareQueuedDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	if !d.spec.IsStatic {
		return nil, errors.Errorf("Cloudflare Pages supports static sites only — this project builds a server, not a static output directory")
	}
	project := SanitizeProjectName(d.spec.Name)

	if err := d.ensureProject(ctx, project); err != nil {
		return nil, err
	}

	outputDir := d.outputDir()
	if !dirHasFiles(outputDir) {
		_, _ = fmt.Fprintf(d.writer, "Building static site...\n")
		if err := d.runBuild(ctx); err != nil {
			return nil, err
		}
	}
	if !dirHasFiles(outputDir) {
		return nil, errors.Errorf("build produced no files in %q — check the build command and output directory", outputDir)
	}

	_, _ = fmt.Fprintf(d.writer, "Uploading %s to Cloudflare Pages...\n", d.spec.Name)
	dep, err := UploadDir(ctx, d.client, outputDir, project, defaultBranch)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(d.writer, "✓ Deployed — %s\n", dep.URL)

	return []deployment.CreatedResource{{
		ID:      dep.ID,
		Type:    "cloudflare_pages",
		Name:    project,
		Primary: true,
		Metadata: map[string]any{
			"url":         dep.URL,
			"projectName": project,
		},
	}}, nil
}

// ensureProject creates the Pages project if it doesn't already exist (idempotent re-deploy).
func (d *CloudflareQueuedDeployment) ensureProject(ctx context.Context, project string) error {
	projects, err := d.client.ListProjects(ctx)
	if err != nil {
		return errors.Errorf("failed to list Cloudflare Pages projects: %w", err)
	}
	for _, p := range projects {
		if p.Name == project {
			return nil
		}
	}
	if _, err := d.client.CreateProject(ctx, project, defaultBranch); err != nil {
		return errors.Errorf("failed to create Cloudflare Pages project %q: %w", project, err)
	}
	return nil
}

// Destroy deletes the Pages project (and all its deployments). Cloudflare Pages provisions no
// backing databases, so nothing else is orphaned.
func (d *CloudflareQueuedDeployment) Destroy(ctx context.Context) error {
	project := SanitizeProjectName(d.spec.Name)
	if err := d.client.DeleteProject(ctx, project); err != nil {
		return errors.Errorf("failed to destroy Cloudflare Pages project %q: %w", project, err)
	}
	return nil
}

// GetPreviousDeployment is not implemented for Cloudflare Pages yet (rollback is a follow-up).
func (d *CloudflareQueuedDeployment) GetPreviousDeployment(_ context.Context) (*deployment.DeploymentInfo, error) {
	return nil, nil
}

// Rollback is not supported for Cloudflare Pages yet.
func (d *CloudflareQueuedDeployment) Rollback(_ context.Context, _ string) error {
	return errors.Errorf("Cloudflare Pages rollback isn't supported yet — redeploy to update the site")
}

func (d *CloudflareQueuedDeployment) sourcePath() string {
	if p, ok := d.spec.Metadata["buildContext"].(string); ok && p != "" {
		return p
	}
	return "."
}

// outputDir resolves the built static directory (absolute).
func (d *CloudflareQueuedDeployment) outputDir() string {
	dir := d.spec.OutputDir
	if dir == "" {
		dir = "dist"
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(d.sourcePath(), dir)
}

// runBuild installs dependencies (if a package.json is present) and runs the build command in the
// source directory. A no-op when there's no build command.
func (d *CloudflareQueuedDeployment) runBuild(ctx context.Context) error {
	if strings.TrimSpace(d.spec.BuildCommand) == "" {
		return nil
	}
	src := d.sourcePath()
	if _, err := os.Stat(src); err != nil {
		return errors.Errorf("source path does not exist: %s", src)
	}
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	env := os.Environ()
	for _, ev := range d.spec.EnvVars {
		env = append(env, fmt.Sprintf("%s=%s", ev.Name, ev.Value))
	}

	if _, err := os.Stat(filepath.Join(src, "package.json")); err == nil {
		install := exec.CommandContext(buildCtx, "npm", "install")
		install.Dir, install.Env, install.Stdout, install.Stderr = src, env, d.writer, d.writer
		if err := install.Run(); err != nil {
			return errors.Errorf("dependency install failed: %w", err)
		}
	}

	build := exec.CommandContext(buildCtx, "sh", "-c", d.spec.BuildCommand)
	build.Dir, build.Env, build.Stdout, build.Stderr = src, env, d.writer, d.writer
	if err := build.Run(); err != nil {
		return errors.Errorf("build command failed: %w", err)
	}
	return nil
}

// dirHasFiles reports whether dir exists and contains at least one regular file.
func dirHasFiles(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(_ string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// SanitizeProjectName normalizes an app name into a valid Cloudflare Pages project name
// (lowercase, alphanumeric + single hyphens, ≤58 chars).
func SanitizeProjectName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastHyphen := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteRune('-')
				lastHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 58 {
		out = strings.Trim(out[:58], "-")
	}
	if out == "" {
		out = "prod-app"
	}
	return out
}
