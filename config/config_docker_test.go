package config

import "testing"

func TestDockerNetworkConfiguration_IsContainerNetworkMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		expected bool
	}{
		{"container mode with name", "container:caddy", true},
		{"container mode with different name", "container:some-vpn-container", true},
		{"container mode empty name", "container:", true}, // Edge case: technically valid prefix
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

