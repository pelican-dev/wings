package config

// WSLRuntime captures the resolved WSL posture after ValidateWSLEnvironment
// runs, so downstream allocation logic can enforce it consistently across
// platforms. The zero value means "not a WSL node" and imposes no restrictions.
type WSLRuntime struct {
	// Active is true when this node is running inside WSL2.
	Active bool
	// Networking is the resolved mode: "mirrored" or "nat".
	Networking string
	// UDPBlocked is true when UDP allocations must be refused (NAT mode).
	UDPBlocked bool
	// SupportLevel is "supported" (mirrored) or "unsupported" (NAT override).
	SupportLevel string
}

var wslRuntime WSLRuntime

// WSLState returns the resolved WSL posture for this node. It is populated by
// ValidateWSLEnvironment at startup; before that (or on non-WSL nodes) it
// reports the zero value.
func WSLState() WSLRuntime {
	return wslRuntime
}

// setWSLState records the resolved WSL posture. Called from the WSL validator.
func setWSLState(s WSLRuntime) {
	wslRuntime = s
}
