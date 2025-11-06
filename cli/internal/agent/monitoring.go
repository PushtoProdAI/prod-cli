package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/aws"
	"github.com/meroxa/prod/cli/internal/deployment/render"
)

func (a *Activities) getRenderWorkspace(ctx context.Context) (string, error) {
	a.uiWriter.SendStatus("retrieving", "Retrieving Render workspace details...")
	workspaces, err := a.renderClient.ListWorkspaces(ctx)
	if err != nil {
		a.uiWriter.SendStatusComplete("retrieving", "❌ Failed to retrieve workspace details")
		var httpErr *render.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.IsClientError() {
				return "", workflow.NewPermanentError(errors.Errorf("failed to list workspaces. client error (%d): %s", httpErr.StatusCode, httpErr.Message))
			}
			if httpErr.IsServerError() {
				return "", errors.Errorf("failed to list workspaces. server error (%d): %s", httpErr.StatusCode, httpErr.Message)
			}
		}
		return "", errors.Errorf("failed to list workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		a.uiWriter.SendStatusComplete("retrieving", "❌ No workspaces found")
		return "", errors.Errorf("no workspaces found")
	}

	ownerID := workspaces[0].Owner.ID
	a.uiWriter.SendStatusComplete("retrieving", "✅ Workplace details retrieved")
	return ownerID, nil
}

func (a *Activities) getRenderServiceURL(ctx context.Context, serviceID string) (string, error) {
	service, err := a.renderClient.GetWebService(ctx, serviceID)
	if err != nil {
		return "", errors.Errorf("failed to get service info: %w", err)
	}
	return service.ServiceDetails.URL, nil
}

func (a *Activities) waitForRenderDeploy(ctx context.Context, serviceID, deployID string) error {
	a.uiWriter.SendStatus("deploying", "Waiting for deployment to complete...")

	deploy, err := a.renderClient.GetDeploy(ctx, serviceID, deployID)
	if err != nil {
		return errors.Errorf("failed to get deploy status: %w", err)
	}

	if deploy.Status == "build_failed" || deploy.Status == "update_failed" || deploy.Status == "deactivated" {
		return errors.Errorf("deployment failed with status: %s", deploy.Status)
	}

	if deploy.Status != "live" {
		return errors.Errorf("deployment not yet live, current status: %s", deploy.Status)
	}

	return nil
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

func (a *Activities) getFlyIOAppURL(ctx context.Context, appID string) (string, error) {
	service, err := a.flyClient.GetApp(ctx, appID)
	if err != nil {
		return "", errors.Errorf("failed to get service info: %w", err)
	}
	return service.Hostname, nil
}

func (a *Activities) isURLLive(ctx context.Context, url string) error {
	// we could also use the deploys endpoint and check the status of the latest deploy,
	// but using the URL saves us on the rate limiting and ultimately is what the user cares about
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	a.uiWriter.SendStatus("deploying", "Waiting for URL to be live...")
	resp, err := client.Get(url)
	if err != nil {
		return errors.Errorf("failed to make GET request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 300 {
		return errors.Errorf("received non-success status code %d from %s", resp.StatusCode, url)
	}
	a.uiWriter.SendStatusComplete("deploying", "✅ URL is live")
	return nil
}

func (a *Activities) determineRootPath(ctx context.Context, routes []analyzer.RouteCandidate) (string, error) {
	a.uiWriter.SendStatus("analyzing", "Determining root path of your application")
	routeInputs := make([]types.RouteCandidate, len(routes))
	for i, r := range routes {
		routeInputs[i] = types.RouteCandidate{
			Method:  r.Method,
			Context: r.Context,
			File:    r.File,
			Path:    r.Path,
			Line:    int64(r.Line),
		}
	}
	r, err := a.llmClient.CategorizeRoutes(ctx, routeInputs)
	if err != nil {
		return "", errors.Errorf("failed to categorize routes: %w", err)
	}
	// just grab the recommend path from the LLM. The data comes back scored with a confidence, so
	// we can do more verification if needed
	a.uiWriter.SendStatusComplete("analyzing", "✅ root path determined")
	return r.Recommended.Path, nil
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

type appRunnerServiceInfo struct {
	ServiceARN string
	ServiceURL string
}

func (a *Activities) createAppRunnerService(ctx context.Context, authToken string, spec *deployment.DeploymentSpec, stackOutputs map[string]string, imageURL, cpu, memory string, port int) (appRunnerServiceInfo, error) {
	a.uiWriter.SendStatus("deploying", "Creating App Runner service...")

	slog.Info("createAppRunnerService called", "port", port, "envVarsCount", len(spec.EnvVars))

	// Extract VPC configuration from stack outputs
	vpcId, ok := stackOutputs["VPCId"]
	if !ok || vpcId == "" {
		return appRunnerServiceInfo{}, errors.Errorf("VPC ID not found in stack outputs")
	}

	// Extract subnets - can be either comma-separated in one output or multiple outputs
	var subnets []string
	if subnet1, exists := stackOutputs["PrivateSubnetAZ1"]; exists && subnet1 != "" {
		subnets = append(subnets, subnet1)
	}
	if subnet2, exists := stackOutputs["PrivateSubnetAZ2"]; exists && subnet2 != "" {
		subnets = append(subnets, subnet2)
	}
	if len(subnets) == 0 {
		return appRunnerServiceInfo{}, errors.Errorf("no private subnets found in stack outputs")
	}

	// Extract security group
	securityGroup, exists := stackOutputs["AppRunnerSecurityGroupId"]
	if !exists || securityGroup == "" {
		return appRunnerServiceInfo{}, errors.Errorf("App Runner security group not found in stack outputs")
	}
	securityGroups := []string{securityGroup}

	// Extract IAM role ARNs
	accessRoleArn, exists := stackOutputs["AppRunnerAccessRoleArn"]
	if !exists || accessRoleArn == "" {
		return appRunnerServiceInfo{}, errors.Errorf("App Runner access role ARN not found in stack outputs")
	}

	instanceRoleArn, exists := stackOutputs["AppRunnerInstanceRoleArn"]
	if !exists || instanceRoleArn == "" {
		return appRunnerServiceInfo{}, errors.Errorf("App Runner instance role ARN not found in stack outputs")
	}

	// Convert deployment spec environment variables to backend.EnvVar format
	envVars := make([]backend.EnvVar, 0, len(spec.EnvVars)+1)
	hasPort := false
	for _, ev := range spec.EnvVars {
		envVars = append(envVars, backend.EnvVar{
			Name:    ev.Name,
			Value:   ev.Value,
			Role:    ev.Role,
			Service: ev.Service,
		})
		if ev.Name == "PORT" {
			hasPort = true
		}
	}

	// Add PORT environment variable if not already present
	// This ensures the application listens on the correct port that App Runner expects
	if !hasPort {
		envVars = append(envVars, backend.EnvVar{
			Name:  "PORT",
			Value: fmt.Sprintf("%d", port),
			Role:  "user",
		})
		slog.Info("Added PORT environment variable", "port", port)
	}

	// Extract database connection information from CloudFormation outputs
	// and populate database-related env vars
	for i := range envVars {
		ev := &envVars[i]
		if ev.Role == deployment.EnvRoleFullURI && ev.Service == "postgresql" {
			// Look for RDS endpoint in stack outputs
			// CloudFormation outputs database endpoint as {ServiceName}Endpoint
			dbName := strings.ReplaceAll(ev.Service, "-", "")
			if endpoint, exists := stackOutputs[dbName+"Endpoint"]; exists && endpoint != "" {
				if dbPort, portExists := stackOutputs[dbName+"Port"]; portExists && dbPort != "" {
					// Build connection string (password will be injected at runtime by CloudFormation in ECS)
					// For App Runner, we need to use Secrets Manager ARN or set via CloudFormation
					// Since we can't resolve secrets here, log a warning
					slog.Warn("Database endpoint found but password resolution not implemented for App Runner",
						"endpoint", endpoint, "port", dbPort, "envVar", ev.Name)
				}
			}
		}
	}

	// Build App Runner service request
	req := backend.AppRunnerServiceRequest{
		ServiceName: spec.Name,
		ImageURL:    imageURL,
		CPU:         cpu,
		Memory:      memory,
		Port:        port,
		EnvVars:     envVars,
		VPCConfig: &backend.VPCConfig{
			VpcId:          vpcId,
			Subnets:        subnets,
			SecurityGroups: securityGroups,
		},
		RoleArns: &backend.RoleArns{
			AccessRoleArn:   accessRoleArn,
			InstanceRoleArn: instanceRoleArn,
		},
	}

	result, err := a.beClient.CreateAppRunnerService(ctx, authToken, req)
	if err != nil {
		return appRunnerServiceInfo{}, errors.Errorf("failed to create App Runner service: %w", err)
	}

	if !result.Success {
		return appRunnerServiceInfo{}, errors.Errorf("App Runner service creation failed: %s", result.Error)
	}

	a.uiWriter.SendStatusComplete("deploying", "✅ App Runner service created successfully")
	return appRunnerServiceInfo{
		ServiceARN: result.ServiceArn,
		ServiceURL: result.ServiceURL,
	}, nil
}

func (a *Activities) waitForAppRunnerService(ctx context.Context, authToken, serviceArn string) error {
	a.uiWriter.SendStatus("deploying", "Waiting for App Runner service to become ready...")

	status, err := a.beClient.GetAppRunnerStatus(ctx, authToken, serviceArn)
	if err != nil {
		return errors.Errorf("failed to get App Runner status: %w", err)
	}

	// Check for failure states
	if status.Status == "CREATE_FAILED" ||
		status.Status == "OPERATION_FAILED" ||
		status.Status == "DELETE_FAILED" {
		errorMsg := "App Runner service failed"
		if status.Error != "" {
			errorMsg = status.Error
		}
		return errors.Errorf("App Runner service deployment failed: %s (status: %s)", errorMsg, status.Status)
	}

	// Check if deployment is complete
	if status.Status != "RUNNING" {
		return errors.Errorf("App Runner service not yet ready, current status: %s", status.Status)
	}

	// Success
	return nil
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
		cpu = "1024"
	}
	if memory == "" {
		memory = "2048"
	}
	if port == 0 {
		port = 8080
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
