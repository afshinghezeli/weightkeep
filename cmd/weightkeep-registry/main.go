// Command weightkeep-registry builds and signs the community registry. It is
// for registry maintainers and the registry repo's CI; users only need
// 'weightkeep registry sync'. See docs/registry.md.
//
// The registry repo holds the sources:
//
//	records/models/<org>/<name>/<commit>.json   one record per revision
//	denylist.json
//
// and 'build' turns them into a static TUF repository under --out.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/registry"
	"github.com/afshinghezeli/weightkeep/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRoot().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "weightkeep-registry:", err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "weightkeep-registry",
		Short:         "Build and sign the weightkeep community registry",
		Version:       version.Get().String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newInit(), newCheck(), newBuild(), newTimestamp())
	return root
}

func newInit() *cobra.Command {
	var keys, out string
	var rootKeys, threshold int
	cmd := &cobra.Command{
		Use:   "init --keys DIR --out DIR",
		Short: "Create signing keys and the first root metadata",
		Long: `init generates one ECDSA P-256 key per role (--root-keys for root) into
--keys, and writes metadata/1.root.json and root.json into --out.

1.root.json is what clients trust: publish it, and point registry.root at a
copy of it. Keep the root keys offline. The targets, snapshot and timestamp
keys go into the registry repo's CI secrets.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if keys == "" || out == "" {
				return errors.New("--keys and --out are required")
			}
			k, err := registry.GenerateKeys(rootKeys)
			if err != nil {
				return err
			}
			if err := registry.WriteKeys(keys, k); err != nil {
				return err
			}
			if err := registry.Init(out, k, threshold, time.Now().UTC()); err != nil {
				return err
			}
			cmd.PrintErrf("wrote keys to %s and %s\n", keys, filepath.Join(out, "metadata", "1.root.json"))
			return nil
		},
	}
	cmd.Flags().StringVar(&keys, "keys", "", "directory for the new private keys (must not contain keys)")
	cmd.Flags().StringVar(&out, "out", "", "the published registry directory")
	cmd.Flags().IntVar(&rootKeys, "root-keys", 1, "how many root keys to create")
	cmd.Flags().IntVar(&threshold, "threshold", 1, "root signatures needed to change the root")
	return cmd
}

func newCheck() *cobra.Command {
	var hubURL, src string
	cmd := &cobra.Command{
		Use:   "check [--src DIR] RECORD...",
		Short: "Check submitted records against the Hub",
		Long: `check verifies each record file: its path matches the revision it describes,
the Hub serves exactly the files and hashes it lists, the repo is neither
gated nor private, its licence is tier A or B, and neither the repo nor any
file is on the denylist. The registry repo's CI runs it on every pull request
for the records the PR adds or changes.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := hub.New(hubURL, hub.Options{})
			if err != nil {
				return err
			}
			deny, err := readDenylist(filepath.Join(src, "denylist.json"))
			if err != nil {
				return err
			}
			var failed int
			for _, p := range args {
				if err := checkOne(cmd, c, deny, src, p); err != nil {
					failed++
					fmt.Fprintf(cmd.OutOrStdout(), "FAIL  %s: %v\n", p, err)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d record(s) failed", failed, len(args))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&hubURL, "hub", "https://huggingface.co", "the Hub to check against")
	cmd.Flags().StringVar(&src, "src", ".", "the registry repo checkout")
	return cmd
}

func newBuild() *cobra.Command {
	var keys, src, out string
	cmd := &cobra.Command{
		Use:   "build --keys DIR --src DIR --out DIR",
		Short: "Sign the records and denylist into a TUF repository",
		Long: `build reads every record under --src/records and --src/denylist.json, writes
them as targets under --out/targets, and signs new targets, snapshot and
timestamp metadata with the keys in --keys. --out must already hold the
root metadata (from init) and, after the first build, the previously
published metadata, so versions keep increasing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if keys == "" || src == "" || out == "" {
				return errors.New("--keys, --src and --out are required")
			}
			k, err := registry.ReadKeys(keys)
			if err != nil {
				return err
			}
			deny, err := readDenylist(filepath.Join(src, "denylist.json"))
			if err != nil {
				return err
			}
			var records []*registry.Record
			dir := filepath.Join(src, "records")
			err = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
				if err != nil || e.IsDir() || !strings.HasSuffix(p, ".json") {
					return err
				}
				r, err := readRecord(src, p)
				if err != nil {
					return err
				}
				records = append(records, r)
				return nil
			})
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := registry.Build(registry.BuildInput{Out: out, Records: records, Denylist: deny, Keys: k, Now: time.Now().UTC()}); err != nil {
				return err
			}
			cmd.PrintErrf("built %s: %d record(s), %d denylist entr(ies)\n", out, len(records), len(deny.Entries))
			return nil
		},
	}
	cmd.Flags().StringVar(&keys, "keys", "", "directory holding the targets, snapshot and timestamp keys")
	cmd.Flags().StringVar(&src, "src", ".", "the registry repo checkout")
	cmd.Flags().StringVar(&out, "out", "", "the published registry directory")
	return cmd
}

func newTimestamp() *cobra.Command {
	var keys, out string
	cmd := &cobra.Command{
		Use:   "timestamp --keys DIR --out DIR",
		Short: "Re-sign timestamp.json so the registry doesn't expire",
		Long: `timestamp re-signs the timestamp metadata for the current snapshot. It
expires after seven days, so a mirror can't keep serving an old registry
(and an old denylist) for long. Run it daily from CI.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if keys == "" || out == "" {
				return errors.New("--keys and --out are required")
			}
			k, err := registry.ReadKeys(keys)
			if err != nil {
				return err
			}
			return registry.Timestamp(out, k, time.Now().UTC())
		},
	}
	cmd.Flags().StringVar(&keys, "keys", "", "directory holding the timestamp key")
	cmd.Flags().StringVar(&out, "out", "", "the published registry directory")
	return cmd
}

func checkOne(cmd *cobra.Command, c *hub.Client, deny *registry.Denylist, src, p string) error {
	r, err := readRecord(src, p)
	if err != nil {
		return err
	}
	d, err := registry.Check(cmd.Context(), c, r, deny)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "ok    %s  tier %s (%s), %d files\n", p, d.Tier, d.License, len(r.Manifest.Files))
	return nil
}

// readRecord parses a record file and checks it sits where its revision
// belongs, records/<type>s/<org>/<name>/<commit>.json.
func readRecord(src, p string) (*registry.Record, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	r, err := registry.ParseRecord(data)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(filepath.Join(src, "records"), p)
	if err != nil {
		return nil, err
	}
	if want := registry.RecordTarget(r.Manifest.Repo, r.Manifest.Commit); filepath.ToSlash(rel) != want {
		return nil, fmt.Errorf("%s describes %s@%s and belongs at records/%s", p, r.Manifest.Repo, r.Manifest.Commit, want)
	}
	return r, nil
}

func readDenylist(p string) (*registry.Denylist, error) {
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return &registry.Denylist{Version: registry.FormatVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var d registry.Denylist
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if err := d.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &d, nil
}
