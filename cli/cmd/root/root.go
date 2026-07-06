package root

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"
	doctorcmd "github.com/pushtoprodai/prod-cli/cmd/doctor"
	mcpcmd "github.com/pushtoprodai/prod-cli/cmd/mcp"
	plugincmd "github.com/pushtoprodai/prod-cli/cmd/plugin"
	"github.com/pushtoprodai/prod-cli/cmd/run"
	"github.com/pushtoprodai/prod-cli/internal/agent"
	"github.com/pushtoprodai/prod-cli/internal/config"
	"github.com/pushtoprodai/prod-cli/internal/output"
	"github.com/pushtoprodai/prod-cli/internal/tui"
)

var (
	_ ecdysis.CommandWithFlags       = (*RootCommand)(nil)
	_ ecdysis.CommandWithExecute     = (*RootCommand)(nil)
	_ ecdysis.CommandWithDocs        = (*RootCommand)(nil)
	_ ecdysis.CommandWithOutput      = (*RootCommand)(nil)
	_ ecdysis.CommandWithArgs        = (*RootCommand)(nil)
	_ ecdysis.CommandWithSubCommands = (*RootCommand)(nil)
)

const exitPrompt = "exit"

type RootFlags struct {
	Version bool `long:"version" short:"v" usage:"show the current Prod version"`
	DryRun  bool `long:"dry-run" usage:"show the plan and estimated cost without deploying"`
	Yes     bool `long:"yes" short:"y" usage:"skip the approval prompt and deploy (for automation)"`
}

type RootArgs struct {
	prompt string
}

type RootCommand struct {
	flags        RootFlags
	args         RootArgs
	output       ecdysis.Output
	Agent        *agent.Agent
	StatusWriter output.StatusWriter
	WriterType   output.WriterType

	// Subcommands
	Run    run.RunCommand          `cmd:"" help:"Run a deployment command"`
	MCP    mcpcmd.MCPCommand       `cmd:"" help:"Start the prod MCP server (stdio)"`
	Doctor doctorcmd.DoctorCommand `cmd:"" help:"Check prerequisites (LLM, Docker)"`
	Plugin plugincmd.PluginCommand `cmd:"" help:"Manage provider plugins (add deploy targets)"`
}

func (c *RootCommand) SubCommands() []ecdysis.Command {
	return []ecdysis.Command{
		&c.Run,
		&c.MCP,
		&c.Doctor,
		&c.Plugin,
	}
}

func (c *RootCommand) Args(args []string) error {
	if len(args) > 1 {
		return errors.New("too many arguments")
	}

	if len(args) == 1 {
		c.args.prompt = args[0]
	}

	return nil
}

func (c *RootCommand) Output(output ecdysis.Output) {
	c.output = output
}

func (c *RootCommand) Execute(ctx context.Context) error {
	if c.flags.Version {
		c.output.Stdout(fmt.Sprintf("%s\n", config.Version))
		return nil
	}

	if c.args.prompt != "" {
		c.processPrompt(c.args.prompt)
		return nil
	}

	// Check if we should use TUI mode
	if c.WriterType == output.WriterTypeTUI {
		// Initialize Bubble Tea model
		model := tui.NewModel(c.Agent)

		// Create Bubble Tea program with mouse support and alternate screen
		program := tea.NewProgram(&model, tea.WithMouseAllMotion(), tea.WithAltScreen())

		// Set the program reference in the model
		model.SetProgram(program)

		// In TUI mode, use TeaWriter directly as the StatusWriter
		if teaWriter, ok := c.Agent.UIOutput.(*tui.TeaWriter); ok {
			// If the StatusWriter is a ProxyWriter, update its target
			if proxyWriter, ok := c.StatusWriter.(*output.ProxyWriter); ok {
				proxyWriter.SetTarget(teaWriter)
			} else {
				c.StatusWriter = teaWriter
			}
		}

		// Run the TUI program
		_, err := program.Run()
		if err != nil {
			return errors.WrapPrefix(err, "failed to run TUI", 0)
		}
	} else {
		// In console mode, just use the existing console writer
		// and process the prompt directly
		if c.args.prompt != "" {
			c.processPrompt(c.args.prompt)
			return nil
		}
		// For console mode without prompt, we might want to show help or handle differently
		return errors.New("console mode requires a prompt argument")
	}

	return nil
}

// processPrompt handles the business logic for processing prompts
// This method is called both when a prompt is provided as an argument
// and when input is captured from interactive mode
func (c *RootCommand) processPrompt(prompt string) {
	ctx := context.Background()
	c.Agent.SetDryRun(c.flags.DryRun)
	// Drive the multi-step flow to completion: read approval/env-var answers from
	// the terminal, or auto-approve with --yes. A single Process call would
	// dead-end at the confirmation prompt.
	c.Agent.DriveOneShot(ctx, prompt, c, os.Stdin, c.flags.Yes)
}

func (c *RootCommand) Usage() string { return "prod" }

func (c *RootCommand) Flags() []ecdysis.Flag {
	return ecdysis.BuildFlags(&c.flags)
}

func (c *RootCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Prod",
		Long: `Deploy from a sentence. Describe intent in English; prod plans it, shows
you the plan and estimated cost, and — once you approve — deploys to your cloud
with your own credentials.

  prod                          start an interactive session
  prod "deploy this to fly"     deploy from a one-line request
  prod --dry-run "deploy ..."   show the plan + cost, deploy nothing
  prod --yes "deploy ..."       skip the approval prompt (automation)
  prod "rollback"               roll back the last deploy`,
	}
}

func (c *RootCommand) Write(p []byte) (n int, err error) {
	if c.output != nil {
		c.output.Stdout(string(p))
		return len(p), nil
	}
	return 0, errors.New("output not set")
}
