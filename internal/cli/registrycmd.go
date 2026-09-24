package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/registry"
)

// openRegistry returns the configured registry client, or nil if none is
// configured.
func (a *app) openRegistry() (*registry.Client, error) {
	if a.cfg.RegistryURL == "" {
		return nil, nil
	}
	if a.cfg.RegistryRoot == "" {
		return nil, errors.New("registry.url is set but registry.root isn't: point it at the registry's trusted root.json")
	}
	root, err := os.ReadFile(a.cfg.RegistryRoot)
	if err != nil {
		return nil, fmt.Errorf("registry root: %w", err)
	}
	return registry.Open(a.cfg.RegistryURL, root, filepath.Join(a.cfg.Home, "registry"))
}

func newRegistryCommand(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Use the community registry of revision records and the denylist",
		Long: `The registry is a signed list of revisions (their manifests and magnet links)
and a denylist, published as a TUF repository so any mirror of it can be
checked. Configure it with registry.url and registry.root in the config file,
or WEIGHTKEEP_REGISTRY_URL and WEIGHTKEEP_REGISTRY_ROOT.

With a synced registry, pull cross-checks what the Hub serves against the
registry's record, pull falls back to the record's magnet link when neither
the Hub nor any mirror has a revision, and seed honours the denylist.`,
	}
	cmd.AddCommand(newRegistrySyncCommand(d), newRegistryShowCommand(d), newRegistryRecordCommand(d))
	return cmd
}

func newRegistrySyncCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Fetch and verify the latest registry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := d.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			rc, err := a.openRegistry()
			if err != nil {
				return err
			}
			if rc == nil {
				return errors.New("no registry configured (registry.url and registry.root)")
			}
			if err := rc.Sync(); err != nil {
				return err
			}
			n, _ := rc.Size()
			deny, err := rc.Denylist()
			if err != nil {
				return err
			}
			cmd.PrintErrf("registry synced: %d revision record(s), %d denylist entr(ies)\n", n, len(deny.Entries))
			return nil
		},
	}
}

func newRegistryShowCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show REPO[@COMMIT]",
		Short: "Show the registry's records for a repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := d.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			rc, err := a.openRegistry()
			if err != nil || rc == nil {
				return errors.Join(err, errors.New("no registry configured"))
			}
			spec, commit, _ := cutAt(args[0])
			repo, err := hub.ParseRepo(spec)
			if err != nil {
				return err
			}
			mrepo := manifest.Repo{Type: string(repo.Type), ID: repo.ID}
			commits := []string{commit}
			if commit == "" {
				if commits, err = rc.Commits(mrepo); err != nil {
					return err
				}
			}
			if len(commits) == 0 {
				return fmt.Errorf("the registry has no records for %s", mrepo)
			}
			for _, c := range commits {
				r, err := rc.Record(mrepo, c)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s@%s  %d files, %s  added %s\n", mrepo, c[:12], len(r.Manifest.Files),
					humanBytes(r.Manifest.Size()), r.Added.Format("2006-01-02"))
				if r.Magnet != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", r.Magnet)
				}
			}
			return nil
		},
	}
}

func newRegistryRecordCommand(d deps) *cobra.Command {
	var magnet string
	cmd := &cobra.Command{
		Use:   "record REPO[@REVISION] [--magnet MAGNET]",
		Short: "Print a registry record for a kept revision, to submit",
		Long: `record prints the JSON record the registry stores for a kept revision: its
manifest and, if you seed it, the magnet link 'weightkeep seed' printed.
Submit it to the registry repository as a pull request; maintainers check it
against the Hub before it's signed into the registry.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := d.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			m, err := loadRevision(cmd, a, args[0])
			if err != nil {
				return err
			}
			r := &registry.Record{Version: registry.FormatVersion, Manifest: m, Magnet: magnet, Added: time.Now().UTC().Truncate(time.Second)}
			if err := r.Validate(); err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(r)
		},
	}
	cmd.Flags().StringVar(&magnet, "magnet", "", "the revision's magnet link, from 'weightkeep seed'")
	return cmd
}

func cutAt(s string) (string, string, bool) {
	for i := range s {
		if s[i] == '@' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
