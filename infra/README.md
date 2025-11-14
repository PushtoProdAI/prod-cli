# Infrastructure

This directory contains AWS CloudFormation templates for production infrastructure components.

## Contents

### CloudFormation Templates

- **`cloudformation-iam-template.yaml`** - IAM roles and permissions for Amazon ECR (Elastic Container Registry) access
- **`s3-cloudformation-bucket.yaml`** - S3 bucket for hosting CloudFormation templates used by Prod CLI
- **`cloudformation-deploy-role-template.yaml`** - IAM role template for customer AWS accounts (hosted in S3)

## S3 CloudFormation Bucket

Creates an S3 bucket to host CloudFormation templates that are used by the Prod CLI during AWS authentication setup.

### Usage

```bash
# Deploy to staging
aws cloudformation create-stack \
  --stack-name prod-cloudformation-bucket \
  --template-body file://s3-cloudformation-bucket.yaml \
  --parameters \
    ParameterKey=BucketName,ParameterValue=prod-aws-deploy

# Wait for stack creation
aws cloudformation wait stack-create-complete \
  --stack-name prod-cloudformation-bucket

# Upload the deployment role template
aws s3 cp cloudformation-deploy-role-template.yaml s3://prod-aws-deploy/cloudformation-deploy-role-template.yaml

# Verify public access
curl https://prod-aws-deploy.s3.amazonaws.com/cloudformation-deploy-role-template.yaml
```

### What it Creates

- S3 bucket with versioning enabled
- Bucket policy allowing public read access to `cloudformation-deploy-role-template.yaml`
- Bucket owner enforced object ownership
- Proper tags for management

## IAM Deployment Role Template

The `cloudformation-deploy-role-template.yaml` template is used by customers during Prod CLI AWS authentication setup. It creates an IAM role in their AWS account that allows Prod to deploy App Runner and RDS resources.

**This template is hosted in S3 and referenced by the CLI automatically.**

### Permissions Included

- App Runner service management
- RDS database deployment
- ECR repository and image management
- VPC networking setup
- Secrets Manager for database credentials

## ECR IAM Template

The CloudFormation template creates a secure, role-based access control system for ECR with three distinct permission levels:

### Resources Created

- **IAM User** (`ECRUser`) - Base user that can assume the ECR roles
- **ECR Pull Only Role** - Read-only access to ECR repositories
- **ECR Push Only Role** - Write access for pushing images to ECR repositories
- **ECR Repository Manager Role** - Administrative access for creating/deleting repositories

### Usage

Deploy the template using CloudFormation to set up ECR access controls for your AWS account. The template uses parameters for customizable resource names and outputs the ARNs of created resources for reference in other templates.
