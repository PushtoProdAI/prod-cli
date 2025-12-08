# prod
Prod enables developers—and AI-assisted builders—to deploy applications using natural language

## Local Development with Supabase

This project includes a local Supabase instance for development. Follow these steps to get started:

### Prerequisites

- Docker (required for Supabase local development)
- Homebrew (for installing Supabase CLI on macOS)

### Quick Setup

1. **Install and start Supabase:**
   ```bash
   make supabase-setup
   ```

   This command will:
   - Install the Supabase CLI if not already installed
   - Initialize the Supabase project
   - Start the local Supabase instance

2. **Access Supabase Studio:**
   ```bash
   make supabase-studio
   ```
   
   Or visit: http://localhost:54323

### Environment Configuration

1. Copy the example environment file:
   ```bash
   cp env.example .env
   ```

2. Configure your environment:
   ```bash
   # For local development with staging authentication (default)
   ENVIRONMENT=staging
   
   # For local Supabase development
   ENVIRONMENT=local
   
   # For production
   ENVIRONMENT=production
   ```

3. The default configuration uses staging for local development, which provides a stable authentication environment while you develop locally.

## Connecting to Remote Supabase Instance

To connect to a remote Supabase instance instead of the local one:

### 1. Get Remote Credentials
1. The credentials are in 1Password in a note called "Remote Supabase Credentials for Prod" 

### 2. Update Environment Variables
Edit your `.env` file and replace the local values:
```bash
# Comment out local development section
# SUPABASE_URL=http://localhost:54321
# SUPABASE_ANON_KEY=...

# Add remote Supabase credentials
SUPABASE_URL=https://your-project-ref.supabase.co
SUPABASE_ANON_KEY=your-anon-key-from-supabase-dashboard
SUPABASE_SERVICE_ROLE_KEY=your-service-role-key-from-supabase-dashboard
DATABASE_URL=postgresql://postgres:[YOUR-PASSWORD]@db.your-project-ref.supabase.co:5432/postgres
```

### 3. Link with Supabase CLI (Optional)
```bash
# Login to Supabase
supabase login

# Link to your remote project
supabase link --project-ref your-project-ref

# Pull the remote schema (optional)
supabase db pull
```

### Available Make Commands

#### Supabase Commands
- `make supabase-start` - Start Supabase local development
- `make supabase-stop` - Stop Supabase local development
- `make supabase-status` - Check Supabase status
- `make supabase-reset` - Reset the database (applies migrations and seeds)
- `make supabase-migration-new` - Create a new migration
- `make supabase-migration-up` - Apply migrations
- `make supabase-seed` - Seed the database
- `make supabase-studio` - Open Supabase Studio in browser

#### CLI Development Commands (from `cli/` directory)
- `make dev` - Run CLI in development mode (local environment, uses `../.env.local`)
- `make dev-staging` - Run CLI in development mode (staging environment, uses `../.env`)
- `make dev-production` - Run CLI in development mode (production environment, uses `../.env`)
- `make build` - Build CLI binary for current platform
- `make build-cli-linux` - Build CLI binary for Linux
- `make build-cli-darwin` - Build CLI binary for macOS (Intel)
- `make build-cli-darwin-arm64` - Build CLI binary for macOS (Apple Silicon)
- `make clean` - Clean build artifacts
- `make generate` - Generate BAML code (run after updating prompts)

### Local Supabase URLs

- **API URL:** http://localhost:54321
- **Database URL:** postgresql://postgres:postgres@localhost:54322/postgres
- **Studio URL:** http://localhost:54323
- **Inbucket (Email Testing):** http://localhost:54324

### Database Management

- **Migrations:** Add new migrations in `supabase/migrations/`
- **Seed Data:** Add seed data in `supabase/seed.sql`
- **Schema Changes:** Modify `supabase/config.toml` for configuration changes

### Troubleshooting

If you encounter issues:

1. **Reset everything:**
   ```bash
   make supabase-stop
   make supabase-reset
   make supabase-start
   ```

2. **Check Docker status:**
   ```bash
   docker ps
   ```

3. **View logs:**
   ```bash
   supabase status
   ```
## Building and Running the Prod CLI

### Prerequisites

1. **Install Ollama** - Currently tested with Ollama 3.1 model
2. **Set up environment variables** - The CLI requires Supabase configuration at build time

### Environment Setup

1. **Create environment file:**
   ```bash
   cp env.example .env
   ```

2. **Configure Supabase credentials** in your `.env` file:
   - For local development: Use the default values in `env.example`
   - For remote Supabase: Update with your remote credentials (see "Connecting to Remote Supabase Instance" section above)

### Building and Running the CLI

The CLI uses build-time configuration via environment variables loaded from `.env` files. The Makefile automatically loads the appropriate environment file based on the command you use.

#### For Local Development and Testing

The CLI provides three development modes that automatically load different environment files:

```bash
cd cli

# Local development (uses ../.env.local)
make dev

# Staging development (uses ../.env)  
make dev-staging

# Production development (uses ../.env)
make dev-production
```

**Environment File Loading:**
- `make dev` - Loads `../.env.local` for local development
- `make dev-staging` - Loads `../.env` for staging environment
- `make dev-production` - Loads `../.env` for production environment

#### For Building the CLI Binary

Use `make build` to produce the actual CLI binary:

```bash
cd cli
make build      # Build the CLI binary (uses ../.env by default)
```

The build process automatically loads environment variables from `../.env` and embeds them into the binary at build time for security.

#### Platform-Specific Builds

You can also build for specific platforms:

```bash
cd cli

# Build for Linux
make build-cli-linux

# Build for macOS (Intel)
make build-cli-darwin

# Build for macOS (Apple Silicon) - default
make build-cli-darwin-arm64
```

### Additional Setup (Supabase Functions)

You'll need to start the Supabase functions for the CLI to work properly:

```bash
cd supabase
supabase functions serve
```

This is required for both local and remote Supabase instances.

### Running the Built Binary

After building with `make build`, you can run the binary directly:

```bash
./bin/prod-[version]-darwin-arm64
```

**Note:** Logs will be sent to `~/.prod/logs.txt`

## CLI Authentication System

The Prod CLI uses a modern web-based authentication system that supports both OAuth and email/password authentication.

### Authentication Flow

1. **Start Authentication:**
   ```bash
   ./prod auth login
   ```

2. **Browser Opens:** The CLI automatically opens your browser to the authentication page

3. **Choose Authentication Method:**
   - **OAuth:** Sign in with GitHub, Google, or other providers
   - **Email/Password:** Use email and password authentication
   - **Sign Up:** Create a new account with email/password

4. **Automatic Token Storage:** The CLI automatically receives and stores your authentication token

### Authentication Commands

```bash
# Login to the CLI
./prod auth login

# Check authentication status
./prod auth status

# Logout from the CLI
./prod auth logout
```

### Environment Support
The environment is automatically determined by the `ENVIRONMENT` variable in your `.env` file:

```bash
# Default: staging (recommended for local development)
ENVIRONMENT=staging

# For local Supabase development
ENVIRONMENT=local

# For production use
ENVIRONMENT=production
```

#### Environment Configuration Details

- **Local Environment (`ENVIRONMENT=local`):**
  - Uses local Supabase instance (`http://localhost:54321`)
  - Authentication page runs on `http://localhost:5175/cli-auth`
  - Requires local Supabase and website to be running

- **Staging Environment (`ENVIRONMENT=staging`):**
  - Uses remote Supabase instance
  - Recommended for local development (stable, no local setup required)

- **Production Environment (`ENVIRONMENT=production`):**
  - Uses production Supabase instance
  - For production deployments

### Authentication Features

- **✅ OAuth Integration:** GitHub, Google, and other providers
- **✅ Email/Password Authentication:** Traditional login with validation
- **✅ Account Creation:** Sign up with email confirmation
- **✅ Password Security:** Strong password requirements and validation
- **✅ Automatic Token Management:** No manual token copying required
- **✅ Error Handling:** Clear error messages with retry options
- **✅ Multi-Environment Support:** Works with staging and production

### Troubleshooting Build Issues

If you encounter `SUPABASE_ANON_KEY environment variable not set`:

1. **Ensure `.env` file exists:**
   ```bash
   cp env.example .env
   ```

2. **For local development, create `.env.local`:**
   ```bash
   cp env.example .env.local
   # Edit .env.local with your local development settings
   ```

3. **Verify the correct environment file is being used:**
   - `make dev` looks for `../.env.local`
   - `make dev-staging` and `make dev-production` look for `../.env`
   - `make build` looks for `../.env`

4. **Check that environment variables are loaded:**
   ```bash
   cd cli
   make dev  # Should show "Loading ../.env.local..." if file exists
   ```

5. **Environment file location:**
   - The Makefile looks for `.env` files in the parent directory (`../`)
   - Make sure your `.env` or `.env.local` file is in the project root, not in the `cli/` directory

## Updating Prompts
   1. We are currently using BAML for handling our LLM interations. BAML requires a generation step to update the prompts and clients.
   2. When you change a prompt, run `make generate` from `prod/cli` and this willy generate updated code. When things are working, please commit this code. 

## Output Pattern Guide

This explains how to write output in the codebase using the standard `fmt.Fprintf(out, ...)` pattern.

## Basic Pattern

Use `fmt.Fprintf(out, ...)` for all output throughout the codebase:

```go
func DoSomething(ctx context.Context, out io.Writer) error {
    // Regular output
    fmt.Fprintf(out, "Starting operation...\n")
    
    // Do work...
    
    fmt.Fprintf(out, "✅ Operation completed successfully\n")
    return nil
}
```

## Spinner Messages

Spinners are triggered automatically based on message patterns. Use these patterns to get automatic spinners in TUI mode:

### Automatic Spinner Triggers

These patterns will automatically start spinners:

```go
// Docker operations
fmt.Fprintf(out, "Generating Dockerfile...\n")
fmt.Fprintf(out, "Building Docker image...\n")
fmt.Fprintf(out, "Tagging image for registry...\n")
fmt.Fprintf(out, "Pushing image to registry...\n")

// Deployment operations
fmt.Fprintf(out, "🔄 Executing: Creating web service...\n")
fmt.Fprintf(out, "🔄 Executing: Setting up database...\n")

// Authentication
fmt.Fprintf(out, "Checking Render authentication...\n")
fmt.Fprintf(out, "🔍 Validating API key...\n")
```

### Automatic Spinner Stop Triggers

These patterns will automatically stop spinners:

```go
// Success messages
fmt.Fprintf(out, "✓ Successfully built image\n")
fmt.Fprintf(out, "✅ API key validated successfully\n")
fmt.Fprintf(out, "✅ Authentication successful\n")

// Error messages
fmt.Fprintf(out, "❌ Failed to build image: %v\n", err)
fmt.Fprintf(out, "✗ Failed to authenticate\n")
fmt.Fprintf(out, "Error: %v\n", err)
```

## Examples

### Simple Service Function

```go
func DeployService(ctx context.Context, serviceName string, out io.Writer) error {
    // This will trigger a spinner automatically
    fmt.Fprintf(out, "🔄 Executing: Deploying %s...\n", serviceName)
    
    // Simulate work
    time.Sleep(2 * time.Second)
    
    // This will stop the spinner automatically
    fmt.Fprintf(out, "✅ Successfully deployed %s\n", serviceName)
    
    // Regular output continues
    fmt.Fprintf(out, "Service URL: https://%s.example.com\n", serviceName)
    return nil
}
```

### Error Handling

```go
func BuildImage(ctx context.Context, imageName string, out io.Writer) error {
    fmt.Fprintf(out, "Building Docker image...\n")
    
    if err := doBuild(); err != nil {
        // This stops any active spinner
        fmt.Fprintf(out, "❌ Failed to build image: %v\n", err)
        return err
    }
    
    fmt.Fprintf(out, "✓ Successfully built image: %s\n", imageName)
    return nil
}
```

## Testing

For tests, you can easily capture or discard output:

```go
func TestDeployService(t *testing.T) {
    var buf bytes.Buffer
    err := DeployService(context.Background(), "test-service", &buf)
    
    assert.NoError(t, err)
    output := buf.String()
    assert.Contains(t, output, "Successfully deployed test-service")
}

// Or discard output in tests that don't need it
func TestSomethingElse(t *testing.T) {
    err := SomeFunction(context.Background(), io.Discard)
    assert.NoError(t, err)
}
```