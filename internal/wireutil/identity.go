// Package wireutil contains provider-independent wire and endpoint helpers.
package wireutil

import (
	"crypto/rand"
	"fmt"
	"net"
	"strings"
)

// IsLoopback reports whether a host is a literal loopback address or
// localhost. It deliberately does not resolve DNS or allow private networks.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// UUIDv4 returns a random RFC 9562 version 4 UUID in its canonical
// lowercase, hyphenated form. Keep the native provider's other ID formats
// separate; not every session identifier is a UUID.
func UUIDv4() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}
