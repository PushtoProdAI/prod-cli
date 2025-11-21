package aws

// AWSAuthSetup contains the information needed to set up AWS authentication
type AWSAuthSetup struct {
	AccountID  string `json:"account_id"`
	ExternalID string `json:"external_id"`
	Region     string `json:"region"`
}

// BackingService represents a backing service (database, cache, etc.) for AWS deployment
type BackingService struct {
	Type               string            `json:"type"`
	Name               string            `json:"name"`
	Engine             string            `json:"engine,omitempty"`
	InstanceClass      string            `json:"instanceClass,omitempty"`
	AllocatedStorage   int               `json:"allocatedStorage,omitempty"`
	NodeType           string            `json:"nodeType,omitempty"`
	NumCacheNodes      int               `json:"numCacheNodes,omitempty"`
	CacheUsageLimits   *CacheUsageLimits `json:"cacheUsageLimits,omitempty"`
	DailySnapshotTime  string            `json:"dailySnapshotTime,omitempty"`
	MajorEngineVersion string            `json:"majorEngineVersion,omitempty"`
}

// CacheUsageLimits represents the usage limits for Serverless ElastiCache
type CacheUsageLimits struct {
	DataStorage   *DataStorageLimit `json:"dataStorage,omitempty"`
	ECPUPerSecond *ECPULimit        `json:"ecpuPerSecond,omitempty"`
}

// DataStorageLimit represents data storage limits
type DataStorageLimit struct {
	Maximum int    `json:"maximum,omitempty"`
	Unit    string `json:"unit,omitempty"`
}

// ECPULimit represents ECPU per second limits
type ECPULimit struct {
	Maximum int `json:"maximum,omitempty"`
}

// EnvVar represents an environment variable with categorization
type EnvVar struct {
	Name              string `json:"name"`
	Value             string `json:"value,omitempty"`
	Role              string `json:"role,omitempty"`              // "full_uri", "hostname", "port", etc.
	Service           string `json:"service,omitempty"`           // "postgresql", "redis", etc.
	Sensitive         bool   `json:"sensitive,omitempty"`         // true if variable contains sensitive data
	SensitivityReason string `json:"sensitivityReason,omitempty"` // explanation for why variable is sensitive
}

// DeploymentSpec represents the specification for deploying to AWS
type DeploymentSpec struct {
	ServiceName      string           `json:"serviceName"`
	ImageURL         string           `json:"imageUrl"`
	CPU              string           `json:"cpu"`
	Memory           string           `json:"memory"`
	Port             int              `json:"port"`
	EnvVars          []EnvVar         `json:"envVars"`
	BackingServices  []BackingService `json:"backingServices,omitempty"`
	MigrationCommand string           `json:"migrationCommand,omitempty"`
	CreateAppRunner  *bool            `json:"createAppRunner,omitempty"` // If false, skip App Runner creation (for first deploy pre-migration)
}

// DeploymentResult represents the result of an AWS deployment
type DeploymentResult struct {
	StackID   string            `json:"stackId"`
	StackName string            `json:"stackName"`
	Status    string            `json:"status"`
	Outputs   map[string]string `json:"outputs,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// StackCheckRequest represents a request to check if a CloudFormation stack exists
type StackCheckRequest struct {
	StackName string `json:"stackName"`
}

// StackResourceInfo contains information about resources in the stack
type StackResourceInfo struct {
	HasRDS               bool     `json:"hasRDS"`
	HasElastiCache       bool     `json:"hasElastiCache"`
	HasAppRunner         bool     `json:"hasAppRunner"`
	RDSInstances         []string `json:"rdsInstances,omitempty"`
	ElastiCacheInstances []string `json:"elastiCacheInstances,omitempty"`
}

// StackCheckResponse represents the response from checking if a stack exists
type StackCheckResponse struct {
	Exists    bool              `json:"exists"`
	StackID   string            `json:"stackId,omitempty"`
	Status    string            `json:"status,omitempty"`
	Outputs   map[string]string `json:"outputs,omitempty"`
	Resources StackResourceInfo `json:"resources,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// MigrationRequest represents the request to run an ECS migration task
type MigrationRequest struct {
	StackName         string   `json:"stackName"`
	ClusterArn        string   `json:"clusterArn"`
	TaskDefinitionArn string   `json:"taskDefinitionArn"`
	MigrationCommand  string   `json:"migrationCommand"`
	Subnets           []string `json:"subnets"`
	SecurityGroups    []string `json:"securityGroups"`
}

// MigrationResult represents the result of running an ECS migration
type MigrationResult struct {
	Success  bool     `json:"success"`
	ExitCode int      `json:"exitCode"`
	Logs     []string `json:"logs"`
	Error    string   `json:"error,omitempty"`
	TaskArn  string   `json:"taskArn,omitempty"`
}

// TemplatePreviewResponse represents the response from template preview
type TemplatePreviewResponse struct {
	Template    string `json:"template"`
	ServiceName string `json:"serviceName"`
}

// AWSDeploymentSpec is an alias for DeploymentSpec for clarity
type AWSDeploymentSpec = DeploymentSpec
