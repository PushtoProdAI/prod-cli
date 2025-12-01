package run

import (
	"bufio"
	"context"
	"os"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/agent"
	"github.com/meroxa/prod/cli/internal/output"
)

var (
	_ ecdysis.CommandWithExecute = (*RunCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*RunCommand)(nil)
	_ ecdysis.CommandWithArgs    = (*RunCommand)(nil)
	_ ecdysis.CommandWithFlags   = (*RunCommand)(nil)
)

type RunFlags struct {
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
	// Keep interactive=true for JSON mode so state machine waits for user responses
	c.Agent.SetInteractive(true)

	// Use StatusWriter as the output writer - this ensures all output
	// goes through the appropriate writer (JSONWriter in JSON mode)
	// which properly formats all output as JSON events
	c.Agent.Process(ctx, c.args.prompt, c.StatusWriter)

	// Read from stdin for user responses (plan approval, confirmations, etc)
	jsonMode := os.Getenv("PROD_JSON_MODE") == "true"
	if jsonMode {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			input := scanner.Text()
			// Process each line of input from VSCode extension
			c.Agent.Process(ctx, input, c.StatusWriter)

			// Exit gracefully if state machine completed
			if c.Agent.IsComplete() {
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return errors.Errorf("error reading from stdin: %w", err)
		}
	}

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
