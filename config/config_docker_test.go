package config

import "testing"

// TestDockerNetworkConfiguration_IsContainerNetworkMode tests the IsContainerNetworkMode
// method to ensure it correctly identifies when the network mode is set to share another
// container's network namespace (i.e., "container:<name>" format).
func TestDockerNetworkConfiguration_IsContainerNetworkMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		expected bool
	}{
		{"container mode with name", "container:caddy", true},
		{"container mode with different name", "container:some-vpn-container", true},
		{"container mode empty name", "container:", false}, // Docker rejects "container:" without a name
		{"default pelican network", "pelican_nw", false},
		{"bridge network", "bridge", false},
		{"host network", "host", false},
		{"empty string", "", false},
		{"partial match", "containers", false}, // Should not match without colon
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DockerNetworkConfiguration{Mode: tt.mode}
			if got := c.IsContainerNetworkMode(); got != tt.expected {
				t.Errorf("IsContainerNetworkMode() = %v, want %v for mode %q", got, tt.expected, tt.mode)
			}
		})
	}
}
