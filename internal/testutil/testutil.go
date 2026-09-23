// Package testutil holds helpers shared by tests.
package testutil

import (
	"os"
	"testing"
)

// Network skips the test unless WEIGHTKEEP_NETWORK_TESTS=1. Tests that use it
// must only touch small, tier A (permissively licensed, ungated) repos.
func Network(t testing.TB) {
	t.Helper()
	if os.Getenv("WEIGHTKEEP_NETWORK_TESTS") != "1" {
		t.Skip("network test; set WEIGHTKEEP_NETWORK_TESTS=1 to run")
	}
}
