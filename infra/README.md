# Infrastructure

This directory contains AWS CloudFormation templates for production infrastructure components.

## Contents

### CloudFormation Templates

- **`cloudformation-iam-template.yaml`** - IAM roles and permissions for Amazon ECR (Elastic Container Registry) access

## ECR IAM Template

The CloudFormation template creates a secure, role-based access control system for ECR with three distinct permission levels:

### Resources Created

- **IAM User** (`ECRUser`) - Base user that can assume the ECR roles
- **ECR Pull Only Role** - Read-only access to ECR repositories
- **ECR Push Only Role** - Write access for pushing images to ECR repositories
- **ECR Repository Manager Role** - Administrative access for creating/deleting repositories

### Usage

Deploy the template using CloudFormation to set up ECR access controls for your AWS account. The template uses parameters for customizable resource names and outputs the ARNs of created resources for reference in other templates.

