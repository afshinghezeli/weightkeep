package oms

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// walkFiles hashes every regular file under dir, skipping the default
// exclusions. Symbolic links are an error, as OMS signers don't follow them
// (allow_symlinks: false).
func walkFiles(dir string, visit func(rel, sum string)) error {
	// Opening through os.Root keeps every open inside dir, even if a path is
	// swapped for a symlink between the walk and the open.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if ignored(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link; OMS verification doesn't follow them (export with --link copy or hardlink)", rel)
		}
		if d.IsDir() {
			return nil
		}
		f, err := root.Open(rel)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		visit(rel, hex.EncodeToString(h.Sum(nil)))
		return nil
	})
}
