package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/keep"
)

func newPullCommand(d deps) *cobra.Command {
	var (
		revision         string
		include, exclude []string
		parallel         int
	)
	cmd := &cobra.Command{
		Use:   "pull REPO[@REVISION]",
		Short: "Download a model revision into the store and verify it",
		Long: `Pull resolves a branch, tag or commit to a commit id, downloads every file of
that revision into the store, and checks each one against the hash the Hub
lists for it. Interrupted downloads resume where they stopped.

Small files (configs, tokenizer, README, licence) are always kept. --include
and --exclude choose which large files to download; patterns use the same
syntax as huggingface_hub's allow_patterns ("*" also matches "/").

Pulling a revision that is already kept only asks the Hub where the branch
points now. Pulling a pinned commit that is already kept needs no network.`,
		Example: `  weightkeep pull HuggingFaceTB/SmolLM2-135M
  weightkeep pull bartowski/SmolLM2-135M-Instruct-GGUF --include '*Q4_K_M*'
  weightkeep pull openai-community/gpt2@607a30d783dfa663caf39e06633721c8d4cfcd7e`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, rev, _ := strings.Cut(args[0], "@")
			if revision != "" {
				if rev != "" {
					return fmt.Errorf("give the revision either after @ or with --revision, not both")
				}
				rev = revision
			}
			repo, err := hub.ParseRepo(spec)
			if err != nil {
				return err
			}

			a, err := d.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			if parallel > 0 {
				a.keeper.Fetcher.Parallel = parallel
			}
			prog := newProgress(cmd.ErrOrStderr(), "pulling "+repo.String())
			a.keeper.Fetcher.Events = prog.event

			res, err := a.keeper.Pull(cmd.Context(), keep.PullRequest{
				Repo: repo, Revision: rev, Include: include, Exclude: exclude,
			})
			prog.finish()
			if err != nil {
				return explain(err, repo.String())
			}
			printPullSummary(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&revision, "revision", "", "branch, tag or commit (default main)")
	cmd.Flags().StringArrayVar(&include, "include", nil, "only download large files matching this glob (repeatable)")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "skip large files matching this glob (repeatable)")
	cmd.Flags().IntVar(&parallel, "parallel", 0, "files to download at once (default 4)")
	return cmd
}

func printPullSummary(cmd *cobra.Command, res *keep.PullResult) {
	m := res.Manifest
	var kept int64
	keptFiles := len(res.Downloaded) + len(res.Present)
	skipped := map[string]bool{}
	for _, p := range res.Skipped {
		skipped[p] = true
	}
	for _, f := range m.Files {
		if !skipped[f.Path] {
			kept += f.Size
		}
	}
	parts := []string{fmt.Sprintf("%d downloaded", len(res.Downloaded))}
	if len(res.Present) > 0 {
		parts = append(parts, fmt.Sprintf("%d already kept", len(res.Present)))
	}
	if len(res.Skipped) > 0 {
		parts = append(parts, fmt.Sprintf("%d left out by filters", len(res.Skipped)))
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "kept %s@%s: %d files, %s (%s)\n",
		m.Repo, m.Commit[:12], keptFiles, humanBytes(kept), strings.Join(parts, ", "))
}
