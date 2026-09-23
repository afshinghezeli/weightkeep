package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/keep"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

func newVerifyCommand(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify [REPO[@REVISION]]",
		Short: "Re-hash kept files to detect corruption",
		Long: `Verify reads every kept file of the given revision (or of everything kept) and
checks it still matches its SHA-256. A blob shared by several revisions is
read once.

A corrupt blob is moved to quarantine/ in the store, so it is no longer
served; pull the revision again to fetch a good copy. Exits with status 1
if anything was corrupt or unreadable.`,
		Example: `  weightkeep verify
  weightkeep verify HuggingFaceTB/SmolLM2-135M@main`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, err := d.openApp(ctx)
			if err != nil {
				return err
			}
			defer a.Close()
			var sel keep.Selector
			if len(args) == 1 {
				if sel, err = selectorFromArg(ctx, a, args[0]); err != nil {
					return err
				}
			}

			type tally struct {
				files int
				bytes int64
				bad   int
			}
			tallies := map[string]*tally{}
			var order []string
			quarantined := map[string]string{}
			var bad int
			err = a.keeper.Verify(ctx, sel, func(r keep.VerifyResult) {
				key := r.Repo.String() + "@" + r.Commit
				t, ok := tallies[key]
				if !ok {
					t = &tally{}
					tallies[key] = t
					order = append(order, key)
				}
				t.files++
				t.bytes += r.File.Size
				if r.Err == nil {
					return
				}
				t.bad++
				bad++
				if !keep.IsCorrupt(r.Err) {
					fmt.Fprintf(cmd.OutOrStdout(), "UNREADABLE  %s  %s: %v\n", short(key), r.File.Path, r.Err)
					return
				}
				if _, done := quarantined[r.File.SHA256]; !done {
					moved, qerr := a.store.Quarantine(r.File.SHA256)
					if qerr != nil {
						moved = "not moved: " + qerr.Error()
					}
					quarantined[r.File.SHA256] = moved
				}
				fmt.Fprintf(cmd.OutOrStdout(), "CORRUPT     %s  %s (moved to %s)\n", short(key), r.File.Path, quarantined[r.File.SHA256])
			})
			if errors.Is(err, manifest.ErrNotFound) && len(args) == 0 {
				cmd.PrintErrln("nothing kept yet")
				return nil
			}
			if err != nil {
				return err
			}
			for _, key := range order {
				if t := tallies[key]; t.bad == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "ok          %s  %d files, %s\n", short(key), t.files, humanBytes(t.bytes))
				}
			}
			if bad > 0 {
				return fmt.Errorf("%d file(s) failed verification; pull the affected revisions again", bad)
			}
			return nil
		},
	}
	return cmd
}

// short trims "repo@<40 hex>" to a 12-character commit.
func short(key string) string {
	if len(key) > 28 {
		return key[:len(key)-28]
	}
	return key
}
