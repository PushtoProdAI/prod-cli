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

2. **Check Docker:**
   ```bash
   docker ps
   ```
   Ensure all Supabase containers are running.

3. **View logs:**
   ```bash
   supabase logs
   ```
## Running the Prod CLI
   1. Install Ollama. Currently tested with Ollama3.1 model
   2. Create a .env file in supabase/functions
   3. The contents of that file are in a 1Password in a note called "Prod - Backend AWS"
   4. From the supabase directory, run `supabase functions serve`
   5. Run `PROD_DEBUG=true go run cmd/main.go`
   6. logs will be sent to ~/.prod/logs.txt

## Updating Prompts
   1. We are currently using BAML for handling our LLM interations. BAML requires a generation step to update the prompts and clients.
   2. When you change a prompt, run `make generate` from `prod/cli` and this will generate updated code. When things are working, please commit this code. 

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

## Benefits

1. **Consistent API**: Same pattern everywhere - `fmt.Fprintf(out, ...)`
2. **Automatic Spinners**: Spinners work automatically based on message patterns
3. **Mode Flexibility**: Works in TUI mode (with spinners) and console mode (plain text)
4. **Easy Testing**: Can pass `bytes.Buffer` or `io.Discard` for tests
5. **Standard Go**: Uses standard library functions developers already know

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