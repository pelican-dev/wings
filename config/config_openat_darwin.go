package config

// UseOpenat2 always returns false on Darwin as the openat2 syscall is
// Linux-specific (kernel 5.6+).
func UseOpenat2() bool {
	return false
}
