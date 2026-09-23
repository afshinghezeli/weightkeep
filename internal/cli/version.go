package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/version"
)

func newVersionCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and Go version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			cmd.Println(info.String())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}
