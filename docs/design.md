# Prod CLI Design Document

> **⚠️ Legacy — describes the retired SaaS-backend architecture.** prod is now a single
> self-contained binary: local state, direct LLM, no backend, no account. This document is
> kept for the AWS CloudFormation/template logic and the historical DB schema (useful for
> the AWS port), but where it describes a backend/auth/LLM-proxy, that is gone. For the
> current architecture read [CLAUDE.md](../CLAUDE.md); for the framework read
> [cloud-framework-plan.md](./cloud-framework-plan.md).

## Overview

Prod is a CLI tool that deploys applications to cloud platforms (AWS, Render, Fly.io, etc.). The system consists of three main parts:

1. **CLI** - Go-based command-line tool with TUI
2. **Supabase Edge Functions** - Backend API layer  
3. **Database** - PostgreSQL via Supabase

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                           CLI (Go)                               │
│  Commands → Agent (State Machine) → Workflows → Activities       │
│                         ↓                                        │
│              Platform Adapters (AWS, Render, Fly.io, etc.)       │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Supabase Edge Functions                       │
│  cli-auth │ aws-auth │ deploy-aws-stack │ tokens │ llm-proxy    │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Supabase Database                            │
│  token_balances │ deployment_operations │ aws_credentials        │
└─────────────────────────────────────────────────────────────────┘
```

---

## CLI

### Entry Point

`cmd/main.go` initializes all dependencies and runs the root command. Configuration is injected via constructor functions.

### Command Structure

| Command | Location | Purpose |
|---------|----------|---------|
| root | `cmd/root/root.go` | Main entry, manages TUI or console mode |
| run | `cmd/run/run.go` | Non-interactive mode for automation |
| auth | `cmd/auth/` | Login, logout, status, token subcommands |

### Key Components

#### Agent (`internal/agent/`)

The agent orchestrates deployment operations using a finite state machine.

**States:**
```
checkPrerequisites → plan → confirm → detectExisting → categorizeEnvVars → prepareProject → deploy
```

```
                                    ┌─────────────────────┐
                                    │  checkPrerequisites │
                                    │  ─────────────────  │
                                    │  • Auth check       │
                                    │  • Platform auth    │
                                    └──────────┬──────────┘
                                               │
                                               ▼
                                    ┌─────────────────────┐
                                    │        plan         │
                                    │  ─────────────────  │
                                    │  • Analyze project  │
                                    │  • Detect language  │
                                    │  • Find services    │
                                    │  • Estimate cost    │
                                    └──────────┬──────────┘
                                               │
                                               ▼
                    ┌──────────────────────────────────────────────────┐
                    │                    confirm                        │
                    │  ────────────────────────────────────────────────│
                    │  • Show deployment plan to user                   │
                    │  • Wait for approval                              │
                    └──────────┬───────────────────────┬───────────────┘
                               │ approved              │ rejected
                               ▼                       ▼
                    ┌─────────────────────┐         (exit)
                    │   detectExisting    │
                    │  ─────────────────  │
                    │  • Check for prior  │
                    │    deployments      │
                    │  • Preserve DBs     │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  categorizeEnvVars  │
                    │  ─────────────────  │
                    │  • Prompt for vals  │
                    │  • Mark sensitive   │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │   prepareProject    │
                    │  ─────────────────  │
                    │  • Generate configs │
                    │  • Build artifacts  │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │       deploy        │
                    │  ─────────────────  │
                    │  • Execute workflow │
                    │  • Platform-specific│
                    │    deployment       │
                    │  • Health check     │
                    └──────────┬──────────┘
                               │
                               ▼
                           (complete)
```

**Key files:**
- `agent.go` - State machine and main orchestration
- `workflow.go` - Durable workflow definitions
- `activities.go` - Atomic units of work
- `planning.go` - Deployment planning logic

#### Deployment (`internal/deployment/`)

Platform-specific deployment logic uses the adapter pattern.

**Core interfaces:**
```go
type Deployable interface {
    Deploy(ctx context.Context) ([]CreatedResource, error)
    GetPreviousDeployment(ctx context.Context) (*DeploymentInfo, error)
    Rollback(ctx context.Context, targetDeploymentID string) error
}

type DeploymentAdapter interface {
    SupportedStrategies() []DeploymentStrategy
    GenerateArtifacts(spec *DeploymentSpec, strategy DeploymentStrategy) (Deployable, error)
    EstimateCost(spec *DeploymentSpec, strategy DeploymentStrategy) (CostEstimate, error)
}
```

Each platform has its own subdirectory with adapter implementation: `aws/`, `render/`, `flyio/`, `heroku/`, `netlify/`, `vercel/`.

#### Analyzer (`internal/analyzer/`)

Detects project type, dependencies, and configuration.

```go
type Analyzer interface {
    CanHandle() (bool, error)
    Analyze() (*ProjectSpec, error)
}
```

Implementations: `node.go`, `python.go`

#### TUI (`internal/tui/`)

Uses Bubble Tea framework

- `model.go` - Application state
- `teawriter.go` - Bridge between agent and TUI via StatusWriter interface

#### Output (`internal/output/`)

Abstracts output handling for different modes.

```go
type StatusWriter interface {
    io.Writer
    SendStatus(status, message string)
    SendStatusComplete(status, message string)
    SendDeploymentStart(platform, projectPath string)
    SendDeploymentComplete(platform, status, url, errorMsg string, durationMs int64)
    SendPlanApprovalRequest(plan map[string]interface{})
    SendEnvVarPrompt(varName, defaultValue, message string)
}
```

Implementations:
- `ConsoleWriter` - Plain text
- `JSONWriter` - JSON Lines for VSCode extension
- `TeaWriter` - TUI integration
- `ProxyWriter` - Dynamic writer switching

#### Auth (`internal/auth/`)

Platform-specific authentication.

```go
type AuthProvider interface {
    CheckAuthentication(ctx context.Context) (bool, error)
    ValidateAPIKey(ctx context.Context, token string) (bool, error)
    PerformOAuthLogin(ctx context.Context) error
    APIKeyPrompt() string
}
```

#### LLM (`internal/llm/`)

Handles LLM interactions using BAML for type-safe prompts and responses. All calls proxy through the `llm-proxy` Supabase Edge Function for usage tracking.

```go
type Client interface {
    ExtractIntent(ctx context.Context, prompt string) (types.Intent, error)
    SummarizeIntent(...) (types.Summary, error)
    DetermineLaunchCommand(...) (types.LaunchCommand, error)
}
```

**BAML Functions** (defined in `cli/baml_src/`):

| Function | Purpose |
|----------|---------|
| `ExtractIntent` | Parse user input to extract action (deploy/rollback/status), platform, and source path |
| `SummarizeIntent` | Generate friendly summary of what will happen for user confirmation |
| `SummarizeSteps` | Convert deployment steps list into readable paragraph |
| `SummarizeDeployError` | Explain errors in plain language with OS-specific remediation steps |
| `DetermineEnvVarRoles` | Classify env vars as database-related, Redis-related, or general; mark sensitive vars |
| `CategorizeRoutes` | Analyze detected routes to find best health check endpoint |
| `DetermineBuildOutput` | Determine correct build output directory from config files |
| `DetermineLaunchCommand` | Infer production start command from package.json, Procfile, or launcher scripts |
| `DetermineMigrationCommand` | Detect database migration command based on ORM/migration tool |
| `FetchPricing` | Extract pricing info from provider pricing page markdown |

### Patterns to Follow

1. **Dependency injection** - Pass dependencies via constructors, not globals
2. **Interface segregation** - Small, focused interfaces
3. **State machine for UX** - Multi-step flows use FSM pattern
4. **Workflows for durability** - Use go-workflows for operations that need retry/recovery
5. **Adapter pattern** - New platforms implement DeploymentAdapter interface
6. **Error wrapping** - Use `github.com/go-errors/errors` for stack traces

---

## Supabase Edge Functions

### Function Categories

#### Authentication
| Function | Purpose |
|----------|---------|
| `cli-auth` | OAuth flow, email/password auth, token generation |
| `aws-auth` | AWS IAM role linking via CloudFormation |
| `update-password` | Password reset flow |

#### AWS Deployment
| Function | Purpose |
|----------|---------|
| `create-repo` | Create ECR repository |
| `push-token` | Get ECR push credentials |
| `pull-token` | Get ECR pull credentials |
| `deploy-aws-stack` | Create/update CloudFormation stacks |
| `preview-aws-template` | Generate template for cost estimation |
| `get-aws-stack-status` | Poll stack status |
| `run-ecs-migration` | Execute database migrations via ECS |

#### Resources & Billing
| Function | Purpose |
|----------|---------|
| `tokens` | Token balance, consumption, packages |
| `base-images` | Available Docker base images |
| `record-stack` | Usage analytics |
| `deployment-logger` | Operation audit log |

#### LLM
| Function | Purpose |
|----------|---------|
| `llm-proxy` | Proxy OpenAI calls with usage tracking |
| `llm-usage` | Admin analytics |

### Shared Utilities (`_shared/`)

**`aws.ts`** - STS role assumption, ECR operations
**`sentry.ts`** - Error monitoring

### Patterns to Follow

#### Authentication
```typescript
const authHeader = req.headers.get('Authorization')!;
const token = authHeader.replace('Bearer ', '');
const { data } = await supabaseClient.auth.getUser(token);

if (!data.user) {
  return new Response("Unauthorized", { status: 401 });
}
```

#### Supabase Client
```typescript
// User-scoped client (most common)
const supabase = createClient(
  Deno.env.get("SUPABASE_URL")!,
  Deno.env.get("SUPABASE_ANON_KEY")!,
  {
    global: {
      headers: { Authorization: req.headers.get('Authorization')! },
    },
  }
);

// Service role client (admin operations)
const supabase = createClient(
  Deno.env.get("SUPABASE_URL")!,
  Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!
);
```

#### Database Operations
Prefer RPC calls to stored procedures over direct table access. Stored procedures run atomically in the database, enabling row-level locking and multi-step transactions that would otherwise require multiple round trips. They also centralize business logic in one place rather than spreading it across Edge Functions.

```typescript
const { data } = await supabase.rpc('consume_tokens', {
  p_user_id: user.id,
  p_tokens_to_consume: amount,
  p_operation: operation,
  p_metadata: JSON.stringify(metadata)
});
```

#### Error Handling
```typescript
Deno.serve(async (req) => {
  try {
    // ... function logic ...
  } catch (error) {
    console.error('Unexpected error:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'function-name',
      operation: 'general_error',
    });
    await flushSentry();
    
    return new Response(
      JSON.stringify({ error: 'Internal server error' }),
      { status: 500, headers: { 'Content-Type': 'application/json' } }
    );
  }
});
```

#### CORS
```typescript
const corsHeaders = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
  'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
  'Access-Control-Max-Age': '86400',
}

if (req.method === 'OPTIONS') {
  return new Response('ok', { headers: corsHeaders });
}
```

#### AWS Role Assumption
```typescript
const assumeRoleParams: any = {
  RoleArn: awsCredentials.role_arn,
  RoleSessionName: `session-${tenantId}`,
  DurationSeconds: 3600,
};

if (awsCredentials.external_id) {
  assumeRoleParams.ExternalId = awsCredentials.external_id;
}

const { Credentials } = await stsClient.send(new AssumeRoleCommand(assumeRoleParams));
```

---

## Database

### Schemas

| Schema | Purpose |
|--------|---------|
| `public` | Application tables and functions |
| `audit` | Deployment operations logging |
| `internal` | Admin-only tables |

### Core Tables

#### Authentication & Security
| Table | Purpose |
|-------|---------|
| `password_history` | Prevents password reuse |
| `account_lockout` | Tracks failed login attempts |
| `password_policies` | Password policy settings |
| `internal.admin_users` | Admin user designations |

#### Tokens & Billing
| Table | Purpose |
|-------|---------|
| `token_balances` | Current token balance per user |
| `token_transactions` | Immutable log of all token operations |
| `token_packages` | Available token packages |
| `token_purchases` | Purchase records |

#### Deployment
| Table | Purpose |
|-------|---------|
| `audit.deployment_operations` | Audit log of deployments |
| `aws_credentials` | User AWS IAM role configuration |

#### Configuration
| Table | Purpose |
|-------|---------|
| `base_images` | Docker base images by language |
| `llm_usage_logs` | LLM API usage tracking |

### Key Functions

#### Token Operations
```sql
consume_tokens(p_user_id, p_tokens_to_consume, p_operation, p_metadata)  -- Atomic consumption with row lock
refund_tokens(p_user_id, p_operation_id)                                  -- Refund failed operations
get_token_summary(p_user_id)                                              -- Balance for CLI display
```

#### Deployment
```sql
log_deployment_operation(...)        -- Log start of deployment
update_deployment_operation(...)     -- Update status/completion
query_deployment_operations(...)     -- Flexible filtering
get_deployment_history(...)          -- History for rollback
```

### Security Patterns

1. **Row Level Security (RLS)** - All tables have RLS enabled
2. **User-scoped access** - Users can only access their own records via `auth.uid()`
3. **Service role for sensitive ops** - Admin tables restricted to service role
4. **SECURITY DEFINER** - Functions use `SET search_path = ''` to prevent injection
5. **Row-level locking** - `consume_tokens` uses `SELECT ... FOR UPDATE`

### Patterns to Follow

1. **Event sourcing for tokens** - `token_transactions` is immutable; balances are computed
2. **RPC over direct access** - Business logic in PostgreSQL functions
3. **Schema separation** - `public` for app, `audit` for logs, `internal` for admin
4. **Explicit grants** - REVOKE ALL from PUBLIC, grant selectively

---

## Rollbacks

Rollbacks revert an application to a previous deployment state. The implementation varies by platform.

### Platform-Native Rollbacks

For platforms with built-in rollback functionality, we delegate to their APIs:

| Platform | Rollback Mechanism |
|----------|-------------------|
| Render | Render API - reverts to previous deploy |
| Fly.io | `flyctl` CLI - redeploys previous release |
| Heroku | Heroku API - rolls back to previous release |
| Vercel | Vercel API - promotes previous deployment |
| Netlify | Netlify API - publishes previous deploy |

These platforms maintain their own deployment history and handle rollback atomically.

### AWS Rollbacks

AWS has no built-in rollback mechanism since we manage infrastructure via CloudFormation. We implement rollbacks by tracking deployment history in Supabase and redeploying previous Docker images.

See [AWS Deployment > Rollbacks](#rollbacks-1) for details.

---

## AWS Deployment (Deep Dive)

AWS deployment is built from scratch using CloudFormation, ECR, App Runner, and ECS. This section documents the complete flow.

### Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                                CLI (Go)                                      │
│  workflow_aws.go → deployment/aws/ → backend/aws/client.go                  │
│       │                                                                      │
│       ├── 1. Build Docker image locally (Docker SDK)                        │
│       ├── 2. Push to customer's ECR                                         │
│       └── 3. Orchestrate deployment via Supabase functions                  │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼ HTTPS
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Supabase Edge Functions                              │
│  create-repo │ push-token │ deploy-aws-stack │ get-aws-stack-status │       │
│              │            │ run-ecs-migration│                       │       │
│                                    │                                         │
│                    Template Generator (deploy-aws-stack/)                    │
│                    ├── template-generator.ts                                 │
│                    ├── networking.ts                                         │
│                    ├── backing-services.ts                                   │
│                    ├── compute.ts                                            │
│                    └── iam-roles.ts                                          │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼ STS AssumeRole
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Customer's AWS Account                               │
│                                                                              │
│  ┌─────────┐    ┌─────────────────────────────────────────────────────┐     │
│  │   ECR   │    │              CloudFormation Stack                    │     │
│  │  Repo   │    │  ┌─────────────────────────────────────────────┐    │     │
│  └─────────┘    │  │                    VPC                       │    │     │
│       │         │  │  ┌──────────┐  ┌──────────┐                 │    │     │
│       │         │  │  │ Private  │  │ Private  │ (RDS, Cache)    │    │     │
│       │         │  │  │ Subnet 1 │  │ Subnet 2 │                 │    │     │
│       │         │  │  └──────────┘  └──────────┘                 │    │     │
│       │         │  │  ┌──────────┐  ┌──────────┐                 │    │     │
│       │         │  │  │ Public   │  │ Public   │ (ECS tasks)     │    │     │
│       │         │  │  │ Subnet 1 │  │ Subnet 2 │                 │    │     │
│       │         │  │  └──────────┘  └──────────┘                 │    │     │
│       │         │  └─────────────────────────────────────────────┘    │     │
│       │         │                                                      │     │
│       │         │  ┌───────────┐  ┌─────────────┐  ┌──────────────┐   │     │
│       │         │  │    RDS    │  │ ElastiCache │  │   Secrets    │   │     │
│       │         │  │ Postgres  │  │   Valkey    │  │   Manager    │   │     │
│       │         │  └───────────┘  └─────────────┘  └──────────────┘   │     │
│       │         │                                                      │     │
│       │         │  ┌───────────────────────────────────────────────┐  │     │
│       │         │  │                 App Runner                     │  │     │
│       ▼         │  │  ┌─────────────┐    ┌──────────────────────┐  │  │     │
│   ┌───────┐     │  │  │     VPC     │    │       Service        │  │  │     │
│   │ Image │────────│──│  Connector  │───▶│   (web application)  │  │  │     │
│   └───────┘     │  │  └─────────────┘    └──────────────────────┘  │  │     │
│                 │  └───────────────────────────────────────────────┘  │     │
│                 │                                                      │     │
│                 │  ┌───────────────────────────────────────────────┐  │     │
│                 │  │                    ECS                         │  │     │
│                 │  │  ┌─────────┐    ┌──────────────────────────┐  │  │     │
│                 │  │  │ Cluster │    │  Migration Task (Fargate) │  │  │     │
│                 │  │  └─────────┘    └──────────────────────────┘  │  │     │
│                 │  └───────────────────────────────────────────────┘  │     │
│                 └─────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Deployment Sequence

```
┌─────────┐          ┌──────────┐          ┌─────────────────┐          ┌─────┐
│   CLI   │          │ Supabase │          │ Customer's AWS  │          │User │
└────┬────┘          └────┬─────┘          └───────┬─────────┘          └──┬──┘
     │                    │                        │                       │
     │ 1. create-repo     │                        │                       │
     │───────────────────▶│                        │                       │
     │                    │ STS AssumeRole         │                       │
     │                    │───────────────────────▶│                       │
     │                    │ CreateRepository       │                       │
     │                    │───────────────────────▶│                       │
     │◀───────────────────│ repositoryUri          │                       │
     │                    │                        │                       │
     │ 2. Build Docker image locally               │                       │
     │────────────────────────────────────────────▶│                       │
     │                    │                        │                       │
     │ 3. push-token      │                        │                       │
     │───────────────────▶│                        │                       │
     │                    │ GetAuthorizationToken  │                       │
     │                    │───────────────────────▶│                       │
     │◀───────────────────│ Docker credentials     │                       │
     │                    │                        │                       │
     │ 4. Push image to ECR                        │                       │
     │─────────────────────────────────────────────────────────────────────▶
     │                    │                        │                       │
     │ 5. deploy-aws-stack│                        │                       │
     │───────────────────▶│                        │                       │
     │                    │ Generate CFN template  │                       │
     │                    │ CreateStack            │                       │
     │                    │───────────────────────▶│                       │
     │◀───────────────────│ CREATE_IN_PROGRESS     │                       │
     │                    │                        │                       │
     │ 6. Poll: get-aws-stack-status (loop)        │                       │
     │───────────────────▶│ DescribeStacks         │                       │
     │                    │───────────────────────▶│                       │
     │◀───────────────────│ status + outputs       │                       │
     │         ...        │         ...            │                       │
     │◀───────────────────│ CREATE_COMPLETE        │                       │
     │                    │                        │                       │
     │ 7. run-ecs-migration (if migration cmd)     │                       │
     │───────────────────▶│                        │                       │
     │                    │ RunTask (Fargate)      │                       │
     │                    │───────────────────────▶│                       │
     │                    │ Poll task status       │                       │
     │                    │───────────────────────▶│                       │
     │◀───────────────────│ Migration logs         │                       │
     │                    │                        │                       │
     │ 8. Update stack (add App Runner if deferred)│                       │
     │───────────────────▶│ UpdateStack            │                       │
     │                    │───────────────────────▶│                       │
     │         ...        │         ...            │                       │
     │◀───────────────────│ UPDATE_COMPLETE        │                       │
     │                    │                        │                       │
     │ 9. Health check    │                        │                       │
     │─────────────────────────────────────────────────────────────────────▶
     │                    │                        │                  200 OK
     │◀────────────────────────────────────────────────────────────────────│
     │                    │                        │                       │
     ▼                    ▼                        ▼                       ▼
```

### Two-Phase Deployment (with migrations)

When a project has database migrations, deployment happens in two phases:

**Phase 1: Infrastructure**
- VPC, subnets, security groups
- RDS instance (takes 10-15 min)
- ElastiCache (if needed)
- ECS cluster and task definition
- Secrets Manager secrets
- **App Runner is NOT created yet**

**Phase 2: After migrations**
- Run migration task via ECS Fargate
- Wait for migration to complete
- Update CloudFormation stack to add App Runner
- App Runner pulls image and starts

This ensures the database is migrated before the application starts.

### Rollbacks

Rollbacks redeploy a previous Docker image without modifying infrastructure. Database migrations are not run during rollback—the assumption is the schema is compatible with the old code.

**What gets rolled back:**
- Docker image (reverts to previous ECR image URL)

**What stays the same:**
- Infrastructure (VPC, RDS, ElastiCache, etc.)
- Environment variables (uses current values)
- Database state (no migrations run)

**How it works:**

1. User requests rollback via CLI
2. CLI queries deployment history from `audit.deployment_operations`
3. Previous deployment's `image_url` is extracted from metadata
4. CloudFormation stack is updated with the old image URL
5. App Runner pulls the old image and deploys new instances

```
┌──────────────┐      ┌─────────────────┐      ┌─────────────────┐
│  Deployment  │      │   Supabase DB   │      │       AWS       │
│   History    │◀────▶│  deployment_    │      │                 │
│              │      │  operations     │      │                 │
└──────────────┘      └─────────────────┘      └─────────────────┘
       │                                              │
       │ 1. Get previous image_url                    │
       ▼                                              │
┌──────────────┐                                      │
│     CLI      │                                      │
│  Rollback    │──────────────────────────────────────▶
│   Workflow   │  2. Update CFN stack with old image  │
└──────────────┘                                      │
                                                      ▼
                                              ┌───────────────┐
                                              │  App Runner   │
                                              │ pulls old img │
                                              └───────────────┘
```

Each successful deployment stores `image_url` in the operation metadata, enabling rollback to any previous version.

### AWS Resources Created

| Resource | Service | Purpose |
|----------|---------|---------|
| Repository | ECR | Container image storage |
| VPC | EC2 | Network isolation (10.0.0.0/16) |
| Private Subnets (2) | EC2 | RDS, ElastiCache (no internet) |
| Public Subnets (2) | EC2 | ECS migration tasks (internet via IGW) |
| Internet Gateway | EC2 | Outbound internet for public subnets |
| Security Groups | EC2 | Network access control |
| DB Subnet Group | RDS | Multi-AZ database placement |
| DB Instance | RDS | PostgreSQL (db.t3.micro, 20GB gp3) |
| Serverless Cache | ElastiCache | Valkey/Redis |
| Secrets | Secrets Manager | DB passwords, sensitive env vars |
| Cluster | ECS | Migration task orchestration |
| Task Definition | ECS | Migration container config |
| Access Role | IAM | App Runner ECR pull |
| Instance Role | IAM | App Runner runtime (Secrets access) |
| Task Execution Role | IAM | ECS task setup |
| Task Role | IAM | ECS task runtime |
| VPC Connector | App Runner | Connect to VPC for backing services |
| Service | App Runner | The deployed web application |

### CloudFormation Template Generation

Templates are generated in `supabase/functions/deploy-aws-stack/`:

| File | Responsibility |
|------|----------------|
| `template-generator.ts` | Main orchestrator, input validation |
| `networking.ts` | VPC, subnets, security groups |
| `backing-services.ts` | RDS, ElastiCache |
| `compute.ts` | App Runner, ECS |
| `iam-roles.ts` | IAM roles and policies |
| `secrets-manager-s3.ts` | Secrets, Lambda custom resources |
| `env-builders.ts` | Environment variable configuration |

Input validation in `template-generator.ts` prevents injection attacks:
- Service name: lowercase alphanumeric with hyphens only
- Image URL: must match ECR pattern
- CPU/Memory: validated against allowed values
- Env vars: uppercase letters, numbers, underscores only
- Migration commands: shell metacharacters blocked

### Cross-Account Access

Deployments run in the customer's AWS account via STS AssumeRole:

1. Customer runs CloudFormation template from `infra/cloudformation-iam-template.yaml`
2. Template creates IAM role with trust policy for Prod's AWS account
3. Customer provides role ARN to CLI via `aws-auth` flow
4. Role ARN and ExternalId stored in `aws_credentials` table
5. Edge functions assume role before any AWS operations

```typescript
const assumeRoleParams = {
  RoleArn: awsCredentials.role_arn,
  RoleSessionName: `session-${tenantId}`,
  DurationSeconds: 3600,
  ExternalId: awsCredentials.external_id,  // Prevents confused deputy
};
```

### Database URL Constructor Lambda

A CloudFormation Custom Resource Lambda that constructs connection strings at deploy time. This solves the chicken-and-egg problem: App Runner needs `DATABASE_URL`, but the RDS endpoint isn't known until after creation.

**Why a Lambda instead of CloudFormation intrinsic functions?**

CloudFormation's `!Sub` and `!Join` can build strings, but they can't URL-encode values. Database passwords often contain special characters (`@`, `/`, `#`, etc.) that break connection URLs if not encoded. CloudFormation has no built-in URL encoding function, so a Lambda custom resource handles the encoding safely.

**Location:** `lambda/database-url-constructor/`

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        CloudFormation Stack                                  │
│                                                                              │
│  ┌─────────────┐     ┌─────────────────┐     ┌─────────────────────────┐    │
│  │     RDS     │     │ Secrets Manager │     │   Custom Resource       │    │
│  │  Instance   │     │ (DB Password)   │     │  (DatabaseUrlConstructor)│   │
│  └──────┬──────┘     └────────┬────────┘     └────────────┬────────────┘    │
│         │                     │                           │                  │
│         │ endpoint:port       │ password                  │                  │
│         └─────────────────────┼───────────────────────────┘                  │
│                               │                                              │
│                               ▼                                              │
│                    ┌─────────────────────┐                                   │
│                    │       Lambda        │                                   │
│                    │  ─────────────────  │                                   │
│                    │  1. Get RDS endpoint│                                   │
│                    │  2. Get password    │                                   │
│                    │  3. URL-encode pwd  │                                   │
│                    │  4. Build URL       │                                   │
│                    │  5. Store in secret │                                   │
│                    └──────────┬──────────┘                                   │
│                               │                                              │
│                               ▼                                              │
│                    ┌─────────────────────┐                                   │
│                    │ Secrets Manager     │                                   │
│                    │ /prod/myapp/        │                                   │
│                    │   DATABASE_URL      │                                   │
│                    └──────────┬──────────┘                                   │
│                               │                                              │
│                               ▼                                              │
│                    ┌─────────────────────┐                                   │
│                    │    App Runner       │                                   │
│                    │  (references secret)│                                   │
│                    └─────────────────────┘                                   │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Supported resource types:**

| Type | Input | Output |
|------|-------|--------|
| PostgreSQL | RDS instance ID + password secret | `postgresql://postgres:{pwd}@{host}:{port}/postgres` |
| Redis | ElastiCache identifier | `rediss://{host}:{port}` |

**How it works:**

1. CloudFormation creates RDS/ElastiCache and a password secret
2. CloudFormation invokes Lambda custom resource with resource IDs
3. Lambda queries AWS APIs to get endpoints
4. Lambda retrieves password from Secrets Manager (PostgreSQL only)
5. Lambda constructs URL with URL-encoded password
6. Lambda stores URL in a new Secrets Manager secret
7. App Runner/ECS references the secret via `valueFrom` ARN

**Deployment:**

The Lambda is pre-built and uploaded to S3 via CI/CD (`.github/workflows/deploy-lambda.yaml`). CloudFormation references the S3 package—no inline code. This prevents code injection since user data only flows through CloudFormation parameters.

**Input validation:**
- `DBInstanceId`/`CacheIdentifier`: `/^[a-zA-Z][a-zA-Z0-9-]{0,62}$/`
- `SecretName`: `/^\/prod\/[a-zA-Z0-9-]+\/[A-Z_]+$/`
- `ServiceName`: `/^[a-zA-Z0-9-]+$/`
- `EnvVarName`: `/^[A-Z_]+$/`

**Key files:**
- `lambda/database-url-constructor/index.js` - Lambda handler
- `supabase/functions/deploy-aws-stack/secrets-manager-s3.ts` - CloudFormation resource generation
