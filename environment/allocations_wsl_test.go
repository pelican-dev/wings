package environment

import (
	"testing"

	"github.com/pelican-dev/wings/config"
)

func TestValidateAllocationsForWSL(t *testing.T) {
	ports := config.PortsConfig{
		TCPRangeStart:     20000,
		TCPRangeEnd:       25000,
		UDPRangeStart:     20000,
		UDPRangeEnd:       25000,
		MaxPortsPerServer: 3,
	}

	mirrored := config.WSLRuntime{Active: true, Networking: "mirrored"}
	nat := config.WSLRuntime{Active: true, Networking: "nat", UDPBlocked: true}
	notWSL := config.WSLRuntime{}

	cases := []struct {
		name    string
		state   config.WSLRuntime
		all     []int
		wantErr bool
	}{
		{"non-wsl node ignores everything", notWSL, []int{80, 443, 100000}, false},
		{"mirrored, ports in range", mirrored, []int{20000, 22500, 25000}, false},
		{"mirrored, port below range", mirrored, []int{19999}, true},
		{"mirrored, port above range", mirrored, []int{25001}, true},
		{"mirrored, too many ports", mirrored, []int{20001, 20002, 20003, 20004}, true},
		{"mirrored, no allocations", mirrored, nil, false},
		{"nat blocks any allocation", nat, []int{20001}, true},
		{"nat with no allocations is fine", nat, nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAllocationsForWSL(tc.state, ports, tc.all)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
