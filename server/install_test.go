package server

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestInstallationWaitError(t *testing.T) {
	tests := []struct {
		name      string
		response  container.WaitResponse
		wantError string
	}{
		{
			name:     "successful installation",
			response: container.WaitResponse{StatusCode: 0},
		},
		{
			name:      "installation script failure",
			response:  container.WaitResponse{StatusCode: 1},
			wantError: "install: installation script exited with code 1",
		},
		{
			name:      "command not found",
			response:  container.WaitResponse{StatusCode: 127},
			wantError: "install: installation script exited with code 127",
		},
		{
			name: "container wait failure",
			response: container.WaitResponse{
				Error: &container.WaitExitError{Message: "daemon disconnected"},
			},
			wantError: "install: installation container wait failed: daemon disconnected",
		},
		{
			name: "container wait failure without message",
			response: container.WaitResponse{
				Error: &container.WaitExitError{},
			},
			wantError: "install: installation container wait failed: unknown container wait error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := installationWaitError(tt.response)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("installationWaitError() returned unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("installationWaitError() returned nil, expected %q", tt.wantError)
			}
			if err.Error() != tt.wantError {
				t.Fatalf("installationWaitError() error = %q, expected %q", err.Error(), tt.wantError)
			}
		})
	}
}
