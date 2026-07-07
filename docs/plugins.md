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

**Fast path:** `prod plugin new <name>` scaffolds a buildable plugin against the SDK
(`github.com/pushtoprodai/prod-plugin-sdk` — a lean module that only pulls in
`hashicorp/go-plugin`). Implement the six methods, `go build`, and
`prod plugin install ./prod-provider-<name>`.

**Find & install existing plugins:** `prod plugin search` reads a git-native index
([`PushtoProdAI/prod-plugins`](https://github.com/PushtoProdAI/prod-plugins)) — no backend.
`prod plugin install github.com/org/repo --checksum <sha256>` downloads a plugin's release
binary and verifies the checksum before it ever runs. First-party plugins today:
**DigitalOcean App Platform**, **Koyeb**, and **Railway** (in the `prod-plugins` repo).

## 1. Implement the `Provider` interface

Add prod's plugin SDK to your module and implement six methods:

```go
import plugin "github.com/pushtoprodai/prod-plugin-sdk"

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

```
prod plugin install ./prod-provider-acme
```

prod verifies it's a valid provider, records its sha256, and registers it — from now on
"Acme Cloud" is a deploy target that appears in the menu and resolves by its aliases.
Manage them with:

```
prod plugin list                 # installed plugins + whether their binary still matches
prod plugin remove "Acme Cloud"
```

Pass `--checksum <sha256>` to verify the binary against an out-of-band hash before it
runs. (Installs are local-path for now; the recorded checksum is re-verified at every
launch, so a swapped binary is refused.)

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

## Compatibility & versioning

The contract is the exported package `github.com/pushtoprodai/prod-plugin-sdk` — it
imports nothing from prod's internals, so you compile against a stable interface.

Compatibility is enforced by `plugin.ProtocolVersion`, negotiated in the go-plugin
handshake. A plugin built against one protocol version won't launch under a prod that
expects a different one — prod rejects it with a clear "rebuild against protocol vN"
message rather than misbehaving. The protocol is bumped only on a breaking change to the
`Provider` interface or its request/response types; additive, backward-compatible changes
don't bump it. Pin the `prod-plugin-sdk` version your plugin builds against, and rebuild when
the protocol version changes.

