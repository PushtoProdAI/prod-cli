# Infrastructure

This directory contains AWS CloudFormation templates for managing Prod's AWS infrastructure and customer deployment capabilities.

## Templates

### cloudformation-deploy-role-template.yaml

IAM role template deployed to customer AWS accounts during authentication setup. This template is deployed to S3 by GitHub Actions and referenced by the Prod CLI when customers authenticate.

**Deployment:** Automatically uploaded to S3 by GitHub Actions workflow (`.github/workflows/deploy-cloudformation-template.yaml`) when changes are pushed to main or manually triggered for production.

**Resources Created:**
- `ProdDeployRole` - Main IAM role that Prod assumes to deploy resources
- Managed policies for specific AWS services:
  - App Runner service management (create, update, delete, autoscaling, VPC connectors)
  - RDS database management (instances, subnet groups, parameter groups, snapshots)
  - ECR repository and image operations
  - VPC networking (security groups, subnets, internet gateways)
  - Secrets Manager (for database credentials under `/prod/*` namespace)
  - CloudFormation stack management (for `prod-*` stacks)
  - IAM role management (create/manage `prod-*` roles, pass roles to AWS services)
  - ECS task execution (for running database migrations via Fargate)
  - Lambda function management (for CloudFormation custom resources)
  - S3 access (read-only access to Prod Lambda packages)

**Parameters:**
- `ExternalId` - Security token for role assumption (required)
- `ProdAWSAccountId` - AWS account ID of Prod backend (default: 588738592923)
- `RoleName` - Name for the deployment role (default: ProdDeployRole)

**Security Features:**
- External ID required for role assumption
- 1-hour maximum session duration
- Resource-level permissions scoped to `prod-*` naming convention
- Tag-based conditions requiring `ManagedBy: Prod` tag on created resources

### s3-cloudformation-bucket.yaml

S3 bucket for hosting CloudFormation templates and Lambda function packages used by the Prod CLI during customer deployments.

**Resources Created:**
- S3 bucket with versioning enabled
- Bucket policy with two access patterns:
  - Public read access to CloudFormation deploy role template
  - Conditional access for customer deployment roles to download Lambda packages

**Parameters:**
- `BucketName` - Name of the S3 bucket (default: prod-aws-deploy)
- `TemplateFileName` - Path to the publicly accessible template (default: cloudformation-templates/cloudformation-deploy-role-template.yaml)

**Outputs:**
- Template URL for customer use
- Upload commands for both CloudFormation templates and Lambda packages
- Supabase secret configuration command

### cloudformation-github-oidc-template.yaml

GitHub Actions OIDC provider and IAM role for automated S3 uploads from CI/CD pipelines.

**Resources Created:**
- GitHub OIDC identity provider
- IAM role that GitHub Actions can assume via OIDC federation
- IAM policy granting S3 upload permissions

**Parameters:**
- `GitHubOrg` - GitHub organization or username
- `GitHubRepo` - GitHub repository name
- `S3BucketName` - Target S3 bucket for uploads
- `GitHubOIDCRoleName` - Name for the GitHub Actions role (default: GitHubActionsRole)

**Security Features:**
- OIDC-based authentication (no long-lived credentials)
- 1-hour maximum session duration
- Repository-scoped trust policy (only specified repo can assume role)

**Outputs:**
- OIDC provider ARN
- GitHub Actions role ARN
- Workflow configuration instructions

### cloudformation-iam-template.yaml

IAM user and role-based access control system for ECR operations and customer deployment role assumption.

**Resources Created:**
- `ECRUser` - IAM user that can assume ECR roles
- `ECRPullOnlyRole` - Read-only access to ECR repositories (pull images only)
- `ECRPushOnlyRole` - Write access to ECR repositories (push images only)
- `ECRRepositoryManagerRole` - Administrative access (create/delete repositories)

**Parameters:**
- `IAMUserName` - Name for the IAM user (default: ECRUser)
- `ECRPullOnlyRoleName` - Name for pull-only role (default: ECRPullOnlyRole)
- `ECRPushOnlyRoleName` - Name for push-only role (default: ECRPushOnlyRole)
- `ECRRepositoryManagerRoleName` - Name for repository manager role (default: ECRRepositoryManagerRole)

**Security Features:**
- Least-privilege access via separate roles for different operations
- Tag-based access control using tenant tags (principal tag must match resource tag)
- Cross-account role assumption support with external ID validation
- User can assume both internal ECR roles and customer deployment roles

**Outputs:**
- ARNs for the IAM user and all three ECR roles
