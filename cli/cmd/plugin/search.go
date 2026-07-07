package plugincmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/pluginhost"
)

type searchFlags struct {
	URL string `long:"index-url" usage:"override the plugin index URL (advanced)"`
}

// PluginSearchCommand lists/searches the curated git-native plugin index.
type PluginSearchCommand struct {
	flags searchFlags
	term  string
}

func (c *PluginSearchCommand) Usage() string { return "search" }

func (c *PluginSearchCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Search the curated plugin index",
		Long: "Search the git-native plugin index — a plain JSON file in a public repo, no backend — for\n" +
			"provider plugins. Run without a term to list everything. Listing a plugin is a pull\n" +
			"request against that file.",
	}
}

func (c *PluginSearchCommand) Flags() []ecdysis.Flag { return ecdysis.BuildFlags(&c.flags) }

func (c *PluginSearchCommand) Args(args []string) error {
	if len(args) > 1 {
		return errors.New("search takes at most one term")
	}
	if len(args) == 1 {
		c.term = args[0]
	}
	return nil
}

func (c *PluginSearchCommand) Execute(ctx context.Context) error {
	url := c.flags.URL
	if url == "" {
		url = pluginhost.DefaultIndexURL
	}
	idx, err := pluginhost.FetchIndex(ctx, url)
	if err != nil {
		return err
	}
	matches := idx.Filter(c.term)
	if len(matches) == 0 {
		fmt.Printf("No plugins matched. The index is a plain JSON file — add yours with a PR:\n  %s\n", url)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tMAINTAINER\tDESCRIPTION")
	for _, e := range matches {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Name, e.Maintainer, e.Description)
	}
	_ = tw.Flush()
	fmt.Printf("\nInstall one with, e.g.:\n  %s\n", matches[0].Install)
	return nil
}
