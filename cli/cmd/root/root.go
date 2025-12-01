package root

import (
	"context"
	"fmt"
	"math/rand"

	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/cmd/auth"
	"github.com/meroxa/prod/cli/cmd/run"
	"github.com/meroxa/prod/cli/internal/agent"
	"github.com/meroxa/prod/cli/internal/config"
	"github.com/meroxa/prod/cli/internal/output"
	"github.com/meroxa/prod/cli/internal/tui"
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
	Auth auth.AuthCommand `cmd:"" help:"Manage authentication"`
	Run  run.RunCommand   `cmd:"" help:"Run a deployment command"`
}

func (c *RootCommand) SubCommands() []ecdysis.Command {
	return []ecdysis.Command{
		&c.Auth,
		&c.Run,
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
	const banner = `
______              _ 
| ___ \            | |
| |_/ / __ ___   __| |
|  __/ '__/ _ \ / _` + "`" + ` |
| |  | | | (_) | (_| |
\_|  |_|  \___/ \__,_|
`

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
	c.Agent.Process(ctx, prompt, c)
}

func (c *RootCommand) Usage() string { return "prod" }

func (c *RootCommand) Flags() []ecdysis.Flag {
	return ecdysis.BuildFlags(&c.flags)
}

func (c *RootCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Prod",
		Long:  `Prod starts an interactive session by default.`,
	}
}

func (c *RootCommand) Write(p []byte) (n int, err error) {
	if c.output != nil {
		c.output.Stdout(string(p))
		return len(p), nil
	}
	return 0, errors.New("output not set")
}

func greetUser() string {
	prompts := []string{
		"What would you like to deploy today?",
		"Ready to launch something new?",
		"What’s next on your cloud adventure?",
		"Need a hand with your app or infra today?",
		"What’s cooking—deployments, logs, or maybe scaling?",
		"What can I help you ship today?",
		"How can I make your cloud life easier?",
		"Working on something exciting? Let's get it live.",
		"Want to check on a service, deploy something, or try something new?",
		"Let’s turn code into something live—what’s the plan?",
		"Your cloud assistant is ready. What’s on the agenda?",
		"Deploy. Debug. Discover. What’s your move?",
		"One terminal. Infinite possibility. What shall we do?",
		"Just me and you—what should we take care of today?",
		"Looking to deploy, inspect, or tweak something?",
		"Need insights, deployments, or just a friend in the cloud?",
		"What mission are we embarking on today?",
		"Want to push some code or peek under the hood?",
		"Cloud control is yours. What’s first?",
		"I’m all ears (and APIs). What’s the task?",
	}

	prompt := prompts[rand.Intn(len(prompts))]

	return prompt
}
