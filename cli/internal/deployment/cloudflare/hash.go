// Package cloudflare deploys static sites to Cloudflare Pages via the direct-upload REST
// API (no wrangler), using the user's own CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID.
package cloudflare

import (
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/zeebo/blake3"
)

// HashFile computes the Cloudflare Pages asset hash for a file, reproducing wrangler's
// algorithm byte-for-byte (workers-sdk deploy-helpers/src/deploy/helpers/hash.ts):
//
//	blake3(base64(contents) + extensionWithoutDot) → hex → first 32 characters.
//
// Two details make or break it: the hash input is the base64 *text* (NOT the raw bytes),
// and the extension has NO leading dot (and is "" for dotfiles / no-extension files, per
// Node's path.extname — which Go's filepath.Ext does NOT match for dotfiles). Get either
// wrong and every upload silently fails the manifest match.
func HashFile(contents []byte, path string) string {
	b64 := base64.StdEncoding.EncodeToString(contents)
	sum := blake3.Sum256([]byte(b64 + nodeExtWithoutDot(path)))
	return hex.EncodeToString(sum[:16]) // 16 bytes = 32 hex chars
}

// nodeExtWithoutDot returns a file's extension the way Node's path.extname does, minus the
// leading dot. Unlike Go's filepath.Ext, a basename whose only dot is its first character
// (e.g. ".gitignore") has NO extension, and a name with no dot returns "".
func nodeExtWithoutDot(path string) string {
	base := filepath.Base(path)
	dot := strings.LastIndex(base, ".")
	if dot <= 0 { // no dot, or the dot is the leading char of a dotfile
		return ""
	}
	return base[dot+1:]
}
