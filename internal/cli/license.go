package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

func newLicenseCommand(d deps) *cobra.Command {
	var asJSON, offline bool
	cmd := &cobra.Command{
		Use:     "license REPO[@REVISION]",
		Aliases: []string{"licence"},
		Short:   "Show whether a kept revision may be shared, and why",
		Long: `license shows the sharing tier weightkeep assigns to a kept revision and the
reasons for it. Keeping and serving a model for your own use is never
restricted; the tier only decides what 'weightkeep seed' may share.

  A   permissive (Apache-2.0, MIT, CC-BY, ...): shared by default
  B1  conditions apply (Llama, Gemma, OpenRAIL, ...): opt in per model
  B2  non-commercial only: opt in, non-commercial operators only
  C   never shared: gated, private, unknown or unlicensed

Base models listed in the model card are looked up on the Hub; with
--offline they count as unknown.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, err := d.openApp(ctx)
			if err != nil {
				return err
			}
			defer a.Close()
			sel, err := selectorFromArg(ctx, a, withDefaultRev(args[0]))
			if err != nil {
				return err
			}
			m, err := manifest.Load(ctx, a.store, sel.Repo, sel.Commit)
			if err != nil {
				return err
			}
			dec, err := a.keeper.Licence(ctx, m, !offline)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"repo": m.Repo.ID, "commit": m.Commit, "tier": dec.Tier.String(), "license": dec.License,
					"spdx": dec.SPDX, "supplied_text": dec.SuppliedText, "reasons": dec.Reasons, "notes": dec.Notes,
				})
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s@%s\n", m.Repo, m.Commit[:12])
			lic := dec.License
			if lic == "" {
				lic = "none declared"
			}
			fmt.Fprintf(out, "  licence  %s\n", lic)
			fmt.Fprintf(out, "  tier     %s: %s\n", dec.Tier, dec.Tier.Summary())
			for _, r := range dec.Reasons {
				fmt.Fprintf(out, "  because  %s\n", r)
			}
			for _, n := range dec.Notes {
				fmt.Fprintf(out, "  note     %s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	cmd.Flags().BoolVar(&offline, "offline", false, "don't look up base models on the Hub")
	return cmd
}

// withDefaultRev makes "org/repo" mean "org/repo@main".
func withDefaultRev(arg string) string {
	if strings.Contains(arg, "@") {
		return arg
	}
	return arg + "@main"
}
