package heroku

import (
	"context"
	"fmt"
	"io"

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
		stepExecutor.InjectExistingApp(qd.spec.ExistingProjectID)
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
	}

	envStepID := ""
	if customEnvVars := qd.filterNonDBEnvVars(); len(customEnvVars) > 0 {
		envStepID = fmt.Sprintf("step-%d", stepCounter)

		var deps []string
		if !qd.spec.IsUpdate {
			deps = append([]string{appStepID}, addonStepIDs...)
		}

		steps = append(steps, NewConfigureHerokuEnvStep(
			envStepID,
			"Configure environment variables",
			appStepID,
			customEnvVars,
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
