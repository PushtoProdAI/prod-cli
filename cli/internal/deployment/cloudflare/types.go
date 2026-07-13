package cloudflare

// Cloudflare API types for the Pages direct-upload flow. The /pages/assets/* endpoints
// (check-missing, upload, upsert-hashes) are internal to wrangler and undocumented; the
// project/deployment endpoints are the public Pages REST API.

// Project is a Cloudflare Pages project. A direct-upload project has Source == nil (no git).
type Project struct {
	Name      string `json:"name"`
	Subdomain string `json:"subdomain"`
	Source    any    `json:"source"`
}

// Deployment is the result of creating a Pages deployment. URL is the live *.pages.dev URL.
type Deployment struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// AssetUpload is one file in a batched /pages/assets/upload request. Field names are wire-exact:
// key is the file's 32-char hash, value is base64 of the raw bytes, base64 is always true.
type AssetUpload struct {
	Key      string            `json:"key"`
	Value    string            `json:"value"`
	Metadata map[string]string `json:"metadata"`
	Base64   bool              `json:"base64"`
}

// apiError is a Cloudflare error entry in the standard response envelope.
type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Limits from wrangler's constants.ts.
const (
	maxAssetSize    = 25 * 1024 * 1024 // 25 MiB per file
	maxAssetCount   = 20000            // files per deployment
	maxBucketSize   = 40 * 1024 * 1024 // 40 MiB per upload batch
	maxBucketFiles  = 2000             // files per upload batch (non-Windows)
	uploadConcurus  = 3                // concurrent upload batches
	uploadAttempts  = 5                // per-batch retry attempts
	defaultBranch   = "main"
	cfAPIBaseURL    = "https://api.cloudflare.com/client/v4"
	cfAPIBaseEnvVar = "CLOUDFLARE_API_BASE_URL"
	cfTokenEnvVar   = "CLOUDFLARE_API_TOKEN"
	cfAccountEnvVar = "CLOUDFLARE_ACCOUNT_ID"
)
