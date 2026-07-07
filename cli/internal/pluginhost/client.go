package pluginhost

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"strings"

	"github.com/go-errors/errors"
	hclog "github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/pushtoprodai/prod-cli/pkg/plugin"
)

// Launch starts a provider plugin subprocess and returns a live Provider plus a close
// func. The child environment is CURATED (SkipHostEnv + a deny-listed env) so a plugin
// cannot read prod's platform tokens, registry credential, or LLM key — it sees only
// PATH/HOME and its own cloud creds. If checksum is non-nil the binary must match it.
func Launch(path string, checksum []byte) (plugin.Provider, func(), error) {
	cmd := exec.Command(path)
	cmd.Env = curateEnv(os.Environ())

	cfg := &goplugin.ClientConfig{
		HandshakeConfig:  plugin.Handshake,
		Plugins:          plugin.HostPlugins(),
		Cmd:              cmd,
		SkipHostEnv:      true, // do NOT inject prod's env into the plugin
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolNetRPC},
		AutoMTLS:         true, // a local rogue process can't connect to the plugin socket
		Managed:          true,
		Logger:           hclog.NewNullLogger(),
	}
	if len(checksum) > 0 {
		cfg.SecureConfig = &goplugin.SecureConfig{Checksum: checksum, Hash: sha256.New()}
	}

	client := goplugin.NewClient(cfg)
	conn, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, nil, errors.Errorf("failed to launch plugin %q (protocol/checksum mismatch?): %w", path, err)
	}
	prov, err := plugin.Dispense(conn)
	if err != nil {
		client.Kill()
		return nil, nil, errors.Errorf("plugin %q: %w", path, err)
	}
	return prov, client.Kill, nil
}

// prodSensitiveEnv is the set of variable names/prefixes a plugin must never see —
// prod's own platform tokens, registry credential, and LLM keys.
var (
	prodSensitiveExact = map[string]bool{
		// prod's own platform tokens + registry/LLM credentials.
		"FLY_API_TOKEN": true, "FLY_ACCESS_TOKEN": true,
		"RENDER_API_KEY": true, "VERCEL_TOKEN": true, "NETLIFY_AUTH_TOKEN": true,
		"HEROKU_API_KEY": true, "HEROKU_AUTH_TOKEN": true,
		"OPENAI_API_KEY": true, "ANTHROPIC_API_KEY": true,
		"GOOGLE_APPLICATION_CREDENTIALS": true,
		"AZURE_CLIENT_SECRET":            true, "AZURE_TENANT_ID": true, "AZURE_CLIENT_ID": true,
		// Third-party developer secrets commonly present in a shell that a provider plugin
		// has no business inheriting from prod's session.
		"GITHUB_TOKEN": true, "GH_TOKEN": true, "GITLAB_TOKEN": true,
		"DIGITALOCEAN_TOKEN": true, "DIGITALOCEAN_ACCESS_TOKEN": true, "DO_API_TOKEN": true,
		"NPM_TOKEN": true, "DOCKERHUB_TOKEN": true, "DOCKER_PASSWORD": true, "CLOUDFLARE_API_TOKEN": true,
		// Database / cache connection strings (carry credentials in the URL).
		"DATABASE_URL": true, "DATABASE_URI": true, "POSTGRES_URL": true, "POSTGRESQL_URL": true,
		"MYSQL_URL": true, "REDIS_URL": true, "MONGODB_URI": true, "MONGO_URL": true,
	}
	// Prefix families: prod's own vars (PROD_), the whole AWS_ credential family, and
	// GITHUB_/DIGITALOCEAN_/AZURE_ token variants beyond the exact names above.
	prodSensitivePrefix = []string{"AWS_", "PROD_", "GITHUB_", "DIGITALOCEAN_"}
)

// curateEnv filters a parent environment down to what a plugin may see: everything
// except prod's own sensitive credentials. This is a deny-list (a plugin can still
// resolve its OWN cloud creds, e.g. ACME_TOKEN), which is the pragmatic v1 posture;
// an explicit allow-list is a follow-up.
func curateEnv(parent []string) []string {
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if isProdSensitive(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func isProdSensitive(name string) bool {
	if prodSensitiveExact[name] {
		return true
	}
	for _, p := range prodSensitivePrefix {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
