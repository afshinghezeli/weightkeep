package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/hfcache"
	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

func newExportCommand(d deps) *cobra.Command {
	var cacheDir, toDir, link string
	cmd := &cobra.Command{
		Use:   "export REPO[@REVISION]",
		Short: "Write a kept revision into the Hugging Face cache or a directory",
		Long: `Export writes a kept revision in the layout huggingface_hub and llama.cpp read
(models--org--name/blobs, snapshots, refs), so they load it offline with
HF_HUB_OFFLINE=1 and no proxy running. It uses no network.

By default it writes to the Hugging Face cache (HF_HUB_CACHE, see
'weightkeep env'). --to writes a plain directory instead.

Files are reflinked where the filesystem supports it (no extra space, fully
independent copies), else hardlinked, symlinked into the store, or copied.
Existing files are never overwritten.`,
		Example: `  weightkeep export HuggingFaceTB/SmolLM2-135M
  HF_HUB_OFFLINE=1 python -c "from transformers import AutoTokenizer; AutoTokenizer.from_pretrained('HuggingFaceTB/SmolLM2-135M')"
  weightkeep export bartowski/SmolLM2-135M-Instruct-GGUF --to ./models/smollm`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			mode, err := hfcache.ParseLinkMode(link)
			if err != nil {
				return err
			}
			a, err := d.openApp(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			spec, rev, _ := strings.Cut(args[0], "@")
			repo, err := hub.ParseRepo(spec)
			if err != nil {
				return err
			}
			mrepo := manifest.Repo{Type: string(repo.Type), ID: repo.ID}
			if rev == "" {
				rev = "main"
			}
			commit, _, err := manifest.ResolveRef(ctx, a.store, mrepo, rev)
			if err != nil {
				return fmt.Errorf("%w; pull it first", err)
			}
			m, err := manifest.Load(ctx, a.store, mrepo, commit)
			if err != nil {
				return err
			}

			var res *hfcache.Result
			if toDir != "" {
				res, err = hfcache.ToDir(ctx, a.store, m, toDir, mode)
			} else {
				if cacheDir == "" {
					cacheDir = a.cfg.HFHubCache
				}
				refs, rerr := manifest.RefsFor(ctx, a.store, mrepo, commit)
				if rerr != nil {
					return rerr
				}
				res, err = hfcache.ToCache(ctx, a.store, m, cacheDir, refs, mode)
			}
			if err != nil {
				return err
			}

			var how []string
			for method, n := range res.Methods {
				how = append(how, fmt.Sprintf("%d %s", n, method))
			}
			sort.Strings(how)
			msg := fmt.Sprintf("exported %s@%s to %s: %d written", m.Repo, m.Commit[:12], res.Dir, res.Written)
			if len(how) > 0 {
				msg += " (" + strings.Join(how, ", ") + ")"
			}
			if res.Existing > 0 {
				msg += fmt.Sprintf(", %d already there", res.Existing)
			}
			if len(res.Missing) > 0 {
				msg += fmt.Sprintf(", %d not kept (left out when pulling)", len(res.Missing))
			}
			cmd.PrintErrln(msg)
			return nil
		},
	}
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "Hugging Face hub cache to write into (default: HF_HUB_CACHE)")
	cmd.Flags().StringVar(&toDir, "to", "", "write a plain directory instead of the cache layout")
	cmd.Flags().StringVar(&link, "link", "auto", "how to materialise files: auto, reflink, hardlink, symlink, copy")
	cmd.MarkFlagsMutuallyExclusive("cache-dir", "to")
	return cmd
}
