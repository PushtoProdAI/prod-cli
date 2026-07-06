# Writing a prod provider plugin

prod deploys to a built-in set of clouds (Fly, Render, Vercel, Netlify, Heroku, AWS
App Runner, Google Cloud Run, Azure Container Apps). A **provider plugin** lets you add
your own deploy target — a different cloud, an internal PaaS — as a **separate binary**,
without forking prod. prod discovers your plugin and drives it over a subprocess; your
plugin becomes a first-class target you can deploy to in plain English.

> **The model:** a plugin is the *cloud-service half* of a deploy. prod keeps the LLM,
> the project analysis, and the **docker build + push** (to the registry your plugin
> names); your plugin only **creates/updates the service and reports its URL**. Your
> plugin never sees the user's source — it receives a built image reference.

## 1. Implement the `Provider` interface

Add prod's plugin SDK to your module and implement six methods:

```go
import "github.com/pushtoprodai/prod-cli/pkg/plugin"

type acme struct{}

func (acme) Metadata(context.Context) (plugin.Meta, error) {
    return plugin.Meta{
        Name:             "Acme Cloud",
        Aliases:          []string{"acme", "acme-cloud"}, // natural-language + menu names
        DomainSuffix:     ".acme.app",                    // used for framework host allow-lists
        SupportsRollback: true,
    }, nil
}

// Where prod should push the built image. prod runs the docker build+push with these
// credentials, then calls Deploy with the resulting image reference.
func (acme) RegistryInfo(_ context.Context, project string) (plugin.RegistryInfo, error) {
    return plugin.RegistryInfo{
        Host: "registry.acme.app", Repository: "you/" + project,
        Username: "…", Token: acmeRegistryToken(),
    }, nil
}

// Fail fast if the user's Acme credentials aren't usable.
func (acme) CheckAuth(context.Context) (plugin.AuthStatus, error) {
    if !acmeConfigured() {
        return plugin.AuthStatus{OK: false, Detail: "run `acme login` first"}, nil
    }
    return plugin.AuthStatus{OK: true}, nil
}

// Create/update the service from the pushed image and return it once serving.
func (acme) Deploy(ctx context.Context, req plugin.DeployRequest) (plugin.DeployResult, error) {
    url, id, err := acmeCreateService(ctx, req.Name, req.ImageRef, req.Port, req.PlainEnv, req.SecretEnv)
    if err != nil { return plugin.DeployResult{}, err }
    return plugin.DeployResult{ID: id, Name: req.Name, URL: url}, nil
}

func (acme) PreviousDeployment(ctx context.Context, app string) (plugin.DeployInfo, error) { … }
func (acme) Rollback(ctx context.Context, app, targetID string) error { … }

func main() { plugin.Serve(acme{}) }
```

Resolve **your own** cloud credentials from the user's ambient config (as prod's
built-in adapters read `~/.aws`) — prod does not, and will not, pass you its own
platform tokens (see Security).

The reference implementation is
[`cli/examples/prod-provider-example`](../cli/examples/prod-provider-example/main.go) —
copy it as a starting point.

## 2. Build it as `prod-provider-<name>`

```
go build -o prod-provider-acme ./cmd/prod-provider-acme
```

## 3. Install it

Register the binary with prod (the `prod plugin` CLI lands with the installer; until
then, add an entry to `~/.prod/plugins.json`):

```json
[
  {
    "name": "Acme Cloud",
    "aliases": ["acme", "acme-cloud"],
    "domainSuffix": ".acme.app",
    "supportsRollback": true,
    "path": "/usr/local/bin/prod-provider-acme",
    "checksum": "<hex sha256 of the binary>"
  }
]
```

prod reads this at startup and registers "Acme Cloud" as a deploy target — it appears
in the menu and resolves by its aliases.

## 4. Deploy to it

```
prod "deploy this to acme"
```

prod plans, builds and pushes the image to the registry your `RegistryInfo` names,
launches your plugin, calls `Deploy`, and reports the URL — the same plan → approve →
deploy → rollback flow as a built-in cloud.

---

## How it fits the architecture

A plugin reuses everything, nothing is rebuilt:

- It registers through the **L1 catalog** with a `Platform` value derived from its name,
  so dispatch, the menu, natural-language matching, the rollback gate, and Django host
  allow-lists all come from your `Metadata` — no new dispatch code.
- It deploys through the **L2 managed-container flow** (`Prepare` → registry + a deploy
  step), so the host owns build+push and guarantees the result shaping; your plugin is
  just the `Deploy`/`Rollback` API calls.
- prod launches your plugin **per deploy activity** and kills it on completion.

## Security — the trust model

A plugin is a binary that runs **on your machine with your permissions** — the same
trust you extend to a Terraform provider or a VS Code extension. prod's guardrails:

- **Explicit, checksum-pinned install.** prod never downloads or runs a plugin you
  didn't install. The recorded SHA-256 must match at launch, so a swapped binary is
  refused.
- **Credential isolation.** prod launches your plugin with a **curated environment**: it
  strips its own platform tokens (Fly/Render/Vercel/Netlify/Heroku/AWS/Azure), the
  container-registry credential, and the LLM API keys. Your plugin sees `PATH`, `HOME`,
  and its own cloud creds — never prod's.
- **mTLS** on the plugin connection, so a local rogue process can't talk to it.
- Your plugin **only** receives the deploy request (the image ref + this app's config +
  env). It gets the app's environment (including secrets) because it deploys the app —
  as any deploy tool must.

Only install plugins you trust.
