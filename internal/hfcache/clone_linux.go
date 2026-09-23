package hfcache

import (
	"os"

	"golang.org/x/sys/unix"
)

// reflink makes dst a copy-on-write clone of src (FICLONE: btrfs, XFS,
// bcachefs, overlayfs on those).
func reflink(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(dst)
		}
	}()
	return unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
}
