package config

import "testing"

func TestUseOpenat2ConfigOverride(t *testing.T) {
	Set(&Configuration{
		AuthenticationToken: "test",
		System: SystemConfiguration{
			OpenatMode: "openat",
		},
	})
	openat2Set.Store(false)

	if UseOpenat2() {
		t.Error("expected UseOpenat2() to return false when mode is 'openat'")
	}

	openat2Set.Store(false)
	Update(func(c *Configuration) {
		c.System.OpenatMode = "openat2"
	})

	if !UseOpenat2() {
		t.Error("expected UseOpenat2() to return true when mode is 'openat2'")
	}
}
