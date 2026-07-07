// Package plugincmd provides `prod plugin` — install, list, and remove external
// provider plugins that add deploy targets without forking prod. See docs/plugins.md.
package plugincmd

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/pluginhost"
)

// --- parent: `prod plugin` -------------------------------------------------

var (
	_ ecdysis.CommandWithDocs        = (*PluginCommand)(nil)
	_ ecdysis.CommandWithSubCommands = (*PluginCommand)(nil)
	_ ecdysis.CommandWithExecute     = (*PluginCommand)(nil)
)

type PluginCommand struct {
	New     PluginNewCommand     `cmd:"" help:"Scaffold a new provider plugin"`
	Search  PluginSearchCommand  `cmd:"" help:"Search the curated plugin index"`
	Install PluginInstallCommand `cmd:"" help:"Install a provider plugin binary"`
	List    PluginListCommand    `cmd:"" help:"List installed provider plugins"`
	Remove  PluginRemoveCommand  `cmd:"" help:"Remove an installed provider plugin"`
}

func (c *PluginCommand) Usage() string { return "plugin" }

func (c *PluginCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Manage provider plugins",
		Long: "Install, list, and remove external provider plugins — separate binaries that add\n" +
			"deploy targets (a cloud, an internal PaaS) without forking prod. See docs/plugins.md.\n\n" +
			"Plugins run as a subprocess with your permissions; only install ones you trust.",
	}
}

func (c *PluginCommand) SubCommands() []ecdysis.Command {
	return []ecdysis.Command{&c.New, &c.Search, &c.Install, &c.List, &c.Remove}
}

func (c *PluginCommand) Execute(context.Context) error {
	return list(os.Stdout)
}

func manifestPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Errorf("cannot locate home directory: %w", err)
	}
	return pluginhost.DefaultManifestPath(home), nil
}

// --- install ---------------------------------------------------------------

var (
	_ ecdysis.CommandWithExecute = (*PluginInstallCommand)(nil)
	_ ecdysis.CommandWithArgs    = (*PluginInstallCommand)(nil)
	_ ecdysis.CommandWithFlags   = (*PluginInstallCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*PluginInstallCommand)(nil)
)

type installFlags struct {
	Checksum string `long:"checksum" usage:"expected hex sha256 of the binary (verified before it runs; required for a github.com install)"`
	Yes      bool   `long:"yes" short:"y" usage:"skip the confirmation prompt when installing from github.com"`
}

type PluginInstallCommand struct {
	flags installFlags
	path  string
}

func (c *PluginInstallCommand) Usage() string { return "install" }

func (c *PluginInstallCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Install a provider plugin binary",
		Long: "Install a provider plugin from a local binary path (built as prod-provider-<name>).\n" +
			"prod verifies it's a valid provider, records its sha256, and adds it as a deploy target.\n" +
			"Pass --checksum to verify the binary matches an out-of-band sha256 before it runs.",
	}
}

func (c *PluginInstallCommand) Flags() []ecdysis.Flag { return ecdysis.BuildFlags(&c.flags) }

func (c *PluginInstallCommand) Args(args []string) error {
	if len(args) != 1 {
		return errors.New("install requires exactly one argument: the plugin binary path")
	}
	c.path = args[0]
	return nil
}

func (c *PluginInstallCommand) Execute(ctx context.Context) error {
	out := os.Stdout

	// Remote install from a GitHub release: resolve the asset, verify a required checksum
	// BEFORE the binary is runnable, confirm with the user, then install the verified file
	// via the same local path below.
	if pluginhost.IsGitHubRef(c.path) {
		dest, err := c.installFromGitHub(ctx, out)
		if err != nil {
			return err
		}
		c.path = dest
	} else if strings.HasPrefix(c.path, "http://") || strings.HasPrefix(c.path, "https://") {
		return errors.New("only github.com/owner/repo references are supported for remote install — for any other URL, download the binary yourself, verify it, then install the local path")
	}

	path, err := filepath.Abs(c.path)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return errors.Errorf("no plugin binary at %s", path)
	}

	sum, err := pluginhost.ChecksumFile(path)
	if err != nil {
		return err
	}
	// Verify the binary matches the operator-supplied checksum BEFORE running it.
	if c.flags.Checksum != "" && !strings.EqualFold(c.flags.Checksum, sum) {
		return errors.Errorf("checksum mismatch: binary is %s, --checksum said %s", sum, c.flags.Checksum)
	}

	checksumBytes, _ := hex.DecodeString(sum)
	meta, err := pluginhost.Inspect(path, checksumBytes)
	if err != nil {
		return errors.Errorf("%s is not a usable prod provider plugin: %w", path, err)
	}
	if meta.Name == "" {
		return errors.Errorf("plugin at %s reported no name", path)
	}

	mp, err := manifestPath()
	if err != nil {
		return err
	}
	entry := pluginhost.Entry{
		Name: meta.Name, Aliases: meta.Aliases, DomainSuffix: meta.DomainSuffix,
		SupportsRollback: meta.SupportsRollback, Path: path, Checksum: sum,
	}
	if err := pluginhost.Upsert(mp, entry); err != nil {
		return err
	}

	fmt.Fprintf(out, "✅ Installed provider plugin %q\n", meta.Name)
	if len(meta.Aliases) > 0 {
		fmt.Fprintf(out, "   deploy with: prod \"deploy this to %s\"\n", meta.Aliases[0])
	}
	fmt.Fprintf(out, "   path:     %s\n   checksum: %s\n", path, sum)
	fmt.Fprintln(out, "\n⚠️  Plugins run as a subprocess with your permissions. Only install ones you trust.")
	return nil
}

// installFromGitHub resolves a github.com/owner/repo[@version] reference to a release
// asset, requires a checksum (the security gate), shows the publisher + checksum, confirms
// with the user, and downloads the asset to ~/.prod/plugins verifying the checksum BEFORE
// the binary is ever runnable. Returns the verified local path for the normal install flow.
func (c *PluginInstallCommand) installFromGitHub(ctx context.Context, out *os.File) (string, error) {
	ref, err := pluginhost.ParseGitHubRef(c.path)
	if err != nil {
		return "", err
	}
	if c.flags.Checksum == "" {
		return "", errors.New("installing from github.com requires --checksum <sha256> (the plugin's published binary checksum for your OS/arch) so the download is verified before it runs")
	}
	assetURL, tag, err := pluginhost.ResolveGitHubAsset(ctx, ref)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(out, "Plugin:    %s/%s\n", ref.Owner, ref.Repo)
	fmt.Fprintf(out, "Publisher: %s (github.com)\n", ref.Owner)
	fmt.Fprintf(out, "Release:   %s\n", tag)
	fmt.Fprintf(out, "Asset:     %s\n", assetURL)
	fmt.Fprintf(out, "Checksum:  %s (sha256, verified before the binary runs)\n", strings.ToLower(c.flags.Checksum))
	fmt.Fprintln(out, "\n⚠️  Plugins run as a subprocess with your permissions. Only install ones you trust.")
	if !c.flags.Yes && !confirm(out, "Download and install this plugin?") {
		return "", errors.New("aborted")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(ref.Repo, "prod-provider-")
	dest := filepath.Join(home, ".prod", "plugins", "prod-provider-"+name)
	if err := pluginhost.DownloadVerified(ctx, assetURL, c.flags.Checksum, dest); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "✅ Downloaded and verified → %s\n", dest)
	return dest, nil
}

func confirm(out *os.File, prompt string) bool {
	fmt.Fprintf(out, "%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// --- list ------------------------------------------------------------------

var (
	_ ecdysis.CommandWithExecute = (*PluginListCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*PluginListCommand)(nil)
)

type PluginListCommand struct{}

func (c *PluginListCommand) Usage() string { return "list" }
func (c *PluginListCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{Short: "List installed provider plugins"}
}
func (c *PluginListCommand) Execute(context.Context) error { return list(os.Stdout) }

func list(out *os.File) error {
	mp, err := manifestPath()
	if err != nil {
		return err
	}
	entries, err := pluginhost.LoadManifest(mp)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(out, "No provider plugins installed. See docs/plugins.md to write and install one.")
		return nil
	}
	for _, e := range entries {
		status := "ok"
		if cur, err := pluginhost.ChecksumFile(e.Path); err != nil {
			status = "missing binary"
		} else if !strings.EqualFold(cur, e.Checksum) {
			status = "checksum changed — reinstall"
		}
		fmt.Fprintf(out, "%-24s %-28s [%s]\n", e.Name, strings.Join(e.Aliases, ", "), status)
	}
	return nil
}

// --- remove ----------------------------------------------------------------

var (
	_ ecdysis.CommandWithExecute = (*PluginRemoveCommand)(nil)
	_ ecdysis.CommandWithArgs    = (*PluginRemoveCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*PluginRemoveCommand)(nil)
)

type PluginRemoveCommand struct{ name string }

func (c *PluginRemoveCommand) Usage() string { return "remove" }
func (c *PluginRemoveCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{Short: "Remove an installed provider plugin (by name)"}
}

func (c *PluginRemoveCommand) Args(args []string) error {
	if len(args) != 1 {
		return errors.New("remove requires exactly one argument: the plugin name")
	}
	c.name = args[0]
	return nil
}

func (c *PluginRemoveCommand) Execute(context.Context) error {
	mp, err := manifestPath()
	if err != nil {
		return err
	}
	removed, err := pluginhost.Remove(mp, c.name)
	if err != nil {
		return err
	}
	if !removed {
		return errors.Errorf("no installed plugin named %q", c.name)
	}
	fmt.Fprintf(os.Stdout, "Removed provider plugin %q\n", c.name)
	return nil
}
