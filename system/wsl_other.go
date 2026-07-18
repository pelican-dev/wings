//go:build !linux

package system

// IsWSL returns false on non-Linux platforms.
func IsWSL() bool {
	return false
}

// wslFeatureInfo is never reached on non-Linux platforms (IsWSL is always
// false), but the symbol must exist so system.go compiles everywhere.
func wslFeatureInfo() *WSLInfo {
	return nil
}
