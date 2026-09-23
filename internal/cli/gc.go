package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/keep"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

func newGCCommand(d deps) *cobra.Command {
	var (
		dryRun bool
		grace  time.Duration
	)
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Delete blobs no kept revision uses",
		Long: `gc deletes blobs that no kept revision references (after 'weightkeep rm'),
lock files older than a day, and partial downloads abandoned for a week.

Blobs younger than --grace are left alone, so a pull running at the same
time doesn't lose files it stored before saving its manifest.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := d.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			res, err := a.keeper.GC(cmd.Context(), keep.GCOptions{Grace: grace, DryRun: dryRun})
			if err != nil {
				return err
			}
			verb := "removed"
			if dryRun {
				verb = "would remove"
			}
			msg := fmt.Sprintf("%s %d blob(s), %s", verb, len(res.Blobs), humanBytes(res.BlobBytes))
			if res.Tmp.Partials > 0 {
				msg += fmt.Sprintf("; %d abandoned partial(s), %s", res.Tmp.Partials, humanBytes(res.Tmp.Bytes))
			}
			if res.Protected > 0 {
				msg += fmt.Sprintf("; kept %d unreferenced blob(s) younger than %s", res.Protected, grace)
			}
			cmd.PrintErrln(msg)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed")
	cmd.Flags().DurationVar(&grace, "grace", keep.DefaultGrace, "keep unreferenced blobs younger than this")
	return cmd
}

func newRmCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "rm REPO@REVISION",
		Short: "Forget a kept revision",
		Long: `rm forgets a kept revision: its manifest and the refs pointing at it. Blobs
are shared between revisions, so their space is freed by the next 'gc'.`,
		Example: `  weightkeep rm prajjwal1/bert-tiny@main && weightkeep gc`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if !strings.Contains(args[0], "@") {
				return fmt.Errorf("name the revision: %s@main, or %s@<commit> (see 'weightkeep ls')", args[0], args[0])
			}
			a, err := d.openApp(ctx)
			if err != nil {
				return err
			}
			defer a.Close()
			sel, err := selectorFromArg(ctx, a, args[0])
			if err != nil {
				return err
			}
			if _, err := manifest.Load(ctx, a.store, sel.Repo, sel.Commit); err != nil {
				return err
			}
			if err := manifest.DeleteRefs(ctx, a.store, sel.Repo, sel.Commit); err != nil {
				return err
			}
			if err := manifest.Delete(ctx, a.store, sel.Repo, sel.Commit); err != nil {
				return err
			}
			cmd.PrintErrf("forgot %s@%s; run 'weightkeep gc' to free the space\n", sel.Repo, sel.Commit[:12])
			return nil
		},
	}
}
