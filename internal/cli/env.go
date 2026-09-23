package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newEnvCommand(d deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Print the resolved configuration",
		Long: `Print where weightkeep keeps its data, which Hub it uses as upstream, and
where it found a Hugging Face token. The token itself is never printed.

Settings come from environment variables first, then the config file, then
defaults. Include this output in bug reports.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := d.loadConfig()
			if err != nil {
				return err
			}
			configFile := c.ConfigFile
			if _, err := os.Stat(configFile); errors.Is(err, fs.ErrNotExist) {
				configFile += " (not present)"
			}
			token := "none"
			if !c.Token.IsZero() {
				token = fmt.Sprintf("%s (from %s)", c.Token, c.TokenSource)
			}
			rows := []struct{ key, val string }{
				{"home", c.Home},
				{"config_file", configFile},
				{"upstream", c.Upstream},
				{"serve_addr", c.ServeAddr},
				{"hf_home", c.HFHome},
				{"hf_hub_cache", c.HFHubCache},
				{"token", token},
			}
			if asJSON {
				m := make(map[string]string, len(rows))
				for _, r := range rows {
					m[r.key] = r.val
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(m)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, r := range rows {
				fmt.Fprintf(tw, "%s\t%s\n", r.key, r.val)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}
