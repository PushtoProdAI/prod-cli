package root

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/conduitio/ecdysis"
)

var (
	_ ecdysis.CommandWithFlags   = (*RootCommand)(nil)
	_ ecdysis.CommandWithExecute = (*RootCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*RootCommand)(nil)
	_ ecdysis.CommandWithOutput  = (*RootCommand)(nil)
	_ ecdysis.CommandWithArgs    = (*RootCommand)(nil)
)

const exitPrompt = "exit"

type RootFlags struct {
	Version bool `long:"version" short:"v" usage:"show the current Prod version"`
}

type RootArgs struct {
	prompt string
}

type RootCommand struct {
	flags  RootFlags
	args   RootArgs
	output ecdysis.Output
}

func (c *RootCommand) Args(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("too many arguments")
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
		c.output.Stdout(fmt.Sprintf("%s\n", "0.0.1"))
		return nil
	}

	c.output.Stdout(fmt.Sprintf("%s\n", banner))

	if c.args.prompt != "" {
		c.output.Stdout(fmt.Sprintf("DO SOMETHING WITH THIS PROMPT %s\n", c.args.prompt))
		return nil
	}

	// Create a context that can be canceled on SIGINT (Ctrl+C)
	ctxWithCancel, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	c.output.Stdout(fmt.Sprintf("Interactive mode. Type %q to exit.\n", exitPrompt))

	scanner := bufio.NewScanner(os.Stdin)

	for {
		// Check if we've been interrupted by Ctrl+C or if parent context was canceled
		select {
		case <-ctxWithCancel.Done():
			// Check if it was canceled by parent context or by signal
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.output.Stdout("\nInterrupted, exiting...\n")
			return nil
		default:
		}

		c.output.Stdout("> ")

		// Use a goroutine to handle scanning with context cancellation
		inputChan := make(chan string, 1)
		errChan := make(chan error, 1)

		go func() {
			if scanner.Scan() {
				inputChan <- scanner.Text()
			} else {
				if err := scanner.Err(); err != nil {
					errChan <- fmt.Errorf("error reading input: %w", err)
				} else {
					// EOF reached (e.g., Ctrl+D)
					inputChan <- "EOF"
				}
			}
		}()

		select {
		case <-ctxWithCancel.Done():
			// Check if it was canceled by parent context or by signal
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.output.Stdout("\nInterrupted, exiting...\n")
			return nil
		case err := <-errChan:
			return err
		case input := <-inputChan:
			if input == "EOF" {
				c.output.Stdout("\nEOF detected, exiting...\n")
				return nil
			}

			input = strings.TrimSpace(input)

			if input == "" {
				continue
			}

			if input == exitPrompt {
				c.output.Stdout("Exiting...\n")
				return nil
			}

			// Process the command here
			c.output.Stdout(fmt.Sprintf("You entered: %s\n", input))

			// You can add command processing logic here
			// For example, if you want to support different commands:
			// switch {
			// case strings.HasPrefix(input, "help"):
			//     displayHelp()
			// case strings.HasPrefix(input, "run"):
			//     runSomething()
			// }
		}
	}

	return nil
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
