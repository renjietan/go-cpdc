package buildinfo

import "testing"

func TestWorkspaceFactor(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   uint64
	}{
		{name: "generic", target: "generic", want: 1},
		{name: "ccu", target: "ccu", want: 2},
		{name: "iec", target: "iec", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := Target
			Target = tt.target
			defer func() { Target = previous }()

			if got := WorkspaceFactor(); got != tt.want {
				t.Fatalf("WorkspaceFactor() = %d, want %d", got, tt.want)
			}
		})
	}
}
