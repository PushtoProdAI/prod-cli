package registry

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"golang.org/x/oauth2"
	artifactregistry "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// arRepoEnsurer ensures the Artifact Registry Docker repository exists. Injectable
// so tests don't hit GCP.
type arRepoEnsurer interface {
	ensureDockerRepo(ctx context.Context, project, region, repo string) error
}

// garRegistry is a Google Artifact Registry (Docker) in the user's own GCP
// project — the GCP analogue of ECR. Like ECR it makes an API call in Credentials
// (ensure the repo exists) and returns a short-lived OAuth2 access token, so it's
// constructed from the user's ADC token source rather than via FromEnv.
type garRegistry struct {
	ensurer arRepoEnsurer
	ts      oauth2.TokenSource
	project string // GCP project id
	region  string // e.g. "us-central1"
	repo    string // Artifact Registry repository (a container for images), e.g. "prod"
}

var _ Registry = (*garRegistry)(nil)

// NewGAR builds a GAR registry from the user's ADC token source, GCP project,
// region, and Artifact Registry repository name.
func NewGAR(ctx context.Context, ts oauth2.TokenSource, project, region, repo string) (Registry, error) {
	if project == "" || region == "" || repo == "" {
		return nil, errors.Errorf("GAR requires a GCP project, region, and repository name")
	}
	svc, err := artifactregistry.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, errors.Errorf("failed to build Artifact Registry client: %w", err)
	}
	return &garRegistry{ensurer: &arService{svc}, ts: ts, project: project, region: region, repo: repo}, nil
}

func (r *garRegistry) Name() string { return "gar" }

// host is the regional Docker endpoint, e.g. "us-central1-docker.pkg.dev".
func (r *garRegistry) host() string { return r.region + "-docker.pkg.dev" }

// image validates/normalizes a project name into an image name within the repo.
func (r *garRegistry) image(project string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(project))
	if !projectNameRe.MatchString(p) {
		return "", errors.Errorf("invalid project name %q for a GAR image: use lowercase letters, digits, and . _ -", project)
	}
	return p, nil
}

func (r *garRegistry) Ref(project, tag string) (string, error) {
	img, err := r.image(project)
	if err != nil {
		return "", err
	}
	if !tagRe.MatchString(tag) {
		return "", errors.Errorf("invalid image tag %q", tag)
	}
	// <region>-docker.pkg.dev/<gcp-project>/<ar-repo>/<image>:<tag>
	return fmt.Sprintf("%s/%s/%s/%s:%s", r.host(), r.project, r.repo, img, tag), nil
}

// Credentials ensures the Artifact Registry repo exists (idempotent) and returns
// a short-lived OAuth2 access token for pushing. Docker auth for GAR uses the
// literal username "oauth2accesstoken" with the access token as the password.
func (r *garRegistry) Credentials(ctx context.Context, project string) (Credentials, error) {
	img, err := r.image(project)
	if err != nil {
		return Credentials{}, err
	}
	if err := r.ensurer.ensureDockerRepo(ctx, r.project, r.region, r.repo); err != nil {
		return Credentials{}, err
	}
	tok, err := r.ts.Token()
	if err != nil {
		return Credentials{}, errors.Errorf("failed to get a GCP access token (is ADC set up? run `gcloud auth application-default login`): %w", err)
	}
	return Credentials{
		URL:        r.host(),
		AuthServer: r.host(),
		Repository: fmt.Sprintf("%s/%s/%s", r.project, r.repo, img),
		Username:   "oauth2accesstoken",
		Token:      tok.AccessToken,
	}, nil
}

// arService is the real Artifact Registry ensurer backed by the REST client.
type arService struct{ svc *artifactregistry.Service }

func (a *arService) ensureDockerRepo(ctx context.Context, project, region, repo string) error {
	parent := fmt.Sprintf("projects/%s/locations/%s", project, region)
	op, err := a.svc.Projects.Locations.Repositories.
		Create(parent, &artifactregistry.Repository{Format: "DOCKER"}).
		RepositoryId(repo).Context(ctx).Do()
	if err != nil {
		var gerr *googleapi.Error
		if stderrors.As(err, &gerr) && gerr.Code == 409 { // already exists — the common case
			return nil
		}
		return errors.Errorf("failed to ensure Artifact Registry repository %q (enable artifactregistry.googleapis.com?): %w", repo, err)
	}

	// A new repo is created via a long-running operation; wait until it's done so
	// the subsequent docker push doesn't race a not-yet-ready repository.
	deadline := time.Now().Add(60 * time.Second)
	for op != nil && !op.Done {
		if time.Now().After(deadline) {
			return errors.Errorf("timed out waiting for Artifact Registry repository %q to be created", repo)
		}
		time.Sleep(2 * time.Second)
		op, err = a.svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return errors.Errorf("failed to poll Artifact Registry repository creation: %w", err)
		}
	}
	return nil
}
