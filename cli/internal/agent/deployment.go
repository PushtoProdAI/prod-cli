package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/baml_client/types"
	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/backend"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/aws"
	"github.com/pushtoprodai/prod-cli/internal/deployment/flyio"
	"github.com/pushtoprodai/prod-cli/internal/deployment/heroku"
	"github.com/pushtoprodai/prod-cli/internal/deployment/netlify"
	"github.com/pushtoprodai/prod-cli/internal/deployment/render"
	"github.com/pushtoprodai/prod-cli/internal/deployment/vercel"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

func (a *Activities) createDeployable(spec *deployment.DeploymentSpec, platform Platform) (deployment.Deployable, error) {
	switch platform {
	case Render:
		dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
		return render.NewQueuedDeployment(a.renderClient, spec, dockerGen, true, a.uiWriter), nil
	case FlyIO:
		dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
		return flyio.NewFlyioQueuedDeployment(a.flyClient, spec, dockerGen, a.uiWriter), nil
	case Netlify:
		netlifyAdapter := netlify.NewDefaultNetlifyDeploymentAdapter(a.uiWriter, a.llmClient)
		deployable, err := netlifyAdapter.GenerateArtifacts(spec, deployment.StrategyNetlify)
		if err != nil {
			return nil, errors.Errorf("failed to create Netlify deployment: %w", err)
		}
		return deployable, nil
	case Vercel:
		vercelAdapter := vercel.NewDefaultVercelDeploymentAdapter(a.uiWriter, a.llmClient)
		deployable, err := vercelAdapter.GenerateArtifacts(spec, deployment.StrategyVercel)
		if err != nil {
			return nil, errors.Errorf("failed to create Vercel deployment: %w", err)
		}
		return deployable, nil
	case Heroku:
		herokuAdapter := heroku.NewDefaultHerokuDeploymentAdapter(a.uiWriter, a.llmClient)
		deployable, err := herokuAdapter.GenerateArtifacts(spec, deployment.StrategyHeroku)
		if err != nil {
			return nil, errors.Errorf("failed to create Heroku deployment: %w", err)
		}
		return deployable, nil
	case AWS:
		awsClient, err := aws.NewClient("us-east-1")
		if err != nil {
			return nil, errors.Errorf("failed to create AWS client: %w", err)
		}
		// Create DockerGenerator with spec's env vars for build-time variable support
		// This matches the pattern used by Render and FlyIO
		dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
		return aws.NewAWSDeployment(awsClient, spec, dockerGen, true, "us-east-1", a.uiWriter), nil
	default:
		return nil, errors.Errorf("unsupported platform: %s", platform)
	}
}

func (a *Activities) deploySteps(ctx context.Context, spec deployment.DeploymentSpec, platform Platform) ([]deployment.CreatedResource, error) {
	deployable, err := a.createDeployable(&spec, platform)
	if err != nil {
		return nil, err
	}

	createdResources, err := deployable.Deploy(ctx)
	if err != nil {
		// Check for Render HTTP errors
		var renderHTTPErr *render.HTTPError
		if errors.As(err, &renderHTTPErr) {
			if renderHTTPErr.IsClientError() {
				return []deployment.CreatedResource{}, workflow.NewPermanentError(errors.Errorf("failed to execute %s deployment. client error (%d): %s", platform, renderHTTPErr.StatusCode, renderHTTPErr.Message))
			}
		}

		// Check for Heroku HTTP errors
		var herokuHTTPErr *heroku.HTTPError
		if errors.As(err, &herokuHTTPErr) {
			if herokuHTTPErr.IsClientError() {
				return []deployment.CreatedResource{}, workflow.NewPermanentError(errors.Errorf("failed to execute %s deployment. client error (%d): %s", platform, herokuHTTPErr.StatusCode, herokuHTTPErr.Message))
			}
		}

		return []deployment.CreatedResource{}, errors.Errorf("failed to execute %s deployment: %w", platform, err)
	}

	return createdResources, nil
}

func (a *Activities) summarizeDeploySteps(ctx context.Context, steps []string) error {
	a.uiWriter.SendStatus("summarizing", "Summarizing deployment steps")

	var summaryText string
	summary, err := a.llmClient.SummarizeSteps(ctx, steps)
	if err != nil {
		slog.Warn("Failed to get LLM summary, using fallback", "error", err)
		summaryText = "📋 Deployment Steps\n\n"
		summaryText += "The following steps will be executed:\n\n"
		for i, step := range steps {
			summaryText += fmt.Sprintf("%d. %s\n", i+1, step)
		}
		summaryText += "\nNote: Existing resources will be detected and reused automatically.\n"
	} else {
		summaryText = summary.Summary
	}
	a.uiWriter.SendStatusComplete("summarizing", "")

	if tuiWriter, ok := a.uiWriter.(output.InfoBoxWriter); ok {
		slog.Info("Sending info box for deployment steps", "hasContent", summaryText != "")
		tuiWriter.SendInfoBox("Deployment Steps", summaryText, "📋")
	} else {
		slog.Info("Not a TUI writer, using plain text", "writerType", fmt.Sprintf("%T", a.uiWriter))
		fmt.Fprintf(a.uiWriter, "%s\n", summaryText)
	}
	return nil
}

func (a *Activities) estimateRenderCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	ra := render.NewRenderDeploymentAdapter(a.renderClient, a.uiWriter, a.llmClient)
	costs, err := ra.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) estimateFlyioCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	fa := flyio.NewFlyioDeploymentAdapter(a.flyClient, a.uiWriter, a.llmClient)
	costs, err := fa.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) estimateNetlifyCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	na := netlify.NewNetlifyDeploymentAdapter(netlify.NewCLINetlifyClient(), a.uiWriter, a.llmClient)
	costs, err := na.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) estimateVercelCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	va := vercel.NewVercelDeploymentAdapter(vercel.NewCLIVercelClient(), a.uiWriter, a.llmClient)
	costs, err := va.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) estimateHerokuCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	ha := heroku.NewDefaultHerokuDeploymentAdapter(a.uiWriter, a.llmClient)
	costs, err := ha.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) estimateAWSCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	// TODO: Get region from user's AWS credentials in database
	awsClient, err := aws.NewClient("us-east-1")
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to create AWS client: %w", err)
	}
	aa := aws.NewAWSDeploymentAdapter(awsClient, "us-east-1", a.uiWriter, a.llmClient)
	costs, err := aa.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) categorizeEnvVarsForDeployment(ctx context.Context, dbList []string, envVar analyzer.EnvVarCandidate) (deployment.EnvVar, error) {
	slog.Info("CategorizeEnvVarsForDeployment input", "envVar", envVar)
	slog.Info("CategorizeEnvVarsForDeployment dbList", "dbList", dbList)
	slog.Info("CategorizeEnvVarsForDeployment workflow name", "workflowName", CategorizeEnvVarsWorkflowName)

	// Framework-specific vars (Django, Rails, etc.) are categorized generically here,
	// but their actual values are set later in PrepareDeployment (which runs after this).
	// This allows for good UX (user sees all vars) while letting framework handlers control values.

	ev := types.EnvVarCandidate{
		VarName: envVar.VarName,
		Line:    int64(envVar.Line),
		Context: envVar.Context,
		File:    envVar.File,
	}
	cat, err := a.llmClient.DetermineEnvVarRoles(ctx, ev, dbList)
	if err != nil {
		return deployment.EnvVar{}, errors.Errorf("failed to determine env var roles: %w", err)
	}

	// Log sensitivity detection for visibility
	if cat.IsSensitive {
		slog.Info("Detected sensitive environment variable",
			"name", envVar.VarName,
			"reason", cat.SensitivityReason)
	}

	// Send individual completion message (no spinner start/stop to avoid conflicts)
	a.uiWriter.SendStatus("info", fmt.Sprintf("✅ Environment variable: %s categorized", envVar.VarName))

	return deployment.EnvVar{
		Name:              envVar.VarName,
		Role:              cat.Role,
		Service:           cat.DbType,
		Sensitive:         cat.IsSensitive,
		SensitivityReason: cat.SensitivityReason,
	}, nil
}

func (a *Activities) getEnvVarsFromEnvFiles(_ context.Context, path string) ([]deployment.EnvVar, error) {
	a.uiWriter.SendStatus("analyzing", "Analyzing .env files for environment variables...")
	envVars := make([]deployment.EnvVar, 0)
	for _, file := range []string{".env", ".env.local", ".env.development", ".env.production", ".env.example"} {
		fileEnvVars, err := analyzer.ParseEnvFile(path, file)
		if err != nil {
			return envVars, errors.Errorf("failed to parse env file %s: %w", file, err)
		}
		for k, v := range fileEnvVars {
			envVars = append(envVars, deployment.EnvVar{Name: k, Value: v})
		}
	}
	a.uiWriter.SendStatusComplete("analyzing", "✅ Analyzed .env files")
	return envVars, nil
}

func (a *Activities) determineBuildOutput(ctx context.Context, candidate analyzer.BuildOutputCandidate) (string, error) {
	bo := types.BuildOutputCandidate{
		Framework: candidate.Framework,
		Context:   candidate.ConfigContents,
		Default:   candidate.Path,
		Source:    candidate.Source,
	}
	output, err := a.llmClient.DetermineBuildOutput(ctx, bo)
	if err != nil {
		return "", errors.Errorf("failed to determine build output: %w", err)
	}
	return output.Path, nil
}

func (a *Activities) rollbackDeployment(ctx context.Context, spec deployment.DeploymentSpec, platform Platform, targetDeploymentID string) error {
	a.uiWriter.SendStatus("rolling_back", fmt.Sprintf("Rolling back to deployment %s", targetDeploymentID))

	deployable, err := a.createDeployable(&spec, platform)
	if err != nil {
		return err
	}

	err = deployable.Rollback(ctx, targetDeploymentID)
	if err != nil {
		a.uiWriter.SendStatusComplete("rolling_back", fmt.Sprintf("❌ Rollback failed: %v", err))
		return errors.Errorf("failed to rollback %s deployment: %w", platform, err)
	}

	a.uiWriter.SendStatusComplete("rolling_back", "✅ Successfully rolled back to previous working version")
	return nil
}

func (a *Activities) getPreviousDeployment(ctx context.Context, spec deployment.DeploymentSpec, platform Platform) (*deployment.DeploymentInfo, error) {
	deployable, err := a.createDeployable(&spec, platform)
	if err != nil {
		return nil, err
	}

	return deployable.GetPreviousDeployment(ctx)
}

func (a *Activities) waitForAWSStack(ctx context.Context, authToken, stackName string) (map[string]string, error) {
	a.uiWriter.SendStatus("deploying", "Waiting for CloudFormation stack to complete...")

	status, err := a.beClient.GetAWSStackStatus(ctx, authToken, stackName)
	if err != nil {
		return nil, errors.Errorf("failed to get stack status: %w", err)
	}

	// Check for failure states
	if status.Status == "CREATE_FAILED" ||
		status.Status == "ROLLBACK_COMPLETE" ||
		status.Status == "ROLLBACK_FAILED" ||
		status.Status == "UPDATE_ROLLBACK_COMPLETE" ||
		status.Status == "UPDATE_ROLLBACK_FAILED" {
		errorMsg := "CloudFormation stack failed"
		if status.Error != "" {
			errorMsg = status.Error
		}
		return nil, errors.Errorf("CloudFormation deployment failed: %s (status: %s)", errorMsg, status.Status)
	}

	// Check if deployment is complete
	if status.Status != "CREATE_COMPLETE" && status.Status != "UPDATE_COMPLETE" {
		return nil, errors.Errorf("CloudFormation stack not yet complete, current status: %s", status.Status)
	}

	// Return stack outputs on success
	return status.Outputs, nil
}

func (a *Activities) runECSMigration(ctx context.Context, authToken, stackName string, stackOutputs map[string]string, migrationCommand string) (string, error) {
	a.uiWriter.SendStatus("deploying", "Running database migration via ECS Fargate...")

	// Extract required outputs from CloudFormation stack
	clusterArn, ok := stackOutputs["ECSClusterArn"]
	if !ok || clusterArn == "" {
		return "", errors.Errorf("ECS cluster ARN not found in stack outputs")
	}

	taskDefArn, ok := stackOutputs["MigrationTaskDefinitionArn"]
	if !ok || taskDefArn == "" {
		return "", errors.Errorf("ECS task definition ARN not found in stack outputs")
	}

	// Extract public subnets for ECS tasks (need internet access for ECR)
	var subnets []string
	if subnet1, exists := stackOutputs["PublicSubnetAZ1"]; exists && subnet1 != "" {
		subnets = append(subnets, subnet1)
	}
	if subnet2, exists := stackOutputs["PublicSubnetAZ2"]; exists && subnet2 != "" {
		subnets = append(subnets, subnet2)
	}
	if len(subnets) == 0 {
		return "", errors.Errorf("no public subnets found in stack outputs")
	}

	// Extract security group for App Runner (also used for ECS tasks)
	securityGroup, exists := stackOutputs["AppRunnerSecurityGroupId"]
	if !exists || securityGroup == "" {
		return "", errors.Errorf("App Runner security group not found in stack outputs")
	}
	securityGroups := []string{securityGroup}

	// Call backend to run ECS migration
	req := backend.ECSMigrationRequest{
		StackName:         stackName,
		ClusterArn:        clusterArn,
		TaskDefinitionArn: taskDefArn,
		MigrationCommand:  migrationCommand,
		Subnets:           subnets,
		SecurityGroups:    securityGroups,
	}

	result, err := a.beClient.RunECSMigration(ctx, authToken, req)
	if err != nil {
		return "", errors.Errorf("failed to run ECS migration: %w", err)
	}

	// Join log lines into a single string
	logsStr := ""
	if len(result.Logs) > 0 {
		logsStr = strings.Join(result.Logs, "\n")
	}

	if !result.Success || result.ExitCode != 0 {
		return "", errors.Errorf("migration task failed with exit code %d: %s\nLogs:\n%s", result.ExitCode, result.Error, logsStr)
	}

	a.uiWriter.SendStatusComplete("deploying", "✅ Database migration completed successfully")
	return logsStr, nil
}

func (a *Activities) updateAWSStack(ctx context.Context, authToken string, spec *deployment.DeploymentSpec) error {
	a.uiWriter.SendStatus("deploying", "Updating CloudFormation stack to add App Runner...")

	slog.Info("Updating CloudFormation stack", "stackName", spec.Name, "isUpdate", spec.IsUpdate)

	// Extract parameters from spec metadata
	imageURL, ok := spec.Metadata["pushedImageURL"].(string)
	if !ok || imageURL == "" {
		return errors.Errorf("image URL not found in spec metadata")
	}

	cpu, _ := spec.Metadata["cpu"].(string)
	memory, _ := spec.Metadata["memory"].(string)
	port, _ := spec.Metadata["port"].(int)

	if cpu == "" {
		cpu = aws.DefaultCPU
	}
	if memory == "" {
		memory = aws.DefaultMemory
	}
	if port == 0 {
		port = aws.DefaultPort
	}

	// Import the AWS deployment package to use the shared helper
	// We need to reference the package properly
	deploymentSpec, err := aws.BuildAWSDeploymentSpec(
		spec.Name,
		imageURL,
		cpu,
		memory,
		port,
		spec.EnvVars,
		spec.Services,
		spec.MigrationCommand,
		nil, // CreateAppRunner defaults to true in template
	)
	if err != nil {
		return errors.Errorf("failed to build deployment spec: %w", err)
	}

	backendClient := backend.NewClient()
	result, err := backendClient.DeployAWSStack(ctx, authToken, deploymentSpec)
	if err != nil {
		return errors.Errorf("failed to update CloudFormation stack: %w", err)
	}

	if result.Error != "" {
		return errors.Errorf("CloudFormation stack update failed: %s", result.Error)
	}

	a.uiWriter.SendStatusComplete("deploying", "✅ CloudFormation stack updated successfully")
	slog.Info("CloudFormation stack update initiated", "stackId", result.StackID, "status", result.Status)
	return nil
}
