package keep

import (
	"context"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

// GCOptions tunes garbage collection.
type GCOptions struct {
	// Grace protects unreferenced blobs younger than this. A pull stores
	// blobs before it saves the manifest that references them; the grace
	// period keeps a concurrent gc from deleting them. Zero means none;
	// DefaultGrace is what the CLI uses.
	Grace time.Duration
	// PartialAge is how old an abandoned partial download must be before it
	// is removed. Default 7 days.
	PartialAge time.Duration
	DryRun     bool
}

// DefaultGrace is long enough for any pull to save its manifest.
const DefaultGrace = time.Hour

// GCResult reports what was (or would be) removed.
type GCResult struct {
	Blobs     []store.BlobInfo
	BlobBytes int64
	Tmp       store.TmpCleanup
	// Protected counts unreferenced blobs kept because they are too new.
	Protected int
}

// GC removes blobs no kept revision references, plus stale files in tmp/.
func (k *Keeper) GC(ctx context.Context, opt GCOptions) (*GCResult, error) {
	if opt.PartialAge == 0 {
		opt.PartialAge = 7 * 24 * time.Hour
	}
	now := k.now()
	referenced, err := manifest.ReferencedBlobs(ctx, k.Store)
	if err != nil {
		return nil, err
	}
	blobs, err := k.Store.Blobs(ctx)
	if err != nil {
		return nil, err
	}
	res := &GCResult{}
	for _, b := range blobs {
		if referenced[b.SHA256] {
			continue
		}
		if now.Sub(b.ModTime) < opt.Grace {
			res.Protected++
			continue
		}
		if !opt.DryRun {
			if err := k.Store.Remove(ctx, b.SHA256); err != nil {
				return res, err
			}
		}
		res.Blobs = append(res.Blobs, b)
		res.BlobBytes += b.Size
	}
	res.Tmp, err = k.Store.CleanTmp(now, 24*time.Hour, opt.PartialAge, opt.DryRun)
	return res, err
}
