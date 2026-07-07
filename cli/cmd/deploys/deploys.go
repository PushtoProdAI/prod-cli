// Package deployscmd provides `prod ls`, `prod open`, and `prod logs` — the human
// launcher over local deploy history. It reads ~/.prod/history.json and resolves each
// record through the deploytarget package to deep-link to the platform's console and
// logs, so the CLI and the MCP tools share one source of per-cloud knowledge.
package deployscmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deploytarget"
	"github.com/pushtoprodai/prod-cli/internal/history"
)

func loadHistory() ([]history.Record, error) {
	store, err := history.NewStore()
	if err != nil {
		return nil, err
	}
	return store.List(0)
}

// --- prod ls -----------------------------------------------------------------

type lsFlags struct {
	All      bool   `long:"all" usage:"include rollbacks, destroys, and failed operations (default: successful deploys)"`
	Platform string `long:"platform" usage:"only deploys to this platform (e.g. fly, 'cloud run')"`
	JSON     bool   `long:"json" usage:"emit the list as JSON"`
}

type LsCommand struct{ flags lsFlags }

func (c *LsCommand) Usage() string { return "ls" }
func (c *LsCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{Short: "List recent deployments from local history"}
}
func (c *LsCommand) Flags() []ecdysis.Flag { return ecdysis.BuildFlags(&c.flags) }

func (c *LsCommand) Execute(context.Context) error {
	records, err := loadHistory()
	if err != nil {
		return err
	}
	want := history.CanonicalPlatform(c.flags.Platform)

	type row struct {
		Name        string `json:"name"`
		Platform    string `json:"platform"`
		Status      string `json:"status"`
		Age         string `json:"age"`
		URL         string `json:"url,omitempty"`
		CanRollback bool   `json:"canRollback"`
	}
	var rows []row
	for _, r := range records {
		if !c.flags.All && (r.OperationType != "deploy" || r.Status != "success") {
			continue
		}
		if want != "" && history.CanonicalPlatform(r.Platform) != want {
			continue
		}
		t := deploytarget.Resolve(r)
		rows = append(rows, row{r.ResourceName, t.Platform, r.Status, relAge(r.StartedAt), t.LiveURL, t.CanRollback})
	}

	if c.flags.JSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(rows) == 0 {
		fmt.Println("No deployments yet. Try: prod \"deploy this to fly\"")
		return nil
	}
	fmt.Printf("%-20s %-16s %-9s %-8s %s\n", "NAME", "PLATFORM", "STATUS", "AGE", "URL")
	for _, r := range rows {
		rb := ""
		if r.CanRollback {
			rb = " ↩"
		}
		fmt.Printf("%-20s %-16s %-9s %-8s %s%s\n", trunc(r.Name, 20), r.Platform, statusGlyph(r.Status), r.Age, r.URL, rb)
	}
	return nil
}

// --- prod open <app> ---------------------------------------------------------

type openFlags struct {
	Console bool `long:"console" usage:"open the platform's dashboard for the app instead of its live URL"`
}

type OpenCommand struct {
	flags openFlags
	app   string
}

func (c *OpenCommand) Usage() string { return "open" }
func (c *OpenCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{Short: "Open a deployed app's live URL (or --console for its dashboard)"}
}
func (c *OpenCommand) Flags() []ecdysis.Flag { return ecdysis.BuildFlags(&c.flags) }
func (c *OpenCommand) Args(args []string) error {
	if len(args) != 1 {
		return errors.New("open requires exactly one argument: the app name (see `prod ls`)")
	}
	c.app = args[0]
	return nil
}

func (c *OpenCommand) Execute(context.Context) error {
	t, err := resolveApp(c.app)
	if err != nil {
		return err
	}
	target := t.LiveURL
	if c.flags.Console {
		target = t.ConsoleURL
	}
	if target == "" {
		which := "live URL"
		if c.flags.Console {
			which = "console URL"
		}
		if t.Note != "" {
			return errors.Errorf("no %s for %q — %s", which, c.app, t.Note)
		}
		return errors.Errorf("no %s recorded for %q", which, c.app)
	}
	fmt.Printf("Opening %s\n", target)
	return openURL(target)
}

// --- prod logs <app> ---------------------------------------------------------

type LogsCommand struct{ app string }

func (c *LogsCommand) Usage() string { return "logs" }
func (c *LogsCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{Short: "Tail a deployed app's logs via the platform's own CLI"}
}

func (c *LogsCommand) Args(args []string) error {
	if len(args) != 1 {
		return errors.New("logs requires exactly one argument: the app name (see `prod ls`)")
	}
	c.app = args[0]
	return nil
}

func (c *LogsCommand) Execute(ctx context.Context) error {
	t, err := resolveApp(c.app)
	if err != nil {
		return err
	}
	if t.LogsCmd == "" {
		if t.ConsoleURL != "" {
			return errors.Errorf("no logs CLI known for %s — view logs in the console: %s", t.Platform, t.ConsoleURL)
		}
		return errors.Errorf("no logs command for %q (%s)", c.app, t.Note)
	}
	fields := strings.Fields(t.LogsCmd)
	bin := fields[0]
	if _, err := exec.LookPath(bin); err != nil {
		fmt.Printf("The %q CLI isn't installed. To view logs, install it and run:\n  %s\n", bin, t.LogsCmd)
		if t.ConsoleURL != "" {
			fmt.Printf("Or open the console: %s\n", t.ConsoleURL)
		}
		return nil
	}
	// Print the command so the user learns it, then exec it (streams live).
	fmt.Printf("$ %s\n", t.LogsCmd)
	cmd := exec.CommandContext(ctx, bin, fields[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// --- shared helpers ----------------------------------------------------------

func resolveApp(app string) (deploytarget.Target, error) {
	records, err := loadHistory()
	if err != nil {
		return deploytarget.Target{}, err
	}
	r, ok := history.LatestForApp(records, app)
	if !ok {
		return deploytarget.Target{}, errors.Errorf("no deploy found for %q — run `prod ls` to see known apps", app)
	}
	return deploytarget.Resolve(r), nil
}

func openURL(url string) error {
	var bin string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		bin = "open"
	case "windows":
		bin, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		bin = "xdg-open"
	}
	return exec.Command(bin, append(args, url)...).Start()
}

func relAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func statusGlyph(s string) string {
	switch s {
	case "success":
		return "✅ ok"
	case "failed":
		return "❌ fail"
	default:
		return s
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
