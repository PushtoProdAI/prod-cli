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

2. The default local Supabase configuration is already set up in `env.example`

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

## Deployment Platform Configuration

To deploy applications using the prod CLI, you'll need to configure API keys for your preferred deployment platforms:

### Render
1. Go to [Render API Documentation](https://render.com/docs/api)
2. Generate an API key from your Render dashboard
3. Add it to your `.env` file:
   ```bash
   RENDER_API_KEY=your-render-api-key-here
   ```

### Fly.io
1. Go to [Fly.io API Documentation](https://fly.io/docs/reference/api/)
2. Generate an API token from your Fly.io dashboard
3. Add it to your `.env` file:
   ```bash
   FLY_API_TOKEN=your-fly-api-token-here
   ```

### Available Make Commands

- `make supabase-start` - Start Supabase local development
- `make supabase-stop` - Stop Supabase local development
- `make supabase-status` - Check Supabase status
- `make supabase-reset` - Reset the database (applies migrations and seeds)
- `make supabase-migration-new` - Create a new migration
- `make supabase-migration-up` - Apply migrations
- `make supabase-seed` - Seed the database
- `make supabase-studio` - Open Supabase Studio in browser

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

The CLI uses build-time configuration via environment variables. You have two main options:

#### For Local Development and Testing
Use `make dev` for local testing - this uses `go run` under the hood and doesn't require building a binary:

```bash
cd cli
source ../.env  # Load environment variables
make dev        # Run in development mode with hot reload
```

#### For Building the CLI Binary
Use `make build` to produce the actual CLI binary:

```bash
cd cli
source ../.env  # Load environment variables
make build      # Build the CLI binary
```

Or build with explicit environment variables:
```bash
cd cli
SUPABASE_URL=http://localhost:54321 SUPABASE_ANON_KEY=your-anon-key make build
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



### Troubleshooting Build Issues

If you encounter `SUPABASE_ANON_KEY environment variable not set`:

1. **Ensure `.env` file exists:**
   ```bash
   cp env.example .env
   ```

2. **Verify environment variables are loaded:**
   ```bash
   echo $SUPABASE_ANON_KEY
   ```

3. **Build with explicit variables:**
   ```bash
   cd cli
   SUPABASE_URL=http://localhost:54321 SUPABASE_ANON_KEY=your-anon-key make build
   ```

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