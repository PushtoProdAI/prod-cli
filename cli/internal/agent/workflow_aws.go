package agent

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

func (w *Workflows) deployAWS(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	slog.Info("deployAWS workflow started", "platform", input.Platform)
	slog.Info("DeployPlan details", "action", input.Action, "source", input.Source, "specName", input.Spec.Name, "specLanguage", input.Spec.Language)

	// Get auth token from session
	token := ""
	session := CtxWorkflowSession(ctx)
	if session != nil {
		token = session.AccessToken
	}

	// Use existing project info from DeployPlan
	existingProject := input.ExistingProjectInfo
	if existingProject.Exists {
		slog.Info("Using existing project from detection", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
	}

	// Log deployment start
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "aws", input.Spec, input.Source, input.Action).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
	}

	// Build deployment spec
	slog.Info("Building deployment spec")
	db := deployment.NewDeploymentBuilder(&input.Spec, input.CollectedEnvVars)
	spec, err := db.Build()
	if err != nil {
		slog.Info("Failed to build deployment spec", "error", err)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "aws", "stage": "spec_build",
			}).Get(ctx)
		}
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}

	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["authToken"] = token
	spec.Metadata["platform"] = "aws"

	if existingProject.Exists {
		spec.IsUpdate = true
		spec.ExistingProjectID = existingProject.ProjectID
		spec.ExistingDatabases = existingProject.ExistingDatabases
	}

	// Deploy to AWS (initiates CloudFormation stack)
	// Note: Cost estimation was already done during the planning phase
	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow": DeployAWSWorkflowName, "activity": AgentDeploySteps,
			"component": "deployment", "platform": "aws", "project_name": input.Spec.Name,
		})
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "aws", "stage": "deploy_steps",
			}).Get(ctx)
		}
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Deployment failed: %v", err)}}, nil
	}

	// Find CloudFormation stack and extract deployment metadata
	var stackName string
	var deploymentURL string
	for _, resource := range createdResources {
		if resource.Type == "cloudformation_stack" {
			if name, ok := resource.Metadata["stackName"].(string); ok {
				stackName = name
			}
			// Store metadata in spec for later use (stack updates)
			if image, ok := resource.Metadata["image"].(string); ok {
				spec.Metadata["pushedImageURL"] = image
			}
			if cpu, ok := resource.Metadata["cpu"].(string); ok {
				spec.Metadata["cpu"] = cpu
			}
			if memory, ok := resource.Metadata["memory"].(string); ok {
				spec.Metadata["memory"] = memory
			}
			if port, ok := resource.Metadata["port"].(int); ok {
				spec.Metadata["port"] = port
			}
			break
		}
	}

	// Poll for CloudFormation stack completion (similar to Render polling)
	if stackName != "" {
		// Configure retry options for CloudFormation stack polling
		// RDS creation can take 10-15 minutes, so we need longer retry window
		stackCheckOpts := ActivityOpts
		stackCheckOpts.RetryOptions.MaxAttempts = 120                     // More attempts
		stackCheckOpts.RetryOptions.FirstRetryInterval = time.Second * 10 // Start with longer interval
		stackCheckOpts.RetryOptions.MaxRetryInterval = time.Second * 30   // Cap at 30 seconds
		stackCheckOpts.RetryOptions.BackoffCoefficient = 1.0              // Linear backoff is fine for polling
		stackCheckOpts.RetryOptions.RetryTimeout = time.Minute * 25       // Total timeout: 25 minutes

		stackOutputs, err := workflow.ExecuteActivity[map[string]string](ctx, stackCheckOpts, AgentWaitForAWSStack, token, stackName).Get(ctx)
		if err != nil {
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":   DeployAWSWorkflowName,
				"activity":   AgentWaitForAWSStack,
				"component":  "deployment",
				"platform":   "aws",
				"stack_name": stackName,
			})
			summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
			if e1 != nil {
				prod_error.CaptureErrorWithContext(e1, map[string]any{
					"workflow":  DeployAWSWorkflowName,
					"activity":  AgentSummarizeError,
					"component": "deployment",
					"platform":  "aws",
					"operation": "summarize_original_error",
				})
				if operationId != "" {
					workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
						"error":      err.Error(),
						"platform":   "aws",
						"stage":      "wait_for_stack",
						"stack_name": stackName,
					}).Get(ctx)
				}
				return deployResult{Error: deployError{Summary: err.Error()}}, nil
			}
			slog.Info("CloudFormation deployment failed", "stackName", stackName, "error", err)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":      err.Error(),
					"platform":   "aws",
					"stage":      "wait_for_stack",
					"stack_name": stackName,
				}).Get(ctx)
			}
			return deployResult{Error: summary}, nil
		}

		// If migration command exists, run ECS migration task before creating App Runner service
		if input.Spec.MigrationCommand != "" {
			slog.Info("Running database migration via ECS Fargate", "command", input.Spec.MigrationCommand)
			migrationResult, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentRunECSMigration, token, stackName, stackOutputs, input.Spec.MigrationCommand).Get(ctx)
			if err != nil {
				prod_error.CaptureErrorWithContext(err, map[string]any{
					"workflow":   DeployAWSWorkflowName,
					"activity":   "AgentRunECSMigration",
					"component":  "deployment",
					"platform":   "aws",
					"stack_name": stackName,
				})
				if operationId != "" {
					workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
						"error":      err.Error(),
						"platform":   "aws",
						"stage":      "run_migration",
						"stack_name": stackName,
					}).Get(ctx)
				}
				return deployResult{Error: deployError{Summary: fmt.Sprintf("Database migration failed: %v", err)}}, nil
			}
			slog.Info("Database migration completed successfully", "logs", migrationResult)
		}

		// Define the appRunnerServiceInfo type here since it's from the activity package
		type appRunnerServiceInfo struct {
			ServiceARN string
			ServiceURL string
		}

		// Check if App Runner service exists in outputs
		// If not, this is a first deploy - need to update stack to add App Runner
		serviceArn, hasAppRunner := stackOutputs["AppRunnerServiceArn"]

		if !hasAppRunner || serviceArn == "" {
			// First deploy: App Runner doesn't exist yet
			// Update CloudFormation stack to add App Runner service (post-migration)
			slog.Info("First deploy detected - updating stack to add App Runner service after migration")

			// Update the spec to enable App Runner creation
			spec.IsUpdate = true // Stack exists now, this is an update

			// Update CloudFormation stack to add App Runner (without rebuilding Docker image)
			slog.Info("Updating CloudFormation stack to add App Runner service")
			_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateAWSStack, token, spec).Get(ctx)
			if err != nil {
				prod_error.CaptureErrorWithContext(err, map[string]any{
					"workflow": DeployAWSWorkflowName, "activity": AgentUpdateAWSStack,
					"component": "deployment", "platform": "aws", "project_name": input.Spec.Name,
					"stage": "add_apprunner_post_migration",
				})
				if operationId != "" {
					workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
						"error": err.Error(), "platform": "aws", "stage": "add_apprunner",
					}).Get(ctx)
				}
				return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to add App Runner to stack: %v", err)}}, nil
			}

			// Wait for stack update to complete and get new outputs with App Runner info
			stackCheckOpts := ActivityOpts
			stackCheckOpts.RetryOptions.MaxAttempts = 60
			stackCheckOpts.RetryOptions.FirstRetryInterval = time.Second * 10
			stackCheckOpts.RetryOptions.MaxRetryInterval = time.Second * 30
			stackCheckOpts.RetryOptions.BackoffCoefficient = 1.0
			stackCheckOpts.RetryOptions.RetryTimeout = time.Minute * 15

			stackOutputs, err = workflow.ExecuteActivity[map[string]string](ctx, stackCheckOpts, AgentWaitForAWSStack, token, stackName).Get(ctx)
			if err != nil {
				prod_error.CaptureErrorWithContext(err, map[string]any{
					"workflow":   DeployAWSWorkflowName,
					"activity":   AgentWaitForAWSStack,
					"component":  "deployment",
					"platform":   "aws",
					"stack_name": stackName,
					"stage":      "wait_for_apprunner_addition",
				})
				if operationId != "" {
					workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
						"error":      err.Error(),
						"platform":   "aws",
						"stage":      "wait_for_apprunner_stack_update",
						"stack_name": stackName,
					}).Get(ctx)
				}
				return deployResult{Error: deployError{Summary: fmt.Sprintf("Stack update to add App Runner failed: %v", err)}}, nil
			}

			// Now extract App Runner info from updated stack outputs
			serviceArn = stackOutputs["AppRunnerServiceArn"]
			slog.Info("App Runner service added to stack", "arn", serviceArn)
		}

		// App Runner exists (update deploy) - extract info from outputs
		slog.Info("App Runner service already exists in stack")

		serviceUrl, ok := stackOutputs["AppRunnerServiceUrl"]
		if !ok || serviceUrl == "" {
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":      "App Runner service URL not found in CloudFormation outputs",
					"platform":   "aws",
					"stage":      "extract_apprunner_info",
					"stack_name": stackName,
				}).Get(ctx)
			}
			return deployResult{Error: deployError{Summary: "App Runner service URL not found in CloudFormation outputs"}}, nil
		}

		serviceInfo := appRunnerServiceInfo{
			ServiceARN: serviceArn,
			ServiceURL: serviceUrl,
		}

		slog.Info("App Runner service info extracted", "arn", serviceArn, "url", serviceUrl)

		// Note: CloudFormation already waits for App Runner to be RUNNING before
		// marking the stack as CREATE_COMPLETE/UPDATE_COMPLETE, so no additional
		// wait is needed here.

		// Set deployment URL from App Runner service
		if serviceInfo.ServiceURL != "" {
			// App Runner ServiceUrl doesn't include the scheme, add https://
			if !strings.HasPrefix(serviceInfo.ServiceURL, "http://") && !strings.HasPrefix(serviceInfo.ServiceURL, "https://") {
				deploymentURL = "https://" + serviceInfo.ServiceURL
			} else {
				deploymentURL = serviceInfo.ServiceURL
			}
		} else {
			// Fallback if URL not returned
			deploymentURL = fmt.Sprintf("https://%s.awsapprunner.com", spec.Name)
		}
	}

	if deploymentURL == "" {
		deploymentURL = "Deployment completed but URL not available"
	}

	path, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineRootPath, input.Spec.Routes).Get(ctx)
	if err != nil {
		path = "/"
	}

	fullUrl, err := url.JoinPath(deploymentURL, path)
	if err != nil {
		fullUrl = deploymentURL
	}

	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, fullUrl).Get(ctx)
	if err != nil {
		slog.Error("Health check failed", "error", err, "url", fullUrl)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "aws", "stage": "url_verification", "url": fullUrl,
			}).Get(ctx)
		}
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Deployment failed health check at %s", fullUrl)}}, nil
	}

	if operationId != "" {
		metadata := map[string]any{
			"url":               fullUrl,
			"platform":          "aws",
			"resources_created": createdResources,
			"stack_name":        stackName,
		}

		// Store the image URL for rollback purposes
		if imageURL, ok := spec.Metadata["pushedImageURL"].(string); ok && imageURL != "" {
			metadata["image_url"] = imageURL
		}

		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", metadata).Get(ctx)
	}

	slog.Info("AWS deployment workflow completed successfully")
	return deployResult{Url: fullUrl}, nil
}
