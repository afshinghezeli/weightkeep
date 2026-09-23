// Package cli defines weightkeep's command-line interface.
//
// Commands parse flags, call into the internal packages and format output.
// They hold no logic of their own.
package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"
)

// Streams are where commands write. Data goes to Out, progress and hints to Err.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// NewRootCommand builds the command tree.
func NewRootCommand(s Streams) *cobra.Command {
	root := &cobra.Command{
		Use:   "weightkeep",
		Short: "Keep verified copies of the open-weight models you depend on",
		Long: `weightkeep keeps commit-pinned, hash-verified copies of model repositories
and serves them through a local endpoint that speaks the Hugging Face Hub API.

Point HF_ENDPOINT at 'weightkeep serve' and transformers, vLLM, llama.cpp and
the hf CLI keep working, whether or not the Hub still has the files.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(s.In)
	root.SetOut(s.Out)
	root.SetErr(s.Err)
	root.CompletionOptions.HiddenDefaultCmd = true

	root.AddCommand(newVersionCommand())
	return root
}

// Execute runs the CLI with args and returns the process exit code.
func Execute(ctx context.Context, s Streams, args []string) int {
	root := NewRootCommand(s)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		root.PrintErrln("weightkeep: " + err.Error())
		return 1
	}
	return 0
}
