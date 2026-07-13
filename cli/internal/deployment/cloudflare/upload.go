package cloudflare

import (
	"context"
	"encoding/base64"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-errors/errors"
)

// specialFileNames are handled by the deployment form, not hashed/uploaded as assets.
var specialFileNames = map[string]bool{"_headers": true, "_redirects": true, "_routes.json": true}

// ignoredNames/ignoredDirs are excluded entirely (Functions/advanced mode is out of scope; the
// rest are junk). Matches wrangler's validate.ts ignore list.
var (
	ignoredNames = map[string]bool{"_worker.js": true, ".DS_Store": true}
	ignoredDirs  = map[string]bool{"functions": true, "node_modules": true, ".git": true, ".wrangler": true}
)

type asset struct {
	rel         string // forward-slash relative path, no leading slash
	abs         string
	hash        string
	contentType string
	size        int64
}

// UploadDir runs the full Cloudflare Pages direct-upload for a built static directory and
// returns the created deployment (with its live URL). The caller has already ensured the
// project exists.
func UploadDir(ctx context.Context, client CloudflareClient, dir, projectName, branch string) (*Deployment, error) {
	assets, special, err := scanDir(dir)
	if err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return nil, errors.Errorf("no files to deploy in %q — did the build produce output?", dir)
	}

	jwt, err := client.GetUploadToken(ctx, projectName)
	if err != nil {
		return nil, errors.Errorf("failed to get Cloudflare upload token: %w", err)
	}
	holder := &jwtHolder{jwt: jwt, refresh: func() (string, error) { return client.GetUploadToken(ctx, projectName) }}

	hashes := make([]string, len(assets))
	byHash := make(map[string]asset, len(assets))
	for i, a := range assets {
		hashes[i] = a.hash
		byHash[a.hash] = a
	}

	missing, err := client.CheckMissing(ctx, holder.get(), hashes)
	if err != nil {
		return nil, errors.Errorf("cloudflare check-missing failed: %w", err)
	}

	if len(missing) > 0 {
		batches, err := bucket(missing, byHash)
		if err != nil {
			return nil, err
		}
		if err := uploadBatches(ctx, client, holder, batches); err != nil {
			return nil, errors.Errorf("cloudflare asset upload failed: %w", err)
		}
	}

	if err := client.UpsertHashes(ctx, holder.get(), hashes); err != nil {
		// Non-fatal to the deploy's correctness (assets are uploaded); log-level, don't block.
		return nil, errors.Errorf("cloudflare upsert-hashes failed: %w", err)
	}

	manifest := make(map[string]string, len(assets))
	for _, a := range assets {
		manifest["/"+a.rel] = a.hash // manifest keys carry a leading slash; discovery paths do not
	}
	dep, err := client.CreateDeployment(ctx, projectName, manifest, special)
	if err != nil {
		return nil, errors.Errorf("cloudflare create-deployment failed: %w", err)
	}
	return dep, nil
}

// scanDir walks dir, hashing each eligible file, and returns the assets plus the special-file
// contents (_headers/_redirects/_routes.json) to attach to the deployment.
func scanDir(dir string) ([]asset, map[string][]byte, error) {
	var assets []asset
	special := map[string][]byte{}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && ignoredDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // wrangler skips symlinks
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		name := d.Name()

		if specialFileNames[name] && !strings.Contains(rel, "/") {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			special[name] = contents
			return nil
		}
		if ignoredNames[name] {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxAssetSize {
			return errors.Errorf("file %q is %d bytes, over Cloudflare Pages' 25 MiB per-file limit", rel, info.Size())
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		ct := mime.TypeByExtension(filepath.Ext(name))
		if ct == "" {
			ct = "application/octet-stream"
		}
		assets = append(assets, asset{
			rel:         rel,
			abs:         path,
			hash:        HashFile(contents, name),
			contentType: ct,
			size:        info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(assets) > maxAssetCount {
		return nil, nil, errors.Errorf("%d files exceeds Cloudflare Pages' %d-file limit", len(assets), maxAssetCount)
	}
	return assets, special, nil
}

// bucket splits the missing hashes into upload batches under the size + count limits, largest
// files first (wrangler's packing order).
func bucket(missing []string, byHash map[string]asset) ([][]AssetUpload, error) {
	miss := make([]asset, 0, len(missing))
	for _, h := range missing {
		if a, ok := byHash[h]; ok {
			miss = append(miss, a)
		}
	}
	sort.Slice(miss, func(i, j int) bool { return miss[i].size > miss[j].size })

	var batches [][]AssetUpload
	var cur []AssetUpload
	var curSize int64
	flush := func() {
		if len(cur) > 0 {
			batches = append(batches, cur)
			cur, curSize = nil, 0
		}
	}
	for _, a := range miss {
		if len(cur) >= maxBucketFiles || (curSize+a.size > maxBucketSize && len(cur) > 0) {
			flush()
		}
		contents, err := os.ReadFile(a.abs)
		if err != nil {
			return nil, err
		}
		cur = append(cur, AssetUpload{
			Key:      a.hash,
			Value:    base64.StdEncoding.EncodeToString(contents),
			Metadata: map[string]string{"contentType": a.contentType},
			Base64:   true,
		})
		curSize += a.size
	}
	flush()
	return batches, nil
}

// jwtHolder guards the upload JWT so concurrent batches can refresh it once on expiry.
type jwtHolder struct {
	mu      sync.Mutex
	jwt     string
	refresh func() (string, error)
}

func (h *jwtHolder) get() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.jwt
}

func (h *jwtHolder) renew() (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tok, err := h.refresh()
	if err != nil {
		return "", err
	}
	h.jwt = tok
	return tok, nil
}

// uploadBatches uploads all batches with bounded concurrency, returning the first error.
func uploadBatches(ctx context.Context, client CloudflareClient, holder *jwtHolder, batches [][]AssetUpload) error {
	sem := make(chan struct{}, uploadConcurus)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for _, b := range batches {
		wg.Add(1)
		sem <- struct{}{}
		go func(batch []AssetUpload) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := uploadBatchWithRetry(ctx, client, holder, batch); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(b)
	}
	wg.Wait()
	return firstErr
}

// uploadBatchWithRetry uploads one batch, refreshing the JWT on 401 and backing off on other
// transient errors (up to uploadAttempts).
func uploadBatchWithRetry(ctx context.Context, client CloudflareClient, holder *jwtHolder, batch []AssetUpload) error {
	var err error
	for attempt := 0; attempt < uploadAttempts; attempt++ {
		err = client.UploadAssets(ctx, holder.get(), batch)
		if err == nil {
			return nil
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == 401 {
			if _, rerr := holder.renew(); rerr != nil {
				return errors.Errorf("failed to refresh Cloudflare upload token: %w", rerr)
			}
			continue // retry immediately with the fresh token
		}
		if attempt < uploadAttempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
	}
	return err
}
