# Lambda Functions

This directory contains AWS Lambda functions used as CloudFormation custom resources during customer deployments. Functions are deployed as S3-hosted packages rather than inline code to prevent code injection attacks.

## Functions

### database-url-constructor

Constructs a `DATABASE_URL` connection string from RDS instance details and a password stored in Secrets Manager. CloudFormation cannot directly resolve secrets in environment variables, so this Lambda custom resource constructs the URL at runtime and stores it in Secrets Manager.

**Location:** `database-url-constructor/`

**Input Parameters:**
- RDS instance endpoint
- Database name
- Username
- Password (from Secrets Manager)
- Database engine type

**Output:**
- Formatted DATABASE_URL string stored in Secrets Manager

**Security:**
- Input validation for all parameters
- URL-encodes passwords to handle special characters
- Scoped IAM permissions (no wildcard resources)
- Proper error handling and CloudFormation response signaling

See `database-url-constructor/README.md` for implementation details.

## Build Process

Each Lambda function includes a `build.sh` script that:
1. Installs production dependencies via npm
2. Creates a zip package containing code and node_modules
3. Outputs `function.zip`

Build all functions from project root:
```bash
make lambda-build
```

Build a specific function:
```bash
cd lambda/database-url-constructor
npm install --production
./build.sh
```

## Deployment

Lambda packages are deployed to S3 and referenced by CloudFormation templates during customer deployments.

**Deployment Workflow:**
```
┌─────────────────────────────────────────────────────────┐
│ 1. Build Lambda Package                                 │
│    make lambda-build                                    │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ 2. Upload to S3 via GitHub Actions                      │
│    Automated on push to main or manual trigger          │
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
│ 4. Deploy Edge Function via GitHub Actions              │
│    Automated deployment of Supabase functions           │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ 5. Customer Deployment                                  │
│    CloudFormation fetches Lambda from S3 during deploy  │
└─────────────────────────────────────────────────────────┘
```

**S3 Structure:**
```
s3://prod-aws-deploy/
└── lambda-functions/
    └── database-url-constructor/
        └── function.zip
```

**Customer Access:**
Customer deployment roles include S3 read permissions for Lambda packages (configured in `cloudformation-deploy-role-template.yaml`):
```yaml
S3LambdaPackagePolicy:
  Action:
    - s3:GetObject
    - s3:GetObjectVersion
  Resource:
    - arn:aws:s3:::prod-aws-deploy/lambda-functions/*
```

## Security Model

Lambda functions are deployed as pre-built packages to prevent code injection attacks:

1. Lambda code is written, reviewed, and tested before deployment
2. User-provided data is passed only as CloudFormation parameters
3. Lambda validates all inputs before processing
4. No user data is concatenated into code strings

This eliminates template injection vectors that would exist with inline Lambda code in CloudFormation templates.

## Version Management

Lambda packages are versioned in three places:
1. `package.json` - Semantic version
2. S3 object metadata - Version tag
3. `supabase/functions/_shared/secrets-manager-s3.ts` - Version field in LAMBDA_PACKAGES config

To update a Lambda version:
```bash
cd lambda/database-url-constructor
npm version patch  # or minor, major
make lambda-build
# Upload to S3
# Update version in secrets-manager-s3.ts
```

## Adding New Functions

1. Create directory structure:
   ```bash
   mkdir -p lambda/your-function-name
   cd lambda/your-function-name
   ```

2. Create required files:
   - `index.js` - Lambda handler
   - `package.json` - Dependencies
   - `build.sh` - Build script
   - `README.md` - Documentation

3. Add build target to Makefile:
   ```makefile
   .PHONY: lambda-build-your-function-name
   lambda-build-your-function-name:
       @cd $(LAMBDA_DIR)/your-function-name && \
           npm install --production && \
           ./build.sh
   ```

4. Register in `supabase/functions/_shared/secrets-manager-s3.ts`:
   ```typescript
   const LAMBDA_PACKAGES = {
     yourFunctionName: {
       bucket: Deno.env.get('LAMBDA_BUCKET'),
       key: 'lambda-functions/your-function-name/function.zip',
       version: '1.0.0',
     },
   };
   ```
