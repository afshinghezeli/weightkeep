//go:build !darwin && !linux

package hfcache

import "errors"

func reflink(_, _ string) error { return errors.ErrUnsupported }
