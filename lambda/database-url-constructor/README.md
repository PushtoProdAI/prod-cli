# Database URL Constructor Lambda

This Lambda function is a CloudFormation custom resource that constructs a `DATABASE_URL` connection string from RDS instance details and a password stored in Secrets Manager.

## Security

This function is deployed as a **pre-built package** (not inline code) to prevent code injection attacks. All user-provided data is passed as CloudFormation parameters and validated before use.

## Building

```bash
npm install
npm run build
```

This creates `function.zip` which can be uploaded to S3.

## Deployment

The function is automatically deployed by the `deploy-aws-stack` Supabase Edge Function:

1. Edge function uploads `function.zip` to S3 bucket
2. CloudFormation template references S3 location
3. Lambda function is created from S3 package

## Input Validation

All inputs are validated against strict patterns:
- `DBInstanceId`: Must be valid RDS identifier format
- `SecretName`: Must follow `/prod/{service}/{VAR_NAME}` pattern
- `ServiceName`: Alphanumeric and hyphens only
- `EnvVarName`: Uppercase letters and underscores only

## Parameters

### CloudFormation Custom Resource Properties

- `DBInstanceId`: RDS instance identifier (e.g., `prod-myapp-postgres`)
- `PasswordSecretArn`: Secret name containing password (e.g., `/prod/myapp/POSTGRES_PASSWORD`)
- `SecretName`: Output secret name for DATABASE_URL (e.g., `/prod/myapp/DATABASE_URL`)
- `ServiceName`: Service name for tagging (e.g., `myapp`)
- `EnvVarName`: Environment variable name for metadata (e.g., `DATABASE_URL`)

## Output

Creates a secret in Secrets Manager containing the full PostgreSQL connection URL:
```
postgresql://postgres:{password}@{endpoint}:{port}/postgres
```

Password is URL-encoded to handle special characters.

## IAM Permissions Required

The Lambda execution role needs:
- `secretsmanager:CreateSecret`
- `secretsmanager:UpdateSecret`
- `secretsmanager:DeleteSecret`
- `secretsmanager:GetSecretValue`
- `secretsmanager:TagResource`
- `rds:DescribeDBInstances`
- `logs:CreateLogGroup`
- `logs:CreateLogStream`
- `logs:PutLogEvents`
