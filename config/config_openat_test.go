package config

import (
	"runtime"
	"testing"
)

func TestUseOpenat2(t *testing.T) {
	// Ensure UseOpenat2 doesn't panic.
	Set(&Configuration{
		AuthenticationToken: "test",
		System: SystemConfiguration{
			OpenatMode: "auto",
		},
	})

	result := UseOpenat2()

	switch runtime.GOOS {
	case "darwin":
		if result {
			t.Error("expected UseOpenat2() to return false on Darwin")
		}
	case "linux":
		// On Linux it may be true or false depending on kernel version.
		// Just verify it returns without error.
		t.Logf("UseOpenat2() returned %v on Linux", result)
	}
}
