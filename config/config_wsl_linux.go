//go:build linux

package config

import (
	"fmt"
	"strings"

	"github.com/apex/log"

	"github.com/pelican-dev/wings/system"
)

// ValidateWSLEnvironment checks the WSL environment for required configuration
// and records the resolved posture for downstream enforcement. If not running
// in WSL this is a no-op. Validation order mirrors windows.md §11: detect WSL,
// validate networking mode, validate the data root, validate port ranges.
func ValidateWSLEnvironment() error {
	if !system.IsWSL() {
		return nil
	}

	cfg := &_config.System
	log.Info("WSL environment detected, validating configuration...")

	// Resolve the effective networking mode. "auto" (the default) reads the mode
	// from the live environment; an explicit value trusts the operator.
	mode := strings.ToLower(strings.TrimSpace(cfg.WSL.NetworkingMode))
	if mode == "" || mode == "auto" {
		mode = system.WslNetworkingMode()
	}
	mirrored := mode == "mirrored"

	if !mirrored {
		if cfg.WSL.RequireMirrored && !cfg.WSL.AllowUnsupportedNAT {
			return fmt.Errorf(`WSL detected without mirrored networking enabled.

To enable mirrored networking, create or edit:
    %%USERPROFILE%%\.wslconfig

Add:
    [wsl2]
    networkingMode=mirrored

Then run:
    wsl --shutdown

Restart WSL and relaunch Wings.`)
		}
		log.Warn("WSL node is not in mirrored networking mode: marked UNSUPPORTED (NAT). UDP allocations will be blocked.")
	}

	// The data root must live on the WSL ext4 filesystem, not a Windows mount.
	if cfg.Storage.EnforceWSLExt4 && !cfg.Storage.AllowWindowsMountOverride {
		if system.WslIsWindowsMount(cfg.Data) {
			return fmt.Errorf(`Server data directory is on a Windows-mounted filesystem.
Move the data root to a directory inside the WSL ext4 filesystem
(example: /var/lib/pelican)`)
		}
	}

	// Configured port ranges must be well-formed so firewall rules stay sane.
	if err := validatePortRanges(cfg.Ports); err != nil {
		return err
	}

	// Resource limits are best-effort inside WSL. Downgrade the default so the
	// container runtime does not hard-fail on limits it cannot apply.
	if cfg.LimitsMode == "enforced" {
		cfg.LimitsMode = "best_effort"
		log.Info("WSL detected: limits_mode set to best_effort")
	}
	system.SetLimitsMode(cfg.LimitsMode)

	networking := "mirrored"
	support := "supported"
	if !mirrored {
		networking = "nat"
		support = "unsupported"
	}
	setWSLState(WSLRuntime{
		Active:       true,
		Networking:   networking,
		UDPBlocked:   !mirrored,
		SupportLevel: support,
	})

	if mirrored {
		log.Info("WSL mirrored networking validated successfully")
	}
	return nil
}

// validatePortRanges ensures the configured TCP/UDP ranges are well-formed.
func validatePortRanges(p PortsConfig) error {
	for _, r := range []struct {
		name       string
		start, end int
	}{
		{"tcp", p.TCPRangeStart, p.TCPRangeEnd},
		{"udp", p.UDPRangeStart, p.UDPRangeEnd},
	} {
		if r.start < 1 || r.end > 65535 || r.start > r.end {
			return fmt.Errorf("invalid %s port range %d-%d: must satisfy 1 <= start <= end <= 65535", r.name, r.start, r.end)
		}
	}
	if p.MaxPortsPerServer < 1 {
		return fmt.Errorf("ports.max_ports_per_server must be >= 1 (got %d)", p.MaxPortsPerServer)
	}
	return nil
}
