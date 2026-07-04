package aws

import (
	"context"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// AWSAPIStep is a type alias for AWS-specific API steps
type AWSAPIStep = deployment.Step[AWSClient]

// BaseStep is a type alias for base step functionality
type BaseStep = deployment.BaseStep

// AppRunnerService represents an AWS App Runner service
type AppRunnerService struct {
	ServiceID   string `json:"serviceId"`
	ServiceName string `json:"serviceName"`
	ServiceARN  string `json:"serviceArn"`
	ServiceURL  string `json:"serviceUrl"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// RDSInstance represents an AWS RDS database instance
type RDSInstance struct {
	DBInstanceID     string `json:"dbInstanceIdentifier"`
	DBName           string `json:"dbName"`
	Engine           string `json:"engine"`
	EngineVersion    string `json:"engineVersion"`
	InstanceClass    string `json:"instanceClass"`
	Status           string `json:"status"`
	Endpoint         string `json:"endpoint"`
	Port             int    `json:"port"`
	AllocatedStorage int    `json:"allocatedStorage"`
	CreatedAt        string `json:"createdAt"`
}

// ElastiCacheCluster represents an AWS ElastiCache Redis cluster
type ElastiCacheCluster struct {
	CacheClusterID string `json:"cacheClusterId"`
	Engine         string `json:"engine"`
	EngineVersion  string `json:"engineVersion"`
	CacheNodeType  string `json:"cacheNodeType"`
	Status         string `json:"status"`
	Endpoint       string `json:"endpoint"`
	Port           int    `json:"port"`
	NumCacheNodes  int    `json:"numCacheNodes"`
	CreatedAt      string `json:"createdAt"`
}

// ECRRepository represents an AWS ECR repository
type ECRRepository struct {
	RepositoryName string `json:"repositoryName"`
	RepositoryURI  string `json:"repositoryUri"`
	RepositoryARN  string `json:"repositoryArn"`
	CreatedAt      string `json:"createdAt"`
}

// VPC represents an AWS VPC
type VPC struct {
	VPCID     string   `json:"vpcId"`
	CidrBlock string   `json:"cidrBlock"`
	SubnetIDs []string `json:"subnetIds"`
	IsDefault bool     `json:"isDefault"`
}

// SecurityGroup represents an AWS security group
type SecurityGroup struct {
	GroupID   string `json:"groupId"`
	GroupName string `json:"groupName"`
	VPCID     string `json:"vpcId"`
}

// DBCredentials represents database connection credentials
type DBCredentials struct {
	Username         string `json:"username"`
	Password         string `json:"password"`
	Endpoint         string `json:"endpoint"`
	Port             int    `json:"port"`
	DBName           string `json:"dbName"`
	ConnectionString string `json:"connectionString"`
}

// RedisCredentials represents Redis connection credentials
type RedisCredentials struct {
	Endpoint         string `json:"endpoint"`
	Port             int    `json:"port"`
	AuthToken        string `json:"authToken,omitempty"`
	ConnectionString string `json:"connectionString"`
}

// CreateAppRunnerServiceRequest represents a request to create an App Runner service
type CreateAppRunnerServiceRequest struct {
	ServiceName       string             `json:"serviceName"`
	SourceImageURI    string             `json:"sourceImageUri"`
	CPU               string             `json:"cpu"`
	Memory            string             `json:"memory"`
	Port              int                `json:"port"`
	EnvironmentVars   map[string]string  `json:"environmentVariables"`
	InstanceRoleARN   string             `json:"instanceRoleArn,omitempty"`
	AutoScalingConfig *AutoScalingConfig `json:"autoScalingConfig,omitempty"`
}

// AutoScalingConfig represents App Runner auto-scaling configuration
type AutoScalingConfig struct {
	MinSize        int `json:"minSize"`
	MaxSize        int `json:"maxSize"`
	MaxConcurrency int `json:"maxConcurrency"`
}

// CreateRDSInstanceRequest represents a request to create an RDS instance
type CreateRDSInstanceRequest struct {
	DBInstanceID        string   `json:"dbInstanceIdentifier"`
	DBName              string   `json:"dbName"`
	Engine              string   `json:"engine"`
	EngineVersion       string   `json:"engineVersion"`
	InstanceClass       string   `json:"instanceClass"`
	AllocatedStorage    int      `json:"allocatedStorage"`
	StorageType         string   `json:"storageType"`
	MasterUsername      string   `json:"masterUsername"`
	MasterPassword      string   `json:"masterPassword"`
	VPCSecurityGroupIDs []string `json:"vpcSecurityGroupIds,omitempty"`
	SubnetGroupName     string   `json:"dbSubnetGroupName,omitempty"`
	PubliclyAccessible  bool     `json:"publiclyAccessible"`
}

// CreateElastiCacheClusterRequest represents a request to create an ElastiCache cluster
type CreateElastiCacheClusterRequest struct {
	CacheClusterID   string   `json:"cacheClusterId"`
	Engine           string   `json:"engine"`
	EngineVersion    string   `json:"engineVersion"`
	CacheNodeType    string   `json:"cacheNodeType"`
	NumCacheNodes    int      `json:"numCacheNodes"`
	SecurityGroupIDs []string `json:"securityGroupIds,omitempty"`
	SubnetGroupName  string   `json:"cacheSubnetGroupName,omitempty"`
}

// CreateECRRepositoryRequest represents a request to create an ECR repository
type CreateECRRepositoryRequest struct {
	RepositoryName string            `json:"repositoryName"`
	Tags           map[string]string `json:"tags,omitempty"`
}

// AWSClient defines the interface for interacting with AWS services
type AWSClient interface {
	// App Runner operations
	CreateAppRunnerService(ctx context.Context, req CreateAppRunnerServiceRequest) (*AppRunnerService, error)
	GetAppRunnerService(ctx context.Context, serviceARN string) (*AppRunnerService, error)
	UpdateAppRunnerService(ctx context.Context, serviceARN string, imageURI string, envVars map[string]string) (*AppRunnerService, error)
	DeleteAppRunnerService(ctx context.Context, serviceARN string) error
	ListAppRunnerServices(ctx context.Context) ([]*AppRunnerService, error)

	// RDS operations
	CreateRDSInstance(ctx context.Context, req CreateRDSInstanceRequest) (*RDSInstance, error)
	GetRDSInstance(ctx context.Context, dbInstanceID string) (*RDSInstance, error)
	DescribeRDSInstances(ctx context.Context, dbName string) ([]*RDSInstance, error)
	DeleteRDSInstance(ctx context.Context, dbInstanceID string, skipFinalSnapshot bool) error

	// ElastiCache operations
	CreateElastiCacheCluster(ctx context.Context, req CreateElastiCacheClusterRequest) (*ElastiCacheCluster, error)
	GetElastiCacheCluster(ctx context.Context, cacheClusterID string) (*ElastiCacheCluster, error)
	DescribeElastiCacheClusters(ctx context.Context) ([]*ElastiCacheCluster, error)
	DeleteElastiCacheCluster(ctx context.Context, cacheClusterID string) error

	// ECR operations
	CreateECRRepository(ctx context.Context, req CreateECRRepositoryRequest) (*ECRRepository, error)
	GetECRRepository(ctx context.Context, repositoryName string) (*ECRRepository, error)
	GetECRAuthToken(ctx context.Context) (string, string, error) // Returns username, password, error
	ListECRRepositories(ctx context.Context) ([]*ECRRepository, error)
	DeleteECRRepository(ctx context.Context, repositoryName string, force bool) error

	// VPC and networking operations
	GetDefaultVPC(ctx context.Context) (*VPC, error)
	CreateSecurityGroup(ctx context.Context, vpcID, groupName, description string) (*SecurityGroup, error)
	GetSecurityGroup(ctx context.Context, groupID string) (*SecurityGroup, error)
	AuthorizeSecurityGroupIngress(ctx context.Context, groupID string, port int, sourceGroupID string) error

	// Secrets Manager operations
	StoreSecret(ctx context.Context, secretName string, secretValue map[string]string) (string, error)
	GetSecret(ctx context.Context, secretName string) (map[string]string, error)
	DeleteSecret(ctx context.Context, secretName string, forceDelete bool) error

	// Region and configuration
	GetRegion() string
}
