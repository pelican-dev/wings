//go:build !linux

package config

// ValidateWSLEnvironment is a no-op on non-Linux platforms.
func ValidateWSLEnvironment() error {
	return nil
}
