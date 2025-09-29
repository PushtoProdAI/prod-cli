# Testing GitHub Actions Locally

This guide explains how to test GitHub Actions locally using `act` and provides two different approaches for building and uploading CLI binaries to Supabase Storage.

## 🚀 Quick Start

### 1. Install `act`

```bash
# macOS
brew install act

# Linux
curl https://raw.githubusercontent.com/nektos/act/master/install.sh | sudo bash

# Or download from: https://github.com/nektos/act
```

### 2. Set up Environment

```bash
# Copy environment variables
cp env.example .env

# Edit .env with your Supabase credentials
nano .env
```

### 3. Run the Test Script

```bash
./scripts/test-github-action.sh
```

## 📋 Available Workflows

### 1. REST API Approach (`build-and-upload-cli.yml`)

**Pros:**
- ✅ No external dependencies
- ✅ Works in any environment
- ✅ Direct control over uploads
- ✅ More reliable

**Cons:**
- ❌ More code to maintain
- ❌ Manual error handling

**When to use:** Production environments, CI/CD pipelines

### 2. Supabase CLI Approach (`build-and-upload-cli-cli.yml`)

**Pros:**
- ✅ Built-in features (retries, progress)
- ✅ Simpler commands
- ✅ Better error handling

**Cons:**
- ❌ Requires Supabase CLI installation
- ❌ Version compatibility issues
- ❌ Additional setup

**When to use:** Development, when you need CLI features

## 🔧 Manual Testing

### Test REST API Workflow

```bash
# Create test tag
git tag v0.0.0-test-$(date +%s)

# Run workflow
act push \
  --secret-file .secrets \
  --env GITHUB_REF_NAME="v0.0.0-test" \
  --workflows .github/workflows/build-and-upload-cli.yml \
  --verbose

# Clean up
git tag -d v0.0.0-test-*
```

### Test Supabase CLI Workflow

```bash
# Run CLI workflow
act push \
  --secret-file .secrets \
  --env GITHUB_REF_NAME="v0.0.0-test" \
  --workflows .github/workflows/build-and-upload-cli-cli.yml \
  --verbose
```

### Test Workflow Dispatch

```bash
# Test manual trigger
act workflow_dispatch \
  --secret-file .secrets \
  --workflows .github/workflows/build-and-upload-cli.yml \
  --input version="v0.0.0-test-dispatch" \
  --verbose
```

## 🔐 Required Secrets

Create a `.secrets` file with:

```bash
# Supabase credentials
SUPABASE_URL=https://your-project.supabase.co
SUPABASE_SERVICE_ROLE_KEY=your-service-role-key

# For CLI approach (optional)
SUPABASE_PROJECT_ID=your-project-ref
SUPABASE_ACCESS_TOKEN=your-access-token

# GitHub token (for releases)
GITHUB_TOKEN=your-github-token
```

## 🐛 Troubleshooting

### Common Issues

1. **"act not found"**
   ```bash
   # Install act
   brew install act  # macOS
   # or download from GitHub releases
   ```

2. **"Secrets file not found"**
   ```bash
   # Create secrets file
   ./scripts/test-github-action.sh
   # This will create .secrets file
   ```

3. **"Supabase connection failed"**
   - Check your `.env` file has correct credentials
   - Verify Supabase project is accessible
   - Check network connectivity

4. **"GoReleaser build failed"**
   - Ensure you're in the correct directory
   - Check Go version compatibility
   - Verify all dependencies are installed

### Debug Mode

Run with verbose output:

```bash
act push --verbose --secret-file .secrets
```

### Dry Run

Test without actually uploading:

```bash
act push --dry-run --secret-file .secrets
```

## 📊 Workflow Comparison

| Feature | REST API | Supabase CLI |
|---------|----------|--------------|
| Dependencies | None | Supabase CLI |
| Reliability | High | Medium |
| Error Handling | Manual | Built-in |
| Setup Time | Low | Medium |
| Maintenance | High | Low |
| CI/CD Friendly | Yes | Yes |

## 🎯 Recommendations

### For Production
Use the **REST API approach** because:
- No external dependencies
- More reliable in CI/CD
- Direct control over uploads
- Better error handling

### For Development
Use the **Supabase CLI approach** because:
- Simpler commands
- Built-in features
- Easier debugging
- Better user experience

## 🔄 Continuous Integration

### GitHub Secrets Required

Add these secrets to your GitHub repository:

1. `SUPABASE_URL` - Your Supabase project URL
2. `SUPABASE_SERVICE_ROLE_KEY` - Service role key for API access
3. `SUPABASE_PROJECT_ID` - Project reference ID (for CLI approach)
4. `SUPABASE_ACCESS_TOKEN` - Access token (for CLI approach)

### Triggering Workflows

**Automatic (on tags):**
```bash
git tag v1.0.0
git push origin v1.0.0
```

**Manual (workflow dispatch):**
- Go to Actions tab in GitHub
- Select "Build and Upload CLI"
- Click "Run workflow"
- Enter version (e.g., v1.0.0)

## 📝 Next Steps

1. **Test locally** using the provided scripts
2. **Choose your approach** (REST API recommended for production)
3. **Set up GitHub secrets** with your Supabase credentials
4. **Create a test tag** and push to trigger the workflow
5. **Verify uploads** in your Supabase Storage dashboard

## 🆘 Getting Help

If you encounter issues:

1. Check the [act documentation](https://github.com/nektos/act)
2. Review the workflow logs
3. Verify your environment variables
4. Test with a simple workflow first
5. Check Supabase Storage permissions
