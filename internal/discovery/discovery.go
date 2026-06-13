// Package discovery advertises gardynd via mDNS/zeroconf (_gardynd._tcp).
package discovery

import (
	"log"

	"github.com/grandcat/zeroconf"
)

// Advertise registers the service. Returns a shutdown func; both are no-ops on
// error (best-effort).
func Advertise(instance string, port int) func() {
	server, err := zeroconf.Register(instance, "_gardynd._tcp", "local.", port, []string{"path=/state"}, nil)
	if err != nil {
		log.Printf("zeroconf advertise failed (continuing): %v", err)
		return func() {}
	}
	return server.Shutdown
}
