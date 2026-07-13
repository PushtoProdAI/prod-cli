// Package aws deploys container images to AWS App Runner using the user's own
// AWS credentials — build locally, push to the user's ECR, then create or
// redeploy a managed App Runner service. There is no backend, no CloudFormation,
// and no central account.
package aws

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/auth"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/apprunner"
	"github.com/pushtoprodai/prod-cli/internal/deployment/managedcontainer"
	"github.com/pushtoprodai/prod-cli/internal/history"
	prodreg "github.com/pushtoprodai/prod-cli/internal/registry"
)

const (
	defaultPort   = "8080"
	defaultCPU    = "1024" // 1 vCPU
	defaultMemory = "2048" // 2 GB
	waitTimeout   = 15 * time.Minute
)

// Deployment deploys a project to AWS App Runner.
type Deployment struct {
	spec      *deployment.DeploymentSpec
	dockerGen *deployment.DockerGenerator
	history   *history.Store // local deploy history; source of rollback targets (may be nil)
	writer    io.Writer
}

var (
	_ deployment.Deployable = (*Deployment)(nil)
	_ deployment.Destroyer  = (*Deployment)(nil)
)

// NewAppRunnerDeployment builds an App Runner deployable for a project spec. hist is the
// local deploy history used to find rollback targets; it may be nil (rollback then reports
// nothing to roll back to).
func NewAppRunnerDeployment(spec *deployment.DeploymentSpec, dockerGen *deployment.DockerGenerator, hist *history.Store, writer io.Writer) *Deployment {
	return &Deployment{spec: spec, dockerGen: dockerGen, history: hist, writer: writer}
}

// Deploy runs the shared managed-container flow with App Runner as the provider.
func (d *Deployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	return managedcontainer.Run(ctx, d, d.spec, d.dockerGen)
}

// ResourceType is the primary CreatedResource type for App Runner.
func (d *Deployment) ResourceType() string { return "apprunner_service" }

// Prepare resolves AWS credentials, ensures ECR, and returns a deploy step that
// creates/redeploys the App Runner service (with an ECR access role) and waits until
// RUNNING.
func (d *Deployment) Prepare(ctx context.Context, spec *deployment.DeploymentSpec) (prodreg.Registry, managedcontainer.DeployFunc, error) {
	cfg, accountID, err := auth.NewAWSAuth(d.writer).Config(ctx)
	if err != nil {
		return nil, nil, err
	}
	reg := prodreg.NewECR(cfg, accountID)
	dep := apprunner.New(cfg)

	deploy := func(ctx context.Context, imageRef string) (managedcontainer.DeployResult, error) {
		serviceArn, serviceURL, err := d.deployImage(ctx, dep, imageRef)
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}
		return managedcontainer.DeployResult{
			ID: serviceArn, Name: prodreg.Sanitize(spec.Name), URL: "https://" + serviceURL,
			// Record the pushed image ref alongside region+account so a later rollback can
			// find the previous image in local history (App Runner has no native rollback and
			// keeps no image history the API exposes). The prior image itself survives in ECR
			// (unique per-deploy tag, no lifecycle policy), so re-pointing the service at it is
			// all rollback needs.
			Identifiers: map[string]string{"region": cfg.Region, "account": accountID, "imageRef": imageRef},
		}, nil
	}
	return reg, deploy, nil
}

// deployImage creates/updates the App Runner service to run imageRef and waits until it's
// RUNNING. Shared by the initial Deploy and by Rollback (which passes a previous image), so
// the two paths can't drift. Env vars/secrets come from the current spec.
func (d *Deployment) deployImage(ctx context.Context, dep *apprunner.Deployer, imageRef string) (serviceArn, serviceURL string, err error) {
	accessRole, err := dep.EnsureAccessRole(ctx)
	if err != nil {
		return "", "", err
	}
	plain, secrets := splitEnvVars(d.spec.EnvVars)
	serviceArn, err = dep.Deploy(ctx, apprunner.ServiceConfig{
		Name:          prodreg.Sanitize(d.spec.Name),
		ImageRef:      imageRef,
		AccessRoleArn: accessRole,
		Port:          defaultPort,
		CPU:           defaultCPU,
		Memory:        defaultMemory,
		StartCommand:  d.spec.StartCommand,
		EnvVars:       plain,
		Secrets:       secrets,
	})
	if err != nil {
		return "", "", err
	}
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	serviceURL, err = dep.WaitForRunning(waitCtx, serviceArn)
	if err != nil {
		return "", "", err
	}
	return serviceArn, serviceURL, nil
}

// GetPreviousDeployment finds the image to roll back to from local deploy history. App Runner
// has no native rollback and its API doesn't expose per-deployment image history, so prod uses
// the image ref it recorded on each successful deploy. Returns (nil, nil) — never an error —
// when there's nothing to roll back to, so the caller reports "nothing to roll back to" rather
// than failing.
func (d *Deployment) GetPreviousDeployment(_ context.Context) (*deployment.DeploymentInfo, error) {
	if d.history == nil || d.spec.Name == "" {
		return nil, nil
	}
	records, err := d.history.List(0)
	if err != nil {
		slog.Info("App Runner rollback: could not read history, treating as no previous deploy", "error", err)
		return nil, nil
	}

	app := strings.ToLower(strings.TrimSpace(d.spec.Name))
	// This app's AWS records, newest-first (any status) — needed to tell a manual rollback
	// (most-recent record is a completed success) from an auto-rollback after a failed health
	// check (the current deploy isn't a success record yet, so the newest is started/failed).
	var appRecords []history.Record
	for _, r := range records {
		if strings.ToLower(strings.TrimSpace(r.ResourceName)) != app {
			continue
		}
		if history.CanonicalPlatform(r.Platform) != "aws" {
			continue
		}
		appRecords = append(appRecords, r)
	}
	if len(appRecords) == 0 {
		return nil, nil
	}

	// A record we can roll back TO is a successful deploy carrying a recorded image ref.
	target := func(r history.Record) (string, bool) {
		if r.Status != "success" {
			return "", false
		}
		img, _ := r.Metadata["imageRef"].(string)
		return img, img != ""
	}
	_, mostRecentIsSuccess := target(appRecords[0])

	// Pin to the region+account of the most-recent target so we never roll back into a
	// different AWS account/region that happens to reuse the same app name.
	var region, account string
	skippedCurrent := false
	for _, r := range appRecords {
		img, ok := target(r)
		if !ok {
			continue
		}
		rReg, _ := r.Metadata["region"].(string)
		rAcc, _ := r.Metadata["account"].(string)
		if region == "" && account == "" {
			region, account = rReg, rAcc
		}
		if rReg != region || rAcc != account {
			continue
		}
		// Manual rollback: appRecords[0] is the current (successful) deploy — skip it and
		// return the one before. Auto-rollback: the current failing deploy isn't a success
		// record, so don't skip — return the most-recent good image.
		if mostRecentIsSuccess && !skippedCurrent {
			skippedCurrent = true
			continue
		}
		slog.Info("App Runner rollback target", "image", img, "region", region, "account", account)
		return &deployment.DeploymentInfo{ID: img, Status: r.Status, CreatedAt: r.StartedAt.Format(time.RFC3339)}, nil
	}
	return nil, nil
}

// Rollback redeploys a previous image (image-swap — App Runner has no native rollback).
// targetDeploymentID is the previous image ref from GetPreviousDeployment. Env vars/secrets
// come from the current spec: this rolls back the code (image), not today's configuration.
func (d *Deployment) Rollback(ctx context.Context, targetDeploymentID string) error {
	if targetDeploymentID == "" {
		return errors.Errorf("no previous image to roll back to")
	}
	cfg, _, err := auth.NewAWSAuth(d.writer).Config(ctx)
	if err != nil {
		return err
	}
	slog.Info("Rolling back App Runner service to previous image", "service", prodreg.Sanitize(d.spec.Name), "image", targetDeploymentID)
	_, _, err = d.deployImage(ctx, apprunner.New(cfg), targetDeploymentID)
	return err
}

// Destroy deletes the App Runner service.
func (d *Deployment) Destroy(ctx context.Context) error {
	cfg, _, err := auth.NewAWSAuth(d.writer).Config(ctx)
	if err != nil {
		return err
	}
	return apprunner.New(cfg).Delete(ctx, prodreg.Sanitize(d.spec.Name))
}

// splitEnvVars partitions env vars into plain (RuntimeEnvironmentVariables) and
// sensitive (stored in Secrets Manager). PORT is forced to the App Runner port so
// the app listens where App Runner routes.
func splitEnvVars(vars []deployment.EnvVar) (plain, secrets map[string]string) {
	plain = map[string]string{}
	secrets = map[string]string{}
	for _, v := range vars {
		if v.Sensitive {
			secrets[v.Name] = v.Value
		} else {
			plain[v.Name] = v.Value
		}
	}
	plain["PORT"] = defaultPort
	delete(secrets, "PORT")
	if len(secrets) == 0 {
		secrets = nil
	}
	return plain, secrets
}
