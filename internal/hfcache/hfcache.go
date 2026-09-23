// Package hfcache writes kept revisions out in the layout huggingface_hub
// and llama.cpp read, so tools can load them offline without a proxy:
//
//	<cache>/models--org--name/blobs/<etag>
//	<cache>/models--org--name/snapshots/<commit>/<path> -> ../../blobs/<etag>
//	<cache>/models--org--name/refs/<name>                 (contains the commit)
//
// The etag is what the Hub serves: SHA-256 for LFS files, the git blob SHA-1
// of the content for regular files. Blobs are materialised from the store by
// reflink, hardlink, symlink or copy, in that order of preference.
package hfcache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

// LinkMode chooses how files are materialised from the store.
type LinkMode string

const (
	Auto     LinkMode = "auto" // reflink, hardlink, symlink, copy: first that works
	Reflink  LinkMode = "reflink"
	Hardlink LinkMode = "hardlink"
	Symlink  LinkMode = "symlink"
	Copy     LinkMode = "copy"
)

// ParseLinkMode validates a --link value.
func ParseLinkMode(s string) (LinkMode, error) {
	switch m := LinkMode(s); m {
	case Auto, Reflink, Hardlink, Symlink, Copy:
		return m, nil
	case "":
		return Auto, nil
	}
	return "", fmt.Errorf("unknown link mode %q (auto, reflink, hardlink, symlink, copy)", s)
}

// Result summarises an export.
type Result struct {
	Dir      string           // the repo folder or target directory
	Written  int              // files materialised in this run
	Existing int              // already present and matching
	Missing  []string         // in the manifest but not kept (filtered pulls)
	Methods  map[LinkMode]int // how the written files were materialised
}

// RepoFolder is huggingface_hub's folder name: models--org--name.
func RepoFolder(repo manifest.Repo) string {
	return repo.Type + "s--" + strings.ReplaceAll(repo.ID, "/", "--")
}

// ToCache exports m into an HF cache directory. refs are branch or tag names
// to point at the commit (e.g. "main").
func ToCache(ctx context.Context, st *store.Store, m *manifest.Manifest, cacheDir string, refs []string, mode LinkMode) (*Result, error) {
	repoDir := filepath.Join(cacheDir, RepoFolder(m.Repo))
	res := &Result{Dir: repoDir, Methods: map[LinkMode]int{}}
	snapDir := filepath.Join(repoDir, "snapshots", m.Commit)

	for _, f := range m.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		etag := blobName(f)
		if f.SHA256 == "" || !st.Has(f.SHA256) || etag == "" {
			res.Missing = append(res.Missing, f.Path)
			continue
		}
		blob := filepath.Join(repoDir, "blobs", etag)
		wrote, method, err := materialise(st.Path(f.SHA256), blob, f.Size, mode)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}

		snap := filepath.Join(snapDir, filepath.FromSlash(f.Path))
		rel, err := filepath.Rel(filepath.Dir(snap), blob)
		if err != nil {
			return nil, err
		}
		linked, err := linkSnapshot(rel, blob, snap)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		if wrote || linked {
			res.Written++
			res.Methods[method]++
		} else {
			res.Existing++
		}
	}

	for _, ref := range refs {
		if ref == "" || ref == m.Commit {
			continue
		}
		if err := validateRefName(ref); err != nil {
			return nil, err
		}
		p := filepath.Join(repoDir, "refs", filepath.FromSlash(ref))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(m.Commit), 0o644); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// ToDir writes the kept files of m as a plain directory tree.
func ToDir(ctx context.Context, st *store.Store, m *manifest.Manifest, dir string, mode LinkMode) (*Result, error) {
	res := &Result{Dir: dir, Methods: map[LinkMode]int{}}
	for _, f := range m.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if f.SHA256 == "" || !st.Has(f.SHA256) {
			res.Missing = append(res.Missing, f.Path)
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		wrote, method, err := materialise(st.Path(f.SHA256), dst, f.Size, mode)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		if wrote {
			res.Written++
			res.Methods[method]++
		} else {
			res.Existing++
		}
	}
	return res, nil
}

// blobName is the Hub's etag for the file, which clients use as the blob
// file name. For regular files it's the content's git blob id, which the
// manifest records as git_sha1.
func blobName(f manifest.File) string {
	if f.LFS {
		return f.SHA256
	}
	return f.GitSHA1
}

// materialise makes dst hold the content of src. An existing dst of the
// right size is left alone: it may be a blob huggingface_hub downloaded
// itself, and we never overwrite files we didn't create.
func materialise(src, dst string, size int64, mode LinkMode) (bool, LinkMode, error) {
	if fi, err := os.Stat(dst); err == nil {
		if fi.Size() != size {
			return false, "", fmt.Errorf("%s exists with size %d, expected %d; remove it to export again", dst, fi.Size(), size)
		}
		return false, "", nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, "", err
	}
	var order []LinkMode
	switch mode {
	case Auto:
		order = []LinkMode{Reflink, Hardlink, Symlink, Copy}
	default:
		order = []LinkMode{mode}
	}
	var errs []error
	for _, m := range order {
		var err error
		switch m {
		case Reflink:
			err = reflink(src, dst)
		case Hardlink:
			err = os.Link(src, dst)
		case Symlink:
			err = os.Symlink(src, dst)
		case Copy:
			err = copyFile(src, dst)
		}
		if err == nil {
			return true, m, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", m, err))
	}
	return false, "", errors.Join(errs...)
}

// linkSnapshot points snap at the blob with a relative symlink, as
// huggingface_hub does. Where symlinks aren't allowed (Windows without
// developer mode) it falls back to a hardlink or copy of the blob, which is
// also what huggingface_hub does there.
func linkSnapshot(rel, blob, snap string) (bool, error) {
	if target, err := os.Readlink(snap); err == nil {
		if target == rel {
			return false, nil
		}
		if err := os.Remove(snap); err != nil {
			return false, err
		}
	} else if _, err := os.Stat(snap); err == nil {
		return false, nil // a real file, e.g. from the Windows fallback
	}
	if err := os.MkdirAll(filepath.Dir(snap), 0o755); err != nil {
		return false, err
	}
	if err := os.Symlink(rel, snap); err == nil {
		return true, nil
	}
	if err := os.Link(blob, snap); err == nil {
		return true, nil
	}
	return true, copyFile(blob, snap)
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".weightkeep-tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// validateRefName keeps ref names from escaping the refs directory.
func validateRefName(ref string) error {
	for _, seg := range strings.Split(ref, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "\\\x00") {
			return fmt.Errorf("invalid ref name %q", ref)
		}
	}
	return nil
}
