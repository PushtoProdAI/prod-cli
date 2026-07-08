package run

import (
	"bufio"
	"context"
	"os"
	"strings"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/agent"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

var (
	_ ecdysis.CommandWithExecute = (*RunCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*RunCommand)(nil)
	_ ecdysis.CommandWithArgs    = (*RunCommand)(nil)
	_ ecdysis.CommandWithFlags   = (*RunCommand)(nil)
)

type RunFlags struct {
	DryRun  bool     `long:"dry-run" usage:"show the plan and estimated cost without deploying"`
	Yes     bool     `long:"yes" short:"y" usage:"skip the approval prompt and deploy (for automation)"`
	Name    string   `long:"name" usage:"override the deployed app name (for CI / per-PR previews, e.g. myapp-pr-7)"`
	Env     []string `long:"env" usage:"set an env var value (KEY=VALUE), repeatable — for headless CI; a value on a var prod didn't detect routes to platform secrets"`
	EnvFile string   `long:"env-file" usage:"read env var values from a KEY=VALUE file (e.g. .env.ci) for headless CI"`
}

type RunArgs struct {
	prompt string
}

type RunCommand struct {
	flags        RunFlags
	args         RunArgs
	output       ecdysis.Output
	Agent        *agent.Agent
	StatusWriter output.StatusWriter
}

func (c *RunCommand) Args(args []string) error {
	if len(args) != 1 {
		return errors.New("run command requires exactly one argument: the prompt")
	}
	c.args.prompt = args[0]
	return nil
}

func (c *RunCommand) Execute(ctx context.Context) error {
	c.Agent.SetDryRun(c.flags.DryRun)
	c.Agent.SetNameOverride(c.flags.Name)

	overrides, err := buildEnvOverrides(c.flags.EnvFile, c.flags.Env)
	if err != nil {
		return err
	}
	c.Agent.SetEnvOverrides(overrides)

	// JSON mode is the MCP/automation substrate. With --yes it's fully headless (CI): the
	// same auto-approve driver as the console path runs the deploy to completion emitting
	// JSON events, needing no stdin — so a GitHub Action doesn't have to script approval on
	// stdin. Without --yes it stays the interactive substrate: the driving client feeds
	// approval and env-var answers on stdin.
	if os.Getenv("PROD_JSON_MODE") == "true" {
		if c.flags.Yes {
			c.Agent.DriveOneShot(ctx, c.args.prompt, c.StatusWriter, os.Stdin, true)
			return nil
		}
		c.Agent.SetInteractive(true)
		c.Agent.Process(ctx, c.args.prompt, c.StatusWriter)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			c.Agent.Process(ctx, scanner.Text(), c.StatusWriter)
			if c.Agent.IsComplete() {
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return errors.Errorf("error reading from stdin: %w", err)
		}
		return nil
	}

	// Plain terminal: drive the flow to completion — read approval/env-var answers
	// from the TTY, or auto-approve with --yes — instead of dead-ending at the
	// confirmation prompt.
	c.Agent.DriveOneShot(ctx, c.args.prompt, c.StatusWriter, os.Stdin, c.flags.Yes)
	return nil
}

// buildEnvOverrides merges --env-file then --env into a single map (--env wins over the file,
// and both win over the project's .env). Values may be quoted in the file.
func buildEnvOverrides(envFile string, envs []string) (map[string]string, error) {
	m := map[string]string{}
	if envFile != "" {
		data, err := os.ReadFile(envFile)
		if err != nil {
			return nil, errors.Errorf("failed to read --env-file %q: %w", envFile, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			if k == "" {
				continue // skip a stray "=value" / empty-key line
			}
			m[k] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	for _, kv := range envs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, errors.Errorf("invalid --env %q: expected KEY=VALUE", kv)
		}
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, errors.Errorf("invalid --env %q: empty key", kv)
		}
		m[k] = v
	}
	return m, nil
}

func (c *RunCommand) Usage() string { return "run" }

func (c *RunCommand) Flags() []ecdysis.Flag {
	return ecdysis.BuildFlags(&c.flags)
}

func (c *RunCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Run a deployment command",
		Long:  `Execute a single deployment command and exit. Useful for automation and VSCode integration.`,
	}
}

func (c *RunCommand) Write(p []byte) (n int, err error) {
	if c.output != nil {
		c.output.Stdout(string(p))
		return len(p), nil
	}
	return 0, errors.New("output not set")
}

func (c *RunCommand) Output(output ecdysis.Output) {
	c.output = output
}
