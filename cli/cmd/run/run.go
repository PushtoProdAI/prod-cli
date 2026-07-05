package run

import (
	"bufio"
	"context"
	"os"

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
	DryRun bool `long:"dry-run" usage:"show the plan and estimated cost without deploying"`
	Yes    bool `long:"yes" short:"y" usage:"skip the approval prompt and deploy (for automation)"`
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

	// JSON mode is the MCP/automation substrate: the driving client feeds approval
	// and env-var answers on stdin. Keep this path exactly as-is.
	if os.Getenv("PROD_JSON_MODE") == "true" {
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
