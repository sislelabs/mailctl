package cmd

import "testing"

func TestIsLoopback(t *testing.T) {
	loopback := []string{"127.0.0.1:7777", "localhost:7777", "[::1]:7777", "127.0.0.5:80"}
	for _, a := range loopback {
		if !isLoopback(a) {
			t.Errorf("isLoopback(%q) = false, want true", a)
		}
	}

	// The panel holds a Cloudflare token with DNS edit rights and has no
	// authentication, so anything reachable off this machine must be refused
	// unless the operator opts in explicitly.
	exposed := []string{":7777", "0.0.0.0:7777", "192.168.1.10:7777", "example.com:7777", "garbage"}
	for _, a := range exposed {
		if isLoopback(a) {
			t.Errorf("isLoopback(%q) = true, want false", a)
		}
	}
}
