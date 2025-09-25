# Distribution Scripts

This directory contains scripts for building and distributing the Prod CLI.

## Scripts

### `upload-to-supabase.sh`
Uploads built CLI binaries to Supabase Storage for distribution.

**Usage:**
```bash
# Set required environment variables
export SUPABASE_URL="https://your-project.supabase.co"
export SUPABASE_SERVICE_ROLE_KEY="your-service-role-key"
export VERSION="v1.0.0"

# Run the upload script
./upload-to-supabase.sh
```

**Features:**
- Automatically creates Supabase storage bucket if needed
- Uploads all binaries from `dist/` directory
- Creates release manifest with download URLs
- Generates installer script for easy user installation
- Supports multiple platforms and architectures

### `test-build.sh`
Tests the local build process without creating a release.

**Usage:**
```bash
# Test local build
./test-build.sh

# Test with Supabase upload (requires credentials)
export SUPABASE_URL="https://your-project.supabase.co"
export SUPABASE_SERVICE_ROLE_KEY="your-service-role-key"
./test-build.sh
```

**Features:**
- Installs GoReleaser if not present
- Runs snapshot build to test configuration
- Tests Supabase upload functionality
- Cleans up test artifacts

## Environment Variables

### Required for Upload
- `SUPABASE_URL` - Your Supabase project URL
- `SUPABASE_SERVICE_ROLE_KEY` - Service role key for storage access

### Optional
- `VERSION` - Version to upload (defaults to `latest`)
- `BUCKET_NAME` - Storage bucket name (defaults to `cli-binaries`)

## Generated Files

### During Build
- `dist/` - Directory containing built binaries and archives
- `dist/*.tar.gz` - Linux and macOS binaries
- `dist/*.zip` - Windows binaries
- `dist/*_checksums.txt` - SHA256 checksums

### During Upload
- `releases/{VERSION}/` - Versioned release directory in Supabase Storage
- `releases/{VERSION}/manifest.json` - Release manifest with download URLs
- `install.sh` - Public installer script

## Troubleshooting

### Common Issues

1. **Permission Denied**
   ```bash
   chmod +x scripts/*.sh
   ```

2. **Supabase CLI Not Found**
   ```bash
   npm install -g supabase
   # or
   brew install supabase/tap/supabase
   ```

3. **GoReleaser Not Found**
   ```bash
   brew install goreleaser/tap/goreleaser
   # or
   go install github.com/goreleaser/goreleaser@latest
   ```

4. **Build Failures**
   - Ensure all Go dependencies are installed: `go mod download`
   - Check that CGO dependencies are available
   - Verify Docker is running for cross-compilation

### Debug Mode

Run scripts with debug output:
```bash
set -x
./upload-to-supabase.sh
```

### Manual Testing

Test individual components:
```bash
# Test GoReleaser only
goreleaser release --snapshot --clean --skip-publish

# Test Supabase connection
supabase storage ls --project-ref your-project-ref

# Test upload to specific bucket
supabase storage upload cli-binaries test.txt test.txt --project-ref your-project-ref
```

## Integration

These scripts are designed to work with:
- **GoReleaser** - For building multi-platform binaries
- **GitHub Actions** - For automated releases
- **Supabase Storage** - For hosting binaries
- **Custom Installer** - For user-friendly installation

See the main documentation in `docs/distribution.md` for complete setup instructions.
