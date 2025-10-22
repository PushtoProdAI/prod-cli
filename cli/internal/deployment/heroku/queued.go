package heroku

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
)

// QueuedDeployment represents a queued deployment to Heroku
type QueuedDeployment struct {
	client       *HerokuClient
	spec         *deployment.DeploymentSpec
	writer       io.Writer
	buildContext string
}

// NewQueuedDeployment creates a new queued deployment for Heroku
func NewQueuedDeployment(client *HerokuClient, spec *deployment.DeploymentSpec, writer io.Writer) *QueuedDeployment {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}

	buildContext := "."
	if bc, ok := spec.Metadata["buildContext"].(string); ok {
		buildContext = bc
	}

	return &QueuedDeployment{
		client:       client,
		spec:         spec,
		writer:       writer,
		buildContext: buildContext,
	}
}

// Deploy executes the deployment using API steps
func (qd *QueuedDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	slog.Info("Heroku Deploy called", "IsUpdate", qd.spec.IsUpdate, "ExistingProjectID", qd.spec.ExistingProjectID)
	steps := qd.GenerateAPISteps()

	stepExecutor := NewStepExecutor(qd.client, qd.writer)

	if qd.spec.IsUpdate {
		slog.Info("Calling InjectExistingApp", "ExistingProjectID", qd.spec.ExistingProjectID)
		InjectExistingApp(stepExecutor, qd.client, qd.spec.ExistingProjectID)
	}

	return stepExecutor.ExecuteSteps(ctx, steps)
}

// GenerateAPISteps generates the ordered list of API steps for deployment
func (qd *QueuedDeployment) GenerateAPISteps() []HerokuAPIStep {
	var steps []HerokuAPIStep
	stepCounter := 1

	appStepID := "app"
	var addonStepIDs []string

	if !qd.spec.IsUpdate {
		createAppStepID := fmt.Sprintf("step-%d", stepCounter)
		steps = append(steps, NewCreateHerokuAppStep(
			createAppStepID,
			"Create Heroku application",
			"",
			"us",
		))
		stepCounter++
		appStepID = createAppStepID

		addonStepIDs = qd.createAddonSteps(&steps, appStepID, &stepCounter)
	} else {
		// For updates, keep appStepID as "app" since that's where InjectExistingApp puts it
		addonStepIDs = qd.createMissingAddonSteps(&steps, appStepID, &stepCounter)
	}

	envStepID := ""
	customEnvVars := qd.buildEnvVars()
	dbMappings := qd.getDBEnvVarMappings()

	// Create env step if we have custom env vars OR database mappings to configure
	if len(customEnvVars) > 0 || len(dbMappings) > 0 {
		envStepID = fmt.Sprintf("step-%d", stepCounter)

		var deps []string
		if !qd.spec.IsUpdate {
			deps = append([]string{appStepID}, addonStepIDs...)
		} else if len(addonStepIDs) > 0 {
			// If updating and creating new addons, wait for them
			deps = addonStepIDs
		}

		steps = append(steps, NewConfigureHerokuEnvStep(
			envStepID,
			"Configure environment variables",
			appStepID,
			customEnvVars,
			dbMappings,
			deps,
		))
		stepCounter++
	}

	deployStepID := fmt.Sprintf("step-%d", stepCounter)

	var deployDeps []string
	if !qd.spec.IsUpdate {
		deployDeps = append([]string{appStepID}, addonStepIDs...)
	}
	if envStepID != "" {
		deployDeps = append(deployDeps, envStepID)
	}

	steps = append(steps, NewGitDeployStep(
		deployStepID,
		"Deploy application via Git push",
		appStepID,
		qd.buildContext,
		qd.spec.StartCommand,
		qd.spec.MigrationCommand,
		deployDeps,
	))
	stepCounter++

	if !qd.spec.IsUpdate {
		scaleStepID := fmt.Sprintf("step-%d", stepCounter)
		steps = append(steps, NewScaleHerokuDynosStep(
			scaleStepID,
			"Scale web dynos",
			appStepID,
			1,
			"basic",
			[]string{deployStepID},
		))
	}

	return steps
}

// createAddonSteps creates steps for addon provisioning
func (qd *QueuedDeployment) createAddonSteps(steps *[]HerokuAPIStep, appStepID string, counter *int) []string {
	var addonStepIDs []string

	for _, service := range qd.spec.Services {
		stepID := fmt.Sprintf("step-%d", *counter)

		var plan string
		var description string

		switch service.Provider {
		case "postgresql":
			plan = "heroku-postgresql:essential-0"
			description = "Create PostgreSQL database"

		case "redis":
			plan = "heroku-redis:mini"
			description = "Create Redis instance"

		case "mysql":
			plan = "jawsdb:kitefin"
			description = "Create MySQL database (JawsDB)"

		case "mongodb":
			plan = "ormongo:2g"
			description = "Create MongoDB database (ObjectRocket)"

		default:
			// Skip unsupported services
			continue
		}

		*steps = append(*steps, NewCreateHerokuAddonStep(
			stepID,
			description,
			appStepID,
			service.Provider,
			plan,
			nil,                 // no config
			[]string{appStepID}, // depends on app creation
		))

		addonStepIDs = append(addonStepIDs, stepID)
		*counter++
	}

	return addonStepIDs
}

// createMissingAddonSteps creates addon steps only for services that don't already exist
func (qd *QueuedDeployment) createMissingAddonSteps(steps *[]HerokuAPIStep, appStepID string, counter *int) []string {
	var addonStepIDs []string

	for _, service := range qd.spec.Services {
		// Skip if this addon already exists
		exists := false
		for _, existingDB := range qd.spec.ExistingDatabases {
			if existingDB == service.Provider {
				exists = true
				break
			}
		}
		if exists {
			continue
		}

		stepID := fmt.Sprintf("step-%d", *counter)

		var plan string
		var description string

		switch service.Provider {
		case "postgresql":
			plan = "heroku-postgresql:essential-0"
			description = "Create PostgreSQL database"

		case "redis":
			plan = "heroku-redis:mini"
			description = "Create Redis instance"

		case "mysql":
			plan = "jawsdb:kitefin"
			description = "Create MySQL database (JawsDB)"

		case "mongodb":
			plan = "ormongo:2g"
			description = "Create MongoDB database (ObjectRocket)"

		default:
			// Skip unsupported services
			continue
		}

		*steps = append(*steps, NewCreateHerokuAddonStep(
			stepID,
			description,
			appStepID,
			service.Provider,
			plan,
			nil,
			[]string{},
		))

		addonStepIDs = append(addonStepIDs, stepID)
		*counter++
	}

	return addonStepIDs
}

// filterNonDBEnvVars returns environment variables that are not database-related
func (qd *QueuedDeployment) filterNonDBEnvVars() map[string]string {
	envVars := make(map[string]string)

	for _, envVar := range qd.spec.EnvVars {
		if !envVar.IsDBRelated() {
			envVars[envVar.Name] = envVar.Value
		}
	}

	return envVars
}

// buildEnvVars builds the complete set of environment variables (non-DB only)
// Database URL mappings are handled in ConfigureHerokuEnvStep
func (qd *QueuedDeployment) buildEnvVars() map[string]string {
	return qd.filterNonDBEnvVars()
}

// getDBEnvVarMappings returns a map of custom DB env var names to their Heroku defaults
// e.g., if the app uses MY_CONN_URL but Heroku sets DATABASE_URL, returns {"MY_CONN_URL": "DATABASE_URL"}
func (qd *QueuedDeployment) getDBEnvVarMappings() map[string]string {
	mappings := make(map[string]string)

	for _, envVar := range qd.spec.EnvVars {
		if envVar.Role == deployment.EnvRoleFullURI {
			var herokuDefaultName string
			switch envVar.Service {
			case "postgresql":
				herokuDefaultName = "DATABASE_URL"
			case "redis":
				herokuDefaultName = "REDIS_URL"
			case "mysql":
				herokuDefaultName = "JAWSDB_URL"
			case "mongodb":
				herokuDefaultName = "MONGODB_URI"
			}

			// Only create mapping if custom name differs from Heroku's default
			if herokuDefaultName != "" && envVar.Name != herokuDefaultName {
				mappings[envVar.Name] = herokuDefaultName
			}
		}
	}

	return mappings
}

func (qd *QueuedDeployment) GetCurrentDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	if qd.spec.ExistingProjectID == "" {
		return nil, errors.Errorf("no app name available")
	}

	releases, err := qd.client.ListReleases(ctx, qd.spec.ExistingProjectID)
	if err != nil {
		return nil, errors.Errorf("failed to list releases: %w", err)
	}

	if len(releases) == 0 {
		return nil, errors.Errorf("no releases found for app %s", qd.spec.ExistingProjectID)
	}

	slog.Info("GetCurrentDeployment: found releases", "count", len(releases))
	for i, rel := range releases {
		hasSlug := rel.Slug != nil && rel.Slug.ID != ""
		slog.Info("Release details", "index", i, "version", rel.Version, "status", rel.Status, "hasSlug", hasSlug, "id", rel.ID)
	}

	// Find the most recent succeeded release with a slug (actual deployment)
	// Releases are ordered oldest-first, so we need to find the last one with a slug
	var currentDeployment *deployment.DeploymentInfo
	for _, rel := range releases {
		if rel.Status == "succeeded" && rel.Slug != nil && rel.Slug.ID != "" {
			slog.Info("Found deployment release", "version", rel.Version, "id", rel.ID)
			currentDeployment = &deployment.DeploymentInfo{
				ID:        rel.ID,
				Status:    rel.Status,
				CreatedAt: rel.CreatedAt.String(),
			}
		}
	}

	if currentDeployment == nil {
		return nil, errors.Errorf("no successful deployment releases found for app %s", qd.spec.ExistingProjectID)
	}

	slog.Info("Returning current deployment", "id", currentDeployment.ID)
	return currentDeployment, nil
}

func (qd *QueuedDeployment) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	if qd.spec.ExistingProjectID == "" {
		return nil, errors.Errorf("no app name available")
	}

	releases, err := qd.client.ListReleases(ctx, qd.spec.ExistingProjectID)
	if err != nil {
		return nil, errors.Errorf("failed to list releases: %w", err)
	}

	slog.Info("GetPreviousDeployment: found releases", "count", len(releases))
	for i, rel := range releases {
		hasSlug := rel.Slug != nil && rel.Slug.ID != ""
		slog.Info("All releases", "index", i, "version", rel.Version, "status", rel.Status, "hasSlug", hasSlug, "id", rel.ID)
	}

	currentRelease, err := qd.GetCurrentDeployment(ctx)
	if err != nil {
		slog.Warn("Could not determine current release", "error", err)
	} else {
		slog.Info("Current release determined", "id", currentRelease.ID)
	}

	// Find deployment releases that have a slug, excluding the current one
	// Releases are returned oldest-first, so we need to find the most recent one before current
	var previousDeployment *deployment.DeploymentInfo
	for _, rel := range releases {
		if rel.Status == "succeeded" && rel.Slug != nil && rel.Slug.ID != "" {
			// If this is the current release, stop - the previous one we found is what we want
			if currentRelease != nil && rel.ID == currentRelease.ID {
				slog.Info("Found current release, stopping search", "version", rel.Version, "id", rel.ID)
				break
			}
			// This is a candidate for previous deployment
			slog.Info("Found deployment release candidate", "version", rel.Version, "id", rel.ID, "status", rel.Status)
			previousDeployment = &deployment.DeploymentInfo{
				ID:        rel.ID,
				Status:    rel.Status,
				CreatedAt: rel.CreatedAt.String(),
			}
		}
	}

	if previousDeployment == nil {
		return nil, errors.Errorf("no previous deployment found for app %s (this appears to be the first deployment)", qd.spec.ExistingProjectID)
	}

	slog.Info("Returning previous deployment", "id", previousDeployment.ID)
	return previousDeployment, nil
}

func (qd *QueuedDeployment) Rollback(ctx context.Context, targetDeploymentID string) error {
	if qd.spec.ExistingProjectID == "" {
		return errors.Errorf("no app name available for rollback")
	}

	appName := qd.spec.ExistingProjectID

	slog.Info("Rolling back Heroku release", "app", appName, "targetRelease", targetDeploymentID)

	// Get current formation before rollback to restore it after
	formations, err := qd.client.ListFormations(ctx, appName)
	if err != nil {
		slog.Warn("Failed to get current formations, will default to 1 web dyno", "error", err)
	}

	// Find web dyno count
	webQuantity := 1 // default
	for _, formation := range formations {
		if formation.Type == "web" {
			webQuantity = formation.Quantity
			slog.Info("Found current web dyno count", "quantity", webQuantity)
			break
		}
	}

	_, err = qd.client.RollbackRelease(ctx, appName, targetDeploymentID)
	if err != nil {
		return errors.Errorf("failed to rollback to release %s: %w", targetDeploymentID, err)
	}

	slog.Info("Release rolled back successfully, now restoring web dyno count", "quantity", webQuantity)

	// After rollback, restore the web dyno count
	// Heroku rollback creates a new release that may not preserve dyno counts
	_, err = qd.client.UpdateFormation(ctx, appName, "web", &webQuantity, nil)
	if err != nil {
		slog.Warn("Failed to scale web dynos after rollback, app may not be accessible", "error", err)
		// Don't fail the rollback if scaling fails - the code is rolled back
	} else {
		slog.Info("Web dynos restored after rollback", "quantity", webQuantity)
	}

	return nil
}
