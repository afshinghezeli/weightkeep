// Package cli defines weightkeep's command-line interface.
//
// Commands parse flags, call into the internal packages and format output.
// They hold no logic of their own.
package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/config"
)

// Streams are where commands write. Data goes to Out, progress and hints to Err.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// deps are the things commands need from the outside world. Tests replace them.
type deps struct {
	loadConfig func() (*config.Config, error)
}

func defaultDeps() deps {
	return deps{
		loadConfig: func() (*config.Config, error) {
			env, err := config.OSEnv()
			if err != nil {
				return nil, err
			}
			return config.Load(env)
		},
	}
}

// NewRootCommand builds the command tree.
func NewRootCommand(s Streams) *cobra.Command {
	return newRoot(s, defaultDeps())
}

func newRoot(s Streams, d deps) *cobra.Command {
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

	root.AddCommand(
		newPullCommand(d),
		newLsCommand(d),
		newVerifyCommand(d),
		newServeCommand(d),
		newSeedCommand(d),
		newExportCommand(d),
		newLicenseCommand(d),
		newRmCommand(d),
		newGCCommand(d),
		newEnvCommand(d),
		newVersionCommand(),
	)
	return root
}

// Execute runs the CLI with args and returns the process exit code.
func Execute(ctx context.Context, s Streams, args []string) int {
	return execute(ctx, s, defaultDeps(), args)
}

func execute(ctx context.Context, s Streams, d deps, args []string) int {
	root := newRoot(s, d)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		root.PrintErrln("weightkeep: " + err.Error())
		return 1
	}
	return 0
}
