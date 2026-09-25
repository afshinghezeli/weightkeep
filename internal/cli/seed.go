package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/keep"
	"github.com/afshinghezeli/weightkeep/internal/policy"
	wktorrent "github.com/afshinghezeli/weightkeep/internal/torrent"
	"github.com/afshinghezeli/weightkeep/internal/version"
)

func newSeedCommand(d deps) *cobra.Command {
	var (
		allow         []string
		nonCommercial bool
		offline       bool
		dryRun        bool
		noDHT         bool
		port          int
		uploadRate    string
		monthlyCap    string
	)
	cmd := &cobra.Command{
		Use:   "seed [REPO[@REVISION]]...",
		Short: "Share kept revisions over BitTorrent, where their licence allows",
		Long: `seed shares fully kept revisions with other people over BitTorrent, straight
from the store. Each torrent lists the Hub as a web seed, so downloaders can
fall back to it while it still has the files.

Only licences that allow redistribution are shared (see 'weightkeep license'):

  A   shared by default
  B1  only with --allow REPO, after you have read the licence
  B2  only with --allow REPO and --non-commercial
  C   never: gated, private, unknown or unlicensed repos

Every revision is listed with the reason it is or isn't shared. Magnet links
of shared revisions are printed on stdout. Runs until interrupted.`,
		Example: `  weightkeep seed --dry-run
  weightkeep seed --upload-rate 10MB --monthly-cap 2TB
  weightkeep seed some-org/llama-finetune --allow some-org/llama-finetune`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			rate, err := parseSize(uploadRate)
			if err != nil {
				return fmt.Errorf("--upload-rate: %w", err)
			}
			limit, err := parseSize(monthlyCap)
			if err != nil {
				return fmt.Errorf("--monthly-cap: %w", err)
			}
			a, err := d.openApp(ctx)
			if err != nil {
				return err
			}
			defer a.Close()
			if err := a.useDenylist(cmd, !offline, true); err != nil {
				return fmt.Errorf("%w\nseeding needs a current denylist; run `weightkeep registry sync`", err)
			}

			var cands []keep.SeedCandidate
			opt := keep.SeedOptions{Allow: allow, NonCommercial: nonCommercial, Online: !offline}
			if len(args) == 0 {
				if cands, err = a.keeper.SeedPlan(ctx, keep.Selector{}, opt); err != nil {
					return err
				}
			}
			for _, arg := range args {
				sel, err := selectorFromArg(ctx, a, withDefaultRev(arg))
				if err != nil {
					return err
				}
				cs, err := a.keeper.SeedPlan(ctx, sel, opt)
				if err != nil {
					return err
				}
				cands = append(cands, cs...)
			}
			errw := cmd.ErrOrStderr()
			var seeding []keep.SeedCandidate
			for _, c := range cands {
				name := c.Manifest.Repo.String() + "@" + c.Manifest.Commit[:12]
				if c.Seed {
					fmt.Fprintf(errw, "share  %s  (tier %s, %s)\n", name, c.Decision.Tier, c.Decision.License)
					seeding = append(seeding, c)
				} else {
					fmt.Fprintf(errw, "skip   %s  %s\n", name, c.Why)
				}
			}
			if len(seeding) == 0 {
				return fmt.Errorf("nothing to share")
			}
			if dryRun {
				return nil
			}

			client, err := wktorrent.NewClient(a.store, wktorrent.ClientConfig{
				DataDir:    filepath.Join(a.cfg.Home, "torrent-client"),
				ListenPort: port,
				NoDHT:      noDHT,
				UploadRate: int(rate),
				// This node only uploads; it never needs the web seeds.
				NoWebSeeds: true,
			})
			if err != nil {
				return err
			}
			defer client.Close()

			var torrents []*torrent.Torrent
			for _, c := range seeding {
				meta, err := buildTorrent(a, c)
				if err != nil {
					fmt.Fprintf(errw, "skip   %s@%s  %v\n", c.Manifest.Repo, c.Manifest.Commit[:12], err)
					continue
				}
				t, err := client.Seed(c.Manifest, meta)
				if err != nil {
					fmt.Fprintf(errw, "skip   %s@%s  %v\n", c.Manifest.Repo, c.Manifest.Commit[:12], err)
					continue
				}
				torrents = append(torrents, t)
				mi, err := metainfo.Load(bytes.NewReader(meta))
				if err != nil {
					return err
				}
				magnet, err := mi.MagnetV2()
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", c.Manifest.Repo, magnet.String())
			}
			if len(torrents) == 0 {
				return fmt.Errorf("nothing could be shared")
			}
			fmt.Fprintf(errw, "sharing %d revision(s) on port %d; Ctrl-C to stop\n", len(torrents), client.Addr().(*net.TCPAddr).Port)
			return watchUploads(ctx, a, torrents, limit, errw)
		},
	}
	cmd.Flags().StringArrayVar(&allow, "allow", nil, "opt in to sharing this tier B repo (repeatable)")
	cmd.Flags().BoolVar(&nonCommercial, "non-commercial", false, "state that this node shares for non-commercial purposes (needed for tier B2)")
	cmd.Flags().BoolVar(&offline, "offline", false, "don't look up base models on the Hub (they then count as unknown)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be shared and stop")
	cmd.Flags().BoolVar(&noDHT, "no-dht", false, "don't use the DHT (peers must be added by hand)")
	cmd.Flags().IntVar(&port, "port", 6881, "BitTorrent listen port")
	cmd.Flags().StringVar(&uploadRate, "upload-rate", "0", "upload limit per second, e.g. 10MB (0: none)")
	cmd.Flags().StringVar(&monthlyCap, "monthly-cap", "0", "stop uploading after this much per calendar month, e.g. 2TB (0: none)")
	return cmd
}

// buildTorrent builds (or loads) the revision's torrent: v1 with padding
// (ADR 0010), the Hub as web seed, and the licence text when weightkeep
// supplies it (ADR 0009).
func buildTorrent(a *app, c keep.SeedCandidate) ([]byte, error) {
	m := c.Manifest
	opts := wktorrent.Options{
		V1Only:    true,
		WebSeeds:  []string{a.cfg.Upstream + "/" + m.Repo.String() + "/resolve/"},
		Comment:   "weightkeep: " + m.Repo.String() + "@" + m.Commit,
		CreatedBy: "weightkeep " + version.Get().Version,
	}
	if c.Decision.SuppliedText != "" {
		text, err := policy.Text(c.Decision.SuppliedText)
		if err != nil {
			return nil, err
		}
		opts.Extra = map[string]any{"weightkeep.license": map[string]any{
			"spdx": c.Decision.SuppliedText, "text": text, "repo": m.Repo.String(),
		}}
	}
	return wktorrent.ForRevision(a.store, m, opts)
}

// watchUploads records uploaded bytes per month and stops uploading once
// the monthly cap is reached, until the next month.
func watchUploads(ctx context.Context, a *app, ts []*torrent.Torrent, limit int64, errw io.Writer) error {
	var last int64
	paused := false
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		var now int64
		peers := 0
		for _, t := range ts {
			st := t.Stats()
			now += st.BytesWrittenData.Int64()
			peers += st.ActivePeers
		}
		month := keep.Month(time.Now())
		total, err := keep.AddUploaded(ctx, a.store, month, now-last)
		if err != nil {
			return err
		}
		last = now
		fmt.Fprintf(errw, "uploaded %s this session, %s this month, %d peer(s)\n", humanBytes(now), humanBytes(total), peers)
		over := limit > 0 && total >= limit
		if over != paused {
			for _, t := range ts {
				if over {
					t.DisallowDataUpload()
				} else {
					t.AllowDataUpload()
				}
			}
			paused = over
			if over {
				fmt.Fprintf(errw, "monthly cap of %s reached; uploads paused until next month\n", humanBytes(limit))
			}
		}
	}
}

// parseSize reads "0", "500", "10MB", "1.5GB", "2TiB" as bytes.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	units := []struct {
		suffix string
		mult   float64
	}{
		{"TIB", 1 << 40}, {"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"B", 1},
	}
	mult := 1.0
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			s, mult = strings.TrimSpace(num), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("%q is not a size like 10MB", s)
	}
	return int64(f * mult), nil
}
