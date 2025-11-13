# Lambda Functions for AWS Deployments

This directory contains AWS Lambda functions used by Prod's CloudFormation deployments. These functions are deployed as **S3-hosted packages** rather than inline code to prevent code injection attacks.

## Directory Structure

```
lambda/
├── README.md                        # This file
└── database-url-constructor/        # Constructs DATABASE_URL from RDS + password
    ├── index.js                     # Lambda handler with input validation
    ├── package.json                 # Dependencies (AWS SDK)
    ├── build.sh                     # Build script
    └── README.md                    # Function-specific docs
```

## Quick Start

### Build Lambda Function

```bash
# From project root
make lambda-build

# Or manually
cd lambda/database-url-constructor
npm install --production
./build.sh
```

### Upload to S3

```bash
# Upload function.zip to your S3 bucket
aws s3 cp lambda/database-url-constructor/function.zip \
  s3://prod-aws-deploy/lambda-functions/database-url-constructor/function.zip \
  --metadata version=1.0.0
```

### Configure Supabase

```bash
# Set the Lambda bucket environment variable
supabase secrets set LAMBDA_BUCKET=prod-aws-deploy

# Deploy the Edge Function
supabase functions deploy deploy-aws-stack
```

## Security

These Lambda functions are deployed as **pre-built packages** (not inline code) to prevent code injection attacks. All user-provided data is passed as CloudFormation parameters and validated before use.

### Why Pre-built Packages?

**Problem**: Inline Lambda code in CloudFormation templates creates a code injection risk when user-provided data (service names, env vars, etc.) is concatenated into code strings.

**Solution**: Pre-built Lambda packages with strict input validation:
1. Lambda code is written once, reviewed, and tested
2. User data is passed only as CloudFormation parameters (not in code)
3. Lambda validates all inputs before processing
4. Eliminates template injection vectors

## Available Functions

### database-url-constructor

Constructs a `DATABASE_URL` connection string from RDS instance details and a password stored in Secrets Manager.

**Purpose**: CloudFormation can't directly resolve secrets in environment variables, so this Lambda custom resource constructs the URL at runtime and stores it in Secrets Manager.

**Security Features**:
- ✅ Input validation for all parameters
- ✅ URL-encodes passwords to handle special characters
- ✅ Scoped IAM permissions (not `Resource: '*'`)
- ✅ Proper error handling and CloudFormation response

See: `database-url-constructor/README.md` for details.

## Adding New Lambda Functions

To add a new Lambda function:

1. **Create directory**:
   ```bash
   mkdir -p lambda/your-function-name
   cd lambda/your-function-name
   ```

2. **Create files**:
   - `index.js` - Lambda handler
   - `package.json` - Dependencies
   - `build.sh` - Build script
   - `README.md` - Documentation

3. **Add to Makefile**:
   ```makefile
   .PHONY: lambda-build-your-function-name
   lambda-build-your-function-name:
       @echo "Building your-function-name..."
       @cd $(LAMBDA_DIR)/your-function-name && \
           npm install --production && \
           ./build.sh
   ```

4. **Update secrets-manager-s3.ts**:
   ```typescript
   const LAMBDA_PACKAGES = {
     yourFunctionName: {
       bucket: Deno.env.get('LAMBDA_BUCKET') || 'prod-aws-deploy',
       key: 'lambda-functions/your-function-name/function.zip',
       version: '1.0.0',
     },
   };
   ```

## Build Process

Each Lambda function has a `build.sh` script that:
1. Installs production dependencies
2. Creates a zip package with code + node_modules
3. Outputs `function.zip`

Example `build.sh`:
```bash
#!/bin/bash
set -e
rm -rf node_modules function.zip
npm install --production
zip -r function.zip index.js package.json node_modules/
echo "✓ Built function.zip"
```

## Deployment Workflow

```
┌─────────────────────────────────────────────────────────┐
│ 1. Build Lambda Package                                 │
│    make lambda-build                                    │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ 2. Upload to S3                                         │
│    aws s3 cp function.zip s3://bucket/lambda-...        │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ 3. Update Version in Code                               │
│    Edit secrets-manager-s3.ts version field             │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ 4. Deploy Edge Function                                 │
│    supabase functions deploy deploy-aws-stack           │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ 5. Customer Deployment                                  │
│    CloudFormation fetches Lambda from S3 during deploy  │
└─────────────────────────────────────────────────────────┘
```

## Testing

### Local Testing

```bash
cd lambda/database-url-constructor

# Install dependencies
npm install

# Run tests (if available)
npm test

# Test with sample CloudFormation event
node -e "
const handler = require('./index').handler;
const event = { /* CloudFormation event */ };
handler(event, { logStreamName: 'test' })
  .then(() => console.log('Success'))
  .catch(err => console.error('Error:', err));
"
```

### Integration Testing

Create a test CloudFormation stack to verify the Lambda function works end-to-end.

## Makefile Commands

```bash
# Build all Lambda functions
make lambda-build

# Show Lambda versions
make lambda-version

# Clean build artifacts
make lambda-clean

# Show all commands
make help
```

## S3 Bucket Structure

After upload, your S3 bucket should have:

```
s3://prod-aws-deploy/
├── cloudformation-deploy-role-template.yaml
└── lambda-functions/
    └── database-url-constructor/
        └── function.zip
```

## Customer Access

Customers need S3 permissions to download Lambda packages during CloudFormation deployment. This is configured in their IAM role:

```yaml
# In cloudformation-deploy-role-template.yaml
S3LambdaPackagePolicy:
  Action:
    - s3:GetObject
    - s3:GetObjectVersion
  Resource:
    - arn:aws:s3:::prod-aws-deploy/lambda-functions/*
```

## Version Management

Lambda packages are versioned for audit trail and rollback:

1. **package.json** - Semantic version (e.g., `1.0.0`)
2. **S3 metadata** - Version tag on uploaded object
3. **secrets-manager-s3.ts** - Version field in config

To update:
```bash
cd lambda/database-url-constructor
npm version patch  # or minor, major
make lambda-build
# Upload to S3
# Update version in secrets-manager-s3.ts
```

## Troubleshooting

### Build fails with "npm: command not found"
Install Node.js: `brew install node` (macOS)

### Upload fails with "Access Denied"
Check AWS credentials: `aws sts get-caller-identity`

### Lambda execution fails
Check CloudWatch Logs: `aws logs tail /aws/lambda/prod-SERVICENAME-db-url-constructor`

### Customer deployment fails with S3 403
Customer needs to update their IAM role with S3 permissions (see Customer Access section)

## Documentation

- **Function Details**: See `database-url-constructor/README.md`
- **Build Commands**: See `/MAKEFILE_COMMANDS.md`
- **Deployment Guide**: See `/DEPLOYMENT_GUIDE_S3_LAMBDA.md`
- **Security Details**: See `/SECURITY_IMPROVEMENTS_SUMMARY.md`

## Support

For issues or questions:
1. Check CloudWatch Logs for Lambda execution errors
2. Review input validation patterns in function code
3. Test locally with sample CloudFormation events
4. Check S3 bucket permissions and policies
