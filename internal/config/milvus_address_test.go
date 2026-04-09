package config

import "testing"

func TestNormalizeMilvusAddress(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"tcp://10.12.105.139:30005", "10.12.105.139:30005"},
		{"TCP://10.0.0.1:19530", "10.0.0.1:19530"},
		{"grpc://h:19530/extra", "h:19530"},
		{"10.0.0.1:19530", "10.0.0.1:19530"},
		{"  https://proxy:443  ", "proxy:443"},
	}
	for _, tt := range tests {
		if got := NormalizeMilvusAddress(tt.in); got != tt.want {
			t.Errorf("NormalizeMilvusAddress(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
