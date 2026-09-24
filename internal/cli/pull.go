package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/keep"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
	wktorrent "github.com/afshinghezeli/weightkeep/internal/torrent"
)

func newPullCommand(d deps) *cobra.Command {
	var (
		revision         string
		include, exclude []string
		parallel         int
		torrentSrc       string
		peers            []string
	)
	cmd := &cobra.Command{
		Use:   "pull REPO[@REVISION]",
		Short: "Download a model revision into the store and verify it",
		Long: `Pull resolves a branch, tag or commit to a commit id, downloads every file of
that revision into the store, and checks each one against the hash the Hub
lists for it. Interrupted downloads resume where they stopped.

Small files (configs, tokenizer, README, licence) are always kept. --include
and --exclude choose which large files to download; patterns use the same
syntax as huggingface_hub's allow_patterns ("*" also matches "/").

Pulling a revision that is already kept only asks the Hub where the branch
points now. Pulling a pinned commit that is already kept needs no network.

--torrent pulls from other weightkeep nodes over BitTorrent instead of the
Hub, from a .torrent file or a magnet link printed by 'weightkeep seed'. The
torrent carries the revision's manifest, covered by its info hash, and every
file is checked against it. The Hub isn't contacted, so this works when the
Hub no longer has the repo.`,
		Example: `  weightkeep pull HuggingFaceTB/SmolLM2-135M
  weightkeep pull bartowski/SmolLM2-135M-Instruct-GGUF --include '*Q4_K_M*'
  weightkeep pull openai-community/gpt2@607a30d783dfa663caf39e06633721c8d4cfcd7e
  weightkeep pull --torrent 'magnet:?xt=urn:btih:...' --peer 192.168.1.20:6881`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if torrentSrc != "" {
				return pullTorrent(cmd, d, torrentSrc, peers, args)
			}
			if len(args) != 1 {
				return fmt.Errorf("name the repo to pull, e.g. weightkeep pull HuggingFaceTB/SmolLM2-135M")
			}
			spec, rev, _ := strings.Cut(args[0], "@")
			if revision != "" {
				if rev != "" {
					return fmt.Errorf("give the revision either after @ or with --revision, not both")
				}
				rev = revision
			}
			repo, err := hub.ParseRepo(spec)
			if err != nil {
				return err
			}

			a, err := d.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			if parallel > 0 {
				a.keeper.Fetcher.Parallel = parallel
			}
			prog := newProgress(cmd.ErrOrStderr(), "pulling "+repo.String())
			a.keeper.Fetcher.Events = prog.event

			res, err := a.keeper.Pull(cmd.Context(), keep.PullRequest{
				Repo: repo, Revision: rev, Include: include, Exclude: exclude,
			})
			prog.finish()
			if err != nil {
				return explain(err, repo.String())
			}
			printPullSummary(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&revision, "revision", "", "branch, tag or commit (default main)")
	cmd.Flags().StringArrayVar(&include, "include", nil, "only download large files matching this glob (repeatable)")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "skip large files matching this glob (repeatable)")
	cmd.Flags().IntVar(&parallel, "parallel", 0, "files to download at once (default 4)")
	cmd.Flags().StringVar(&torrentSrc, "torrent", "", "pull from a .torrent file or magnet link made by 'weightkeep seed'")
	cmd.Flags().StringArrayVar(&peers, "peer", nil, "a node to fetch from, host:port (repeatable; with --torrent)")
	return cmd
}

func pullTorrent(cmd *cobra.Command, d deps, src string, peerArgs []string, args []string) error {
	ctx := cmd.Context()
	var source wktorrent.Source
	if strings.HasPrefix(src, "magnet:") {
		source.Magnet = src
	} else {
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		source.MetaInfo = b
	}
	var peers []net.Addr
	for _, p := range peerArgs {
		addr, err := net.ResolveTCPAddr("tcp", p)
		if err != nil {
			return fmt.Errorf("--peer %s: %w", p, err)
		}
		peers = append(peers, addr)
	}
	var want *manifest.Repo
	var wantCommit string
	if len(args) == 1 {
		spec, rev, _ := strings.Cut(args[0], "@")
		repo, err := hub.ParseRepo(spec)
		if err != nil {
			return err
		}
		want, wantCommit = &manifest.Repo{Type: string(repo.Type), ID: repo.ID}, rev
	}

	a, err := d.openApp(ctx)
	if err != nil {
		return err
	}
	defer a.Close()
	client, err := wktorrent.NewClient(a.store, wktorrent.ClientConfig{
		DataDir: filepath.Join(a.cfg.Home, "torrent-client"),
	})
	if err != nil {
		return err
	}
	defer client.Close()
	cmd.PrintErrln("fetching the torrent's metadata and files from peers...")
	res, err := a.keeper.PullTorrent(ctx, keep.TorrentPull{Client: client, Source: source, Peers: peers, Want: want})
	if err != nil {
		return err
	}
	if wantCommit != "" && wantCommit != res.Manifest.Commit {
		return fmt.Errorf("the torrent is for commit %s, not %s", res.Manifest.Commit, wantCommit)
	}
	printPullSummary(cmd, res)
	return nil
}

func printPullSummary(cmd *cobra.Command, res *keep.PullResult) {
	m := res.Manifest
	var kept int64
	keptFiles := len(res.Downloaded) + len(res.Present)
	skipped := map[string]bool{}
	for _, p := range res.Skipped {
		skipped[p] = true
	}
	for _, f := range m.Files {
		if !skipped[f.Path] {
			kept += f.Size
		}
	}
	parts := []string{fmt.Sprintf("%d downloaded", len(res.Downloaded))}
	if len(res.Present) > 0 {
		parts = append(parts, fmt.Sprintf("%d already kept", len(res.Present)))
	}
	if len(res.Skipped) > 0 {
		parts = append(parts, fmt.Sprintf("%d left out by filters", len(res.Skipped)))
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "kept %s@%s: %d files, %s (%s)\n",
		m.Repo, m.Commit[:12], keptFiles, humanBytes(kept), strings.Join(parts, ", "))
}
