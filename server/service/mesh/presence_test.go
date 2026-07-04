package mesh

import (
	"testing"

	"NanoKVM-Server/config"
)

// The sites tunnel always serves plaintext in-process HTTP, so the advertised
// scheme must be "http" regardless of the device's own LAN proto. The Pro
// defaults to proto=https, so a scheme that tracked conf.Proto would advertise
// "https" and break AllMyStuff's "Open KVM" (the viewer would speak TLS into a
// plaintext local proxy). This guards that regression.
func TestWebSchemeIsAlwaysHTTP(t *testing.T) {
	for _, proto := range []string{"http", "https", ""} {
		conf := &config.Config{Proto: proto, Port: config.Port{Http: 80, Https: 443}}
		if got := webScheme(conf); got != "http" {
			t.Fatalf("webScheme(proto=%q) = %q, want %q", proto, got, "http")
		}
	}
}

// webPort is an opaque id shared by the advert and the site-host allow-list, so
// it just needs to be internally consistent — it tracks the configured listener
// port. The Open frame the viewer sends echoes the advertised port, so any
// value matches. This pins the mapping so the advert and allowedPort never drift.
func TestWebPortTracksProto(t *testing.T) {
	https := &config.Config{Proto: "https", Port: config.Port{Http: 80, Https: 443}}
	if got := webPort(https); got != 443 {
		t.Fatalf("webPort(https) = %d, want 443", got)
	}
	http := &config.Config{Proto: "http", Port: config.Port{Http: 80, Https: 443}}
	if got := webPort(http); got != 80 {
		t.Fatalf("webPort(http) = %d, want 80", got)
	}
}
