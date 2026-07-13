# Cloudflare Pages direct-upload protocol (reverse-engineered)

prod deploys static sites to Cloudflare Pages via the **direct-upload REST API** — no `wrangler`.
Cloudflare doesn't publish this protocol; it's reconstructed from wrangler's source
(`cloudflare/workers-sdk`, `packages/wrangler/src/pages/*` and `deploy-helpers`). The
`/pages/assets/*` endpoints are **internal and undocumented** — pin behavior to a wrangler
version and prove it live (F6) before trusting a change. Implementation:
`cli/internal/deployment/cloudflare/`.

## Auth
- Base: `https://api.cloudflare.com/client/v4` (override `CLOUDFLARE_API_BASE_URL`).
- Account calls (project, deployment): `Authorization: Bearer $CLOUDFLARE_API_TOKEN`.
- Asset calls (`/pages/assets/*`): `Authorization: Bearer <JWT>` from the upload-token call.
- Env: `CLOUDFLARE_API_TOKEN` + `CLOUDFLARE_ACCOUNT_ID` (the user's own creds). Token scope:
  Account → Cloudflare Pages → Edit.

## The file hash (must be byte-exact — `hash.go`)
```
blake3( base64(fileContents) + extensionWithoutDot ) → hex → first 32 chars
```
- Input is the base64 **text**, NOT the raw bytes.
- Extension has **no leading dot**, and is `""` for dotfiles/no-extension (Node `path.extname`
  semantics — Go's `filepath.Ext` differs on dotfiles, so we use `nodeExtWithoutDot`).
- blake3 (not MD5/SHA); first 32 hex chars = hex of the first 16 of the 32-byte digest.
- Library: `github.com/zeebo/blake3` (`blake3.Sum256`).

## The sequence (`upload.go`)
1. `GET /accounts/{acct}/pages/projects` (paginated) — project exists? else
   `POST /accounts/{acct}/pages/projects` `{name, production_branch:"main", deployment_configs:{production:{},preview:{}}}` (no `source` ⇒ direct-upload).
2. `GET /accounts/{acct}/pages/projects/{name}/upload-token` → `result.jwt`.
3. Walk the output dir; hash each file. Skip `_worker.js`, `functions/`, `node_modules`, `.git`,
   `.wrangler`, `.DS_Store`, symlinks. Hold `_headers`/`_redirects`/`_routes.json` aside (special
   files — attached to the deployment, not hashed). Reject files > 25 MiB; cap 20,000 files.
4. `POST /pages/assets/check-missing` (JWT) `{hashes:[all]}` → hashes still needed.
5. Bucket the missing files (≤40 MiB, ≤2000 files/batch, largest first) and
   `POST /pages/assets/upload` (JWT) each batch: `[{key:hash, value:base64(bytes), metadata:{contentType}, base64:true}]`. Concurrency 3, retry 5× w/ backoff, refresh the JWT on 401.
6. `POST /pages/assets/upsert-hashes` (JWT) `{hashes:[all]}`.
7. `POST /accounts/{acct}/pages/projects/{name}/deployments` (API token), multipart:
   `manifest` = JSON `{"/"+path: hash}` (**leading slash**), plus `_headers`/`_redirects`/`_routes.json`
   file parts if present. Response `result.url` (live `*.pages.dev`) + `result.id`.

## Destroy
`DELETE /accounts/{acct}/pages/projects/{name}` — removes the project + all its deployments
(idempotent on 404). Cloudflare Pages provisions no backing databases, so nothing else orphans.

## Scope (v1)
Static sites only (`IsStatic`). Rollback (Cloudflare keeps prior deployments), Pages Functions
(`_worker.js`), custom domains, and env-var/secret bindings are follow-ups.

## Sources
- Hashing: `workers-sdk/.../deploy-helpers/src/deploy/helpers/hash.ts`
- Discovery/limits: `.../wrangler/src/pages/validate.ts`, `.../pages/constants.ts`
- Asset endpoints/batching: `.../wrangler/src/pages/upload.ts`
- Projects: `.../wrangler/src/pages/projects.ts`; deployment: `.../wrangler/src/api/pages/deploy.ts`
- Docs (context): https://developers.cloudflare.com/pages/configuration/api/ ,
  https://developers.cloudflare.com/pages/get-started/direct-upload/
