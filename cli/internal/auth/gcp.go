package auth

import (
	"context"
	"io"
	"os"

	"github.com/go-errors/errors"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// gcpScope is the scope needed for both the Artifact Registry / Cloud Run admin
// APIs and the docker push.
const gcpScope = "https://www.googleapis.com/auth/cloud-platform"

// GCPAuth resolves the user's GCP credentials from Application Default
// Credentials (ADC): GOOGLE_APPLICATION_CREDENTIALS, `gcloud auth
// application-default login`, or the metadata server. There is no backend and no
// central account — prod deploys into the user's own GCP project with their own
// credentials, like the gcloud CLI.
type GCPAuth struct {
	out     io.Writer
	project string // optional override; else creds ProjectID / GOOGLE_CLOUD_PROJECT
	region  string // optional override; else PROD_GCP_REGION / us-central1
}

var _ AuthProvider = (*GCPAuth)(nil)

func NewGCPAuth(out io.Writer) *GCPAuth { return &GCPAuth{out: out} }

func (g *GCPAuth) SetProject(project string) { g.project = project }
func (g *GCPAuth) SetRegion(region string)   { g.region = region }

// Config resolves the ADC token source, GCP project, and region. Cloud Run and
// Artifact Registry build their clients from the returned token source (which
// carries the cloud-platform scope).
func (g *GCPAuth) Config(ctx context.Context) (ts oauth2.TokenSource, project, region string, err error) {
	creds, err := google.FindDefaultCredentials(ctx, gcpScope)
	if err != nil {
		return nil, "", "", errors.Errorf("no GCP credentials — run `gcloud auth application-default login` or set GOOGLE_APPLICATION_CREDENTIALS: %w", err)
	}

	project = firstNonEmpty(g.project, creds.ProjectID, os.Getenv("GOOGLE_CLOUD_PROJECT"))
	if project == "" {
		return nil, "", "", errors.Errorf("no GCP project — set GOOGLE_CLOUD_PROJECT or run `gcloud config set project <id>`")
	}

	region = firstNonEmpty(g.region, os.Getenv("PROD_GCP_REGION"), "us-central1")
	return creds.TokenSource, project, region, nil
}

// CheckAuthentication reports whether usable GCP credentials are configured.
func (g *GCPAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	if _, _, _, err := g.Config(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (g *GCPAuth) ValidateAPIKey(_ context.Context, _ string) (bool, error) { return false, nil }

func (g *GCPAuth) PerformOAuthLogin(_ context.Context) error {
	return errors.Errorf("GCP uses your local Application Default Credentials — run `gcloud auth application-default login`; there's nothing to log in to here")
}

func (g *GCPAuth) APIKeyPrompt() string { return "" }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
