package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/keep"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

func newLsCommand(d deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List kept revisions",
		Long: `List every kept revision with how much of it is in the store. A revision
pulled with --include shows fewer kept files than it has.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := d.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			sums, err := manifest.List(cmd.Context(), a.store)
			if err != nil {
				return err
			}
			if asJSON {
				return writeLsJSON(cmd, sums)
			}
			if len(sums) == 0 {
				cmd.PrintErrln("nothing kept yet; try `weightkeep pull HuggingFaceTB/SmolLM2-135M`")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "REPO\tCOMMIT\tFILES\tSIZE\tFETCHED\tVERIFIED")
			for _, s := range sums {
				size := humanBytes(s.KeptSize)
				if s.KeptSize != s.Size {
					size += " of " + humanBytes(s.Size)
				}
				fmt.Fprintf(tw, "%s\t%s\t%d/%d\t%s\t%s\t%s\n", s.Repo, s.Commit[:12], s.KeptFiles, s.Files,
					size, day(s.FetchedAt), day(s.VerifiedAt))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func day(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02")
}

func writeLsJSON(cmd *cobra.Command, sums []manifest.Summary) error {
	type row struct {
		Repo       string     `json:"repo"`
		Type       string     `json:"type"`
		Commit     string     `json:"commit"`
		Files      int        `json:"files"`
		Size       int64      `json:"size"`
		KeptFiles  int        `json:"kept_files"`
		KeptSize   int64      `json:"kept_size"`
		FetchedAt  time.Time  `json:"fetched_at"`
		VerifiedAt *time.Time `json:"verified_at,omitempty"`
	}
	rows := make([]row, 0, len(sums))
	for _, s := range sums {
		r := row{Repo: s.Repo.ID, Type: s.Repo.Type, Commit: s.Commit, Files: s.Files, Size: s.Size,
			KeptFiles: s.KeptFiles, KeptSize: s.KeptSize, FetchedAt: s.FetchedAt}
		if !s.VerifiedAt.IsZero() {
			v := s.VerifiedAt
			r.VerifiedAt = &v
		}
		rows = append(rows, r)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

// selectorFromArg turns "REPO[@REV]" into a Selector, resolving branch
// names from the local refs table (no network).
func selectorFromArg(ctx context.Context, a *app, arg string) (keep.Selector, error) {
	spec, rev, _ := strings.Cut(arg, "@")
	repo, err := hub.ParseRepo(spec)
	if err != nil {
		return keep.Selector{}, err
	}
	sel := keep.Selector{Repo: manifest.Repo{Type: string(repo.Type), ID: repo.ID}}
	if rev != "" {
		commit, _, err := manifest.ResolveRef(ctx, a.store, sel.Repo, rev)
		if err != nil {
			return keep.Selector{}, err
		}
		sel.Commit = commit
	}
	return sel, nil
}
