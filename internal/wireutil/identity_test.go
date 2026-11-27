package wireutil

import (
	"regexp"
	"testing"
)

func TestIsLoopback(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST", "127.0.0.1", "127.42.1.2", "::1", "::ffff:127.0.0.1"} {
		if !IsLoopback(host) {
			t.Errorf("loopback rejected: %q", host)
		}
	}
	for _, host := range []string{"", "localhost.", "localhost.example.com", "127.0.0.1:8080", "[::1]", "0.0.0.0", "::", "192.168.1.1", "10.0.0.1", "169.254.169.254", "example.com"} {
		if IsLoopback(host) {
			t.Errorf("non-loopback accepted: %q", host)
		}
	}
}

func TestUUIDv4(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := make(map[string]struct{})
	for range 256 {
		id, err := UUIDv4()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(id) {
			t.Fatalf("invalid UUIDv4: %q", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatal("duplicate request ID")
		}
		seen[id] = struct{}{}
	}
}
