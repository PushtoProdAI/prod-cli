# CLI Distribution Setup

This document describes the automated distribution setup for the Prod CLI using GoReleaser and Supabase Storage.

## Overview

The distribution system consists of:

1. **GoReleaser** - Builds the CLI for multiple architectures and platforms
2. **Supabase Storage** - Hosts the binaries for download
3. **GitHub Actions** - Automated release workflow
4. **Installer Script** - One-line installation for users

## Architecture Support

The CLI is built for the following platforms:

- **Linux**: AMD64, ARM64
- **macOS**: AMD64, ARM64  
- **Windows**: AMD64

## Files Created

### Core Configuration
- `.goreleaser.yml` - GoReleaser configuration
- `.github/workflows/release.yml` - GitHub Actions workflow
- `scripts/upload-to-supabase.sh` - Supabase upload script
- `scripts/test-build.sh` - Local testing script

### Generated Files (during release)
- `dist/` - Built binaries and archives
- Supabase Storage bucket: `cli-binaries`
- Public installer script: `install.sh`

## Setup Instructions

### 1. Environment Variables

Add these secrets to your GitHub repository:

```bash
# Supabase Configuration
SUPABASE_URL=https://your-project.supabase.co
SUPABASE_ANON_KEY=your-anon-key
SUPABASE_SERVICE_ROLE_KEY=your-service-role-key
```

### 2. Supabase Storage Setup

The upload script will automatically create the `cli-binaries` bucket, but you can also create it manually:

```bash
# Using Supabase CLI
supabase storage create cli-binaries --project-ref your-project-ref
```

### 3. Test Local Build

```bash
# Run the test script
./scripts/test-build.sh

# Or manually test GoReleaser
goreleaser release --snapshot --clean --skip-publish
```

## Usage

### Creating a Release

#### Automatic Release (via Git Tag)
```bash
# Create and push a tag
git tag v1.0.0
git push origin v1.0.0
```

#### Manual Release (via GitHub Actions)
1. Go to Actions → Release workflow
2. Click "Run workflow"
3. Enter version (e.g., `v1.0.0`)

### Download URLs

After a release, binaries are available at:

```
https://your-project.supabase.co/storage/v1/object/public/cli-binaries/releases/v1.0.0/
```

### User Installation

Users can install the CLI using:

```bash
# One-line installer
curl -sSL https://your-project.supabase.co/storage/v1/object/public/cli-binaries/install.sh | bash

# Or download manually
wget https://your-project.supabase.co/storage/v1/object/public/cli-binaries/releases/v1.0.0/prod_v1.0.0_linux_amd64.tar.gz
```

## File Structure

```
releases/
├── v1.0.0/
│   ├── prod_v1.0.0_linux_amd64.tar.gz
│   ├── prod_v1.0.0_linux_arm64.tar.gz
│   ├── prod_v1.0.0_darwin_amd64.tar.gz
│   ├── prod_v1.0.0_darwin_arm64.tar.gz
│   ├── prod_v1.0.0_windows_amd64.zip
│   └── manifest.json
└── install.sh
```

## Customization

### Adding New Platforms

Edit `.goreleaser.yml`:

```yaml
builds:
  - id: prod-cli
    goos:
      - linux
      - darwin
      - windows
      - freebsd  # Add new OS
    goarch:
      - amd64
      - arm64
      - 386      # Add new architecture
```

### Custom Build Flags

Update the `ldflags` section in `.goreleaser.yml`:

```yaml
ldflags:
  - -s -w
  - -X 'github.com/meroxa/prod/cli/internal/config.Version={{.Version}}'
  - -X 'github.com/meroxa/prod/cli/internal/config.BuildTime={{.Date}}'
```

### Custom Storage Paths

Modify `scripts/upload-to-supabase.sh`:

```bash
# Change the remote path structure
local remote_path="releases/${VERSION}/${file_name}"
# To:
local remote_path="binaries/${VERSION}/${file_name}"
```

## Troubleshooting

### Build Failures

1. **CGO Issues**: Ensure Docker is available for cross-compilation
2. **Missing Dependencies**: Check that all Go modules are properly imported
3. **Permission Issues**: Ensure the upload script has execute permissions

### Upload Failures

1. **Supabase Authentication**: Verify `SUPABASE_SERVICE_ROLE_KEY` is correct
2. **Bucket Permissions**: Ensure the service role has storage write permissions
3. **Network Issues**: Check Supabase URL and network connectivity

### Installation Issues

1. **Platform Detection**: Verify the installer correctly detects the user's platform
2. **Binary Permissions**: Ensure downloaded binaries have execute permissions
3. **PATH Issues**: Guide users to add the installation directory to their PATH

## Security Considerations

1. **Service Role Key**: Keep `SUPABASE_SERVICE_ROLE_KEY` secure and rotate regularly
2. **Binary Signing**: Consider code signing for macOS and Windows binaries
3. **Checksums**: Always verify checksums before installation
4. **HTTPS**: Ensure all download URLs use HTTPS

## Monitoring

### Release Metrics

Track download statistics through:
- Supabase Storage analytics
- GitHub Release download counts
- Custom analytics in the installer script

### Health Checks

Monitor:
- Supabase Storage availability
- Binary download success rates
- Installer script functionality

## Future Enhancements

1. **Code Signing**: Add code signing for macOS and Windows
2. **Package Managers**: Create Homebrew, Snap, and other package manager releases
3. **Auto-Updates**: Implement automatic update checking
4. **Multiple Channels**: Support beta, stable, and nightly release channels
5. **CDN Integration**: Use a CDN for faster global downloads
