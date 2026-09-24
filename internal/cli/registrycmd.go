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

// useDenylist loads the registry's denylist into the keeper. With sync, the
// registry is refreshed first; if that fails, the last synced copy is used
// while it hasn't expired. required makes a missing or expired denylist an
// error rather than a warning. No registry configured means no denylist.
func (a *app) useDenylist(cmd *cobra.Command, sync, required bool) error {
	rc, err := a.openRegistry()
	if err != nil {
		return err
	}
	if rc == nil {
		cmd.PrintErrln("note: no registry configured, so the denylist isn't checked (see docs/registry.md)")
		return nil
	}
	if sync {
		if err := rc.Sync(); err != nil {
			cmd.PrintErrf("warning: %v; using the last synced registry\n", err)
		}
	}
	deny, err := rc.Denylist()
	if err != nil {
		if required {
			return err
		}
		cmd.PrintErrf("warning: the denylist isn't checked: %v\n", err)
		return nil
	}
	a.keeper.Deny = deny
	return nil
}

// useRegistry lets pull check upstream against the registry. The registry
// is synced first if the local copy is missing or expired; if that fails,
// pull goes ahead without the checks.
func (a *app) useRegistry(cmd *cobra.Command) {
	rc, err := a.openRegistry()
	if err != nil {
		cmd.PrintErrf("warning: registry checks skipped: %v\n", err)
		return
	}
	if rc == nil {
		return
	}
	if _, err := rc.Size(); err != nil {
		if err := rc.Sync(); err != nil {
			cmd.PrintErrf("warning: registry checks skipped: %v\n", err)
			return
		}
	}
	a.keeper.Registry = registryView{rc}
}

// registryView adapts the registry client to keep.Registry.
type registryView struct{ c *registry.Client }

func (v registryView) Record(repo manifest.Repo, commit string) (*manifest.Manifest, string, bool, error) {
	r, err := v.c.Record(repo, commit)
	if errors.Is(err, registry.ErrNotListed) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	return r.Manifest, r.Magnet, true, nil
}

func (v registryView) Latest(repo manifest.Repo) (*manifest.Manifest, string, bool, error) {
	commits, err := v.c.Commits(repo)
	if err != nil {
		return nil, "", false, err
	}
	var latest *registry.Record
	for _, c := range commits {
		r, err := v.c.Record(repo, c)
		if err != nil {
			return nil, "", false, err
		}
		if latest == nil || r.Added.After(latest.Added) {
			latest = r
		}
	}
	if latest == nil {
		return nil, "", false, nil
	}
	return latest.Manifest, latest.Magnet, true, nil
}
