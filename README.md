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
