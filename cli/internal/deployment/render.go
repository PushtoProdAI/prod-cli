package deployment

import (
	"context"
	"fmt"
)

type RenderAPIStep struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Method      string         `json:"method"`
	Endpoint    string         `json:"endpoint"`
	Payload     map[string]any `json:"payload"`
	DependsOn   []string       `json:"dependsOn,omitempty"`
}

type RenderProject struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	OwnerID     string `json:"ownerId"`
	TeamID      string `json:"teamId,omitempty"`
	Environment string `json:"environment"`
}

type CreateProjectRequest struct {
	Name        string `json:"name"`
	TeamID      string `json:"teamId,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type CreateWebServiceRequest struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Repo         string            `json:"repo,omitempty"`
	Branch       string            `json:"branch,omitempty"`
	BuildCommand string            `json:"buildCommand,omitempty"`
	StartCommand string            `json:"startCommand,omitempty"`
	EnvVars      map[string]string `json:"envVars,omitempty"`
}

type CreatePostgresRequest struct {
	Name         string `json:"name"`
	DatabaseName string `json:"databaseName,omitempty"`
	User         string `json:"user,omitempty"`
}

type CreateRedisRequest struct {
	Name string `json:"name"`
}

type PostgresConnectionInfo struct {
	InternalConnectionString string `json:"internalConnectionString"`
	ExternalConnectionString string `json:"externalConnectionString"`
}

type RedisConnectionInfo struct {
	InternalConnectionString string `json:"internalConnectionString"`
	ExternalConnectionString string `json:"externalConnectionString"`
}

type RenderService struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type RenderClient interface {
	// Projects
	CreateProject(ctx context.Context, req CreateProjectRequest) (*RenderProject, error)
	GetProject(ctx context.Context, projectID string) (*RenderProject, error)
	ListProjects(ctx context.Context) ([]*RenderProject, error)
	DeleteProject(ctx context.Context, projectID string) error

	// Services
	CreateWebService(ctx context.Context, req CreateWebServiceRequest) (*RenderService, error)
	CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*RenderService, error)
	CreateRedis(ctx context.Context, req CreateRedisRequest) (*RenderService, error)
	
	// Connection Info
	GetPostgresConnectionInfo(ctx context.Context, serviceID string) (*PostgresConnectionInfo, error)
	GetRedisConnectionInfo(ctx context.Context, serviceID string) (*RedisConnectionInfo, error)
}

type RenderDeploymentAdapter struct {
	client RenderClient
}

func NewRenderDeploymentAdapter(client RenderClient) *RenderDeploymentAdapter {
	return &RenderDeploymentAdapter{
		client: client,
	}
}

func (rda *RenderDeploymentAdapter) SupportedStrategies() []DeploymentStrategy {
	return []DeploymentStrategy{
		StrategyDockerfile,
		StrategyAPICall,
		StrategyRenderBlueprint,
		StrategyRenderQueued,
	}
}

func (rda *RenderDeploymentAdapter) GenerateArtifacts(spec *DeploymentSpec, strategy DeploymentStrategy) (Deployable, error) {
	switch strategy {
	case StrategyAPICall:
		return &RenderAPIDeployment{
			client: rda.client,
			spec:   spec,
		}, nil
	case StrategyRenderBlueprint:
		return &RenderBlueprintDeployment{
			client: rda.client,
			spec:   spec,
		}, nil
	case StrategyDockerfile:
		return &RenderDockerDeployment{
			client: rda.client,
			spec:   spec,
		}, nil
	case StrategyRenderQueued:
		return &RenderQueuedDeployment{
			client: rda.client,
			spec:   spec,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported strategy: %s", strategy)
	}
}

type RenderAPIDeployment struct {
	client RenderClient
	spec   *DeploymentSpec
}

func (rad *RenderAPIDeployment) Deploy(ctx context.Context) error {
	project, err := rad.client.CreateProject(ctx, CreateProjectRequest{
		Name:        fmt.Sprintf("project-%s", rad.spec.ProjectID),
		Environment: "production",
	})
	if err != nil {
		return fmt.Errorf("failed to create project: %w", err)
	}

	rad.spec.ProjectID = project.ID

	// Deploy backing services first and collect connection strings
	envVars := make(map[string]string)
	serviceCount := make(map[string]int)
	
	for provider := range rad.spec.ServiceCounts() {
		serviceCount[provider]++
		service, err := rad.deployServiceByProvider(ctx, provider, serviceCount[provider])
		if err != nil {
			return fmt.Errorf("failed to deploy service %s: %w", provider, err)
		}
		
		// Fetch connection string separately after creation
		connStr, err := rad.getConnectionString(ctx, service.ID, provider)
		if err != nil {
			return fmt.Errorf("failed to get connection string for %s: %w", provider, err)
		}
		
		// Add connection string as environment variable
		if connStr != "" {
			envVarName := rad.getConnectionStringEnvVarName(provider)
			envVars[envVarName] = connStr
		}
		
		fmt.Printf("Created %s service: %s (ID: %s)\n", provider, service.Name, service.ID)
	}

	// Deploy web service with environment variables
	webService, err := rad.client.CreateWebService(ctx, CreateWebServiceRequest{
		Name:         fmt.Sprintf("%s-web", rad.spec.ProjectID),
		Type:         "web_service",
		BuildCommand: "npm run build", // Default build command, could be configurable
		StartCommand: "npm start",     // Default start command, could be configurable
		EnvVars:      envVars,
	})
	if err != nil {
		return fmt.Errorf("failed to deploy web service: %w", err)
	}

	fmt.Printf("Created web service: %s (ID: %s)\n", webService.Name, webService.ID)
	return nil
}

func (rad *RenderAPIDeployment) deployServiceByProvider(ctx context.Context, provider string, count int) (*RenderService, error) {
	switch provider {
	case "postgresql":
		return rad.client.CreatePostgres(ctx, CreatePostgresRequest{
			Name:         fmt.Sprintf("%s-postgres-%d", rad.spec.ProjectID, count),
			DatabaseName: fmt.Sprintf("%s_db", rad.spec.ProjectID),
		})
	case "redis":
		return rad.client.CreateRedis(ctx, CreateRedisRequest{
			Name: fmt.Sprintf("%s-redis-%d", rad.spec.ProjectID, count),
		})
	default:
		return nil, fmt.Errorf("unsupported service provider: %s", provider)
	}
}

func (rad *RenderAPIDeployment) getConnectionString(ctx context.Context, serviceID, provider string) (string, error) {
	switch provider {
	case "postgresql":
		connInfo, err := rad.client.GetPostgresConnectionInfo(ctx, serviceID)
		if err != nil {
			return "", err
		}
		return connInfo.InternalConnectionString, nil
	case "redis":
		connInfo, err := rad.client.GetRedisConnectionInfo(ctx, serviceID)
		if err != nil {
			return "", err
		}
		return connInfo.InternalConnectionString, nil
	default:
		return "", fmt.Errorf("unsupported service provider: %s", provider)
	}
}

func (rad *RenderAPIDeployment) getConnectionStringEnvVarName(provider string) string {
	switch provider {
	case "postgresql":
		return "DATABASE_URL"
	case "redis":
		return "REDIS_URL"
	default:
		return fmt.Sprintf("%s_URL", provider)
	}
}

type RenderBlueprintDeployment struct {
	client RenderClient
	spec   *DeploymentSpec
}

func (rbd *RenderBlueprintDeployment) Deploy(ctx context.Context) error {
	// TODO: implement blueprint deployment
	return fmt.Errorf("blueprint deployment not yet implemented")
}

type RenderDockerDeployment struct {
	client RenderClient
	spec   *DeploymentSpec
}

func (rdd *RenderDockerDeployment) Deploy(ctx context.Context) error {
	// TODO: implement docker deployment
	return fmt.Errorf("docker deployment not yet implemented")
}

type RenderQueuedDeployment struct {
	client RenderClient
	spec   *DeploymentSpec
}

func (rqd *RenderQueuedDeployment) Deploy(ctx context.Context) error {
	steps := rqd.generateAPISteps()
	
	// Log each step
	fmt.Println("Generated API Steps:")
	for i, step := range steps {
		fmt.Printf("%d. %s\n", i+1, step.Description)
		fmt.Printf("   Method: %s, Endpoint: %s\n", step.Method, step.Endpoint)
		if len(step.DependsOn) > 0 {
			fmt.Printf("   Depends on: %v\n", step.DependsOn)
		}
		fmt.Println()
	}
	
	return nil
}

func (rqd *RenderQueuedDeployment) generateAPISteps() []RenderAPIStep {
	var steps []RenderAPIStep
	stepCounter := 1
	serviceCount := make(map[string]int)

	// Step 1: Create project
	steps = append(steps, RenderAPIStep{
		ID:          fmt.Sprintf("step-%d", stepCounter),
		Description: fmt.Sprintf("Create project '%s' in production environment", rqd.spec.ProjectID),
		Method:      "POST",
		Endpoint:    "/v1/projects",
		Payload: map[string]any{
			"name":        fmt.Sprintf("project-%s", rqd.spec.ProjectID),
			"environment": "production",
		},
	})
	stepCounter++

	// Steps 2+: Create backing services and fetch their connection info
	projectStepID := fmt.Sprintf("step-%d", stepCounter-1)
	var connectionSteps []string
	
	for provider := range rqd.spec.ServiceCounts() {
		serviceCount[provider]++
		var endpoint, description string
		var payload map[string]any
		
		switch provider {
		case "postgresql":
			endpoint = "/v1/postgres"
			description = fmt.Sprintf("Create PostgreSQL database service (%s-postgres-%d)", rqd.spec.ProjectID, serviceCount[provider])
			payload = map[string]any{
				"name":         fmt.Sprintf("%s-postgres-%d", rqd.spec.ProjectID, serviceCount[provider]),
				"databaseName": fmt.Sprintf("%s_db", rqd.spec.ProjectID),
			}
		case "redis":
			endpoint = "/v1/redis"
			description = fmt.Sprintf("Create Redis key-value store service (%s-redis-%d)", rqd.spec.ProjectID, serviceCount[provider])
			payload = map[string]any{
				"name": fmt.Sprintf("%s-redis-%d", rqd.spec.ProjectID, serviceCount[provider]),
			}
		default:
			continue
		}

		// Create service step
		createStepID := fmt.Sprintf("step-%d", stepCounter)
		steps = append(steps, RenderAPIStep{
			ID:          createStepID,
			Description: description,
			Method:      "POST",
			Endpoint:    endpoint,
			Payload:     payload,
			DependsOn:   []string{projectStepID},
		})
		stepCounter++

		// Fetch connection info step
		connectionStepID := fmt.Sprintf("step-%d", stepCounter)
		var connectionEndpoint, connectionDescription string
		switch provider {
		case "postgresql":
			connectionEndpoint = "/v1/postgres/{serviceId}/connection-info"
			connectionDescription = "Retrieve PostgreSQL connection information"
		case "redis":
			connectionEndpoint = "/v1/redis/{serviceId}/connection-info"
			connectionDescription = "Retrieve Redis connection information"
		}

		steps = append(steps, RenderAPIStep{
			ID:          connectionStepID,
			Description: connectionDescription,
			Method:      "GET",
			Endpoint:    connectionEndpoint,
			Payload:     map[string]any{},
			DependsOn:   []string{createStepID},
		})
		connectionSteps = append(connectionSteps, connectionStepID)
		stepCounter++
	}

	// Final step: Create web service with environment variables from connection strings
	var dependsOn []string
	if len(connectionSteps) > 0 {
		dependsOn = connectionSteps
	} else {
		dependsOn = []string{projectStepID}
	}

	envVars := map[string]any{}
	for provider := range rqd.spec.ServiceCounts() {
		switch provider {
		case "postgresql":
			envVars["DATABASE_URL"] = "{postgres_internal_connection_string}"
		case "redis":
			envVars["REDIS_URL"] = "{redis_internal_connection_string}"
		}
	}

	steps = append(steps, RenderAPIStep{
		ID:          fmt.Sprintf("step-%d", stepCounter),
		Description: "Create web service with database connection environment variables",
		Method:      "POST",
		Endpoint:    "/v1/services",
		Payload: map[string]any{
			"name":         fmt.Sprintf("%s-web", rqd.spec.ProjectID),
			"type":         "web_service",
			"buildCommand": "npm run build",
			"startCommand": "npm start",
			"envVars":      envVars,
		},
		DependsOn: dependsOn,
	})

	return steps
}
