package heroku

import (
	"context"
	"fmt"
	"io"

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
	steps := qd.GenerateAPISteps()

	stepExecutor := NewStepExecutor(qd.client, qd.writer)

	if qd.spec.IsUpdate {
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
		appStepID = qd.spec.ExistingProjectID
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
	return nil, errors.Errorf("rollback not yet implemented for Heroku")
}

func (qd *QueuedDeployment) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	return nil, errors.Errorf("rollback not yet implemented for Heroku")
}

func (qd *QueuedDeployment) Rollback(ctx context.Context, targetDeploymentID string) error {
	return errors.Errorf("rollback not yet implemented for Heroku")
}
