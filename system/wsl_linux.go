//go:build linux

package system

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// limitsMode holds the effective resource-limit enforcement mode for node
// feature reporting. The config layer pushes it in at startup via SetLimitsMode
// because the system package cannot import config without an import cycle.
var limitsMode = "enforced"

// SetLimitsMode records the effective limits mode for node feature reporting.
func SetLimitsMode(mode string) {
	if mode != "" {
		limitsMode = mode
	}
}

// wslFeatureInfo reports the WSL node's networking/storage/limits posture,
// derived from the live environment, for the panel's node feature display.
func wslFeatureInfo() *WSLInfo {
	mode := WslNetworkingMode()
	mirrored := mode == "mirrored"
	support := "supported"
	if !mirrored {
		support = "unsupported"
	}
	return &WSLInfo{
		Networking:   mode,
		UDPInbound:   mirrored,
		LimitsMode:   limitsMode,
		Storage:      "ext4_only",
		SupportLevel: support,
	}
}

var (
	wslOnce   sync.Once
	wslCached bool
)

// IsWSL returns true if Wings is running inside Windows Subsystem for Linux.
// The result is cached after the first call.
func IsWSL() bool {
	wslOnce.Do(func() {
		// Check for WSL_INTEROP environment variable.
		if os.Getenv("WSL_INTEROP") != "" {
			wslCached = true
			return
		}
		// Check /proc/version for "microsoft" (case-insensitive).
		b, err := os.ReadFile("/proc/version")
		if err != nil {
			return
		}
		wslCached = strings.Contains(strings.ToLower(string(b)), "microsoft")
	})
	return wslCached
}

// WslNetworkingMode returns the WSL2 networking mode configured in .wslconfig.
// Returns "mirrored" if networkingMode=mirrored is set under [wsl2], otherwise "nat".
func WslNetworkingMode() string {
	// Try to find .wslconfig by scanning /mnt/c/Users/ for a home dir containing it.
	username := findWindowsUser()
	if username == "" {
		return "nat"
	}

	configPath := filepath.Join("/mnt/c/Users", username, ".wslconfig")
	f, err := os.Open(configPath)
	if err != nil {
		return "nat"
	}
	defer f.Close()

	inWSL2Section := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Section header.
		if strings.HasPrefix(line, "[") {
			inWSL2Section = strings.EqualFold(line, "[wsl2]")
			continue
		}

		if !inWSL2Section {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if strings.EqualFold(key, "networkingMode") && strings.EqualFold(val, "mirrored") {
			return "mirrored"
		}
	}

	return "nat"
}

// WslIsWindowsMount returns true if the given path resides on a Windows-mounted
// filesystem (drvfs/9p) or under /mnt/.
func WslIsWindowsMount(path string) bool {
	// Quick check for /mnt/ prefix (common Windows drive mounts).
	if strings.HasPrefix(path, "/mnt/") {
		return true
	}

	// Parse /proc/mounts to check the filesystem type of the mount point for
	// this path.
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return false
	}
	defer f.Close()

	// Find the longest matching mount point.
	var bestMount string
	var bestFsType string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		mountpoint := fields[1]
		fstype := fields[2]

		if strings.HasPrefix(path, mountpoint) && len(mountpoint) > len(bestMount) {
			bestMount = mountpoint
			bestFsType = fstype
		}
	}

	return bestFsType == "drvfs" || bestFsType == "9p"
}

// findWindowsUser attempts to find the Windows username by looking for a
// .wslconfig file under /mnt/c/Users/. Falls back to $USER.
func findWindowsUser() string {
	entries, err := os.ReadDir("/mnt/c/Users")
	if err != nil {
		return os.Getenv("USER")
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip system directories.
		if strings.EqualFold(name, "Public") || strings.EqualFold(name, "Default") ||
			strings.EqualFold(name, "Default User") || strings.EqualFold(name, "All Users") {
			continue
		}
		if _, err := os.Stat(filepath.Join("/mnt/c/Users", name, ".wslconfig")); err == nil {
			return name
		}
	}
	// Fallback to $USER.
	return os.Getenv("USER")
}
