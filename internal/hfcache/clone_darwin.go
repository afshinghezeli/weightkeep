package hfcache

import "golang.org/x/sys/unix"

// reflink makes dst a copy-on-write clone of src (APFS clonefile).
func reflink(src, dst string) error {
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
}
