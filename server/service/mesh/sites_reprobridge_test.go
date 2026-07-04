package mesh

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"NanoKVM-Server/middleware"

	"github.com/gin-gonic/contrib/static"
	"github.com/gin-gonic/gin"
)

// TestReproTunnelBridge stands up the EXACT device-side tunnel path — the gin
// engine (the Tls() middleware the Pro runs, plus the static web/dist handler)
// served over a meshConn by siteHost — and bridges it to a localhost TCP port,
// exactly as an AllMyStuff viewer maps the site to http://localhost:<port>. Point
// a browser (or an HTTP client) at that port to reproduce the tunneled web UI
// end to end. Guarded by REPRO_TUNNEL_ADDR (e.g. 127.0.0.1:8899); skipped in a
// normal `go test` run. Run with -timeout 0 and kill it when done.
func TestReproTunnelBridge(t *testing.T) {
	addr := os.Getenv("REPRO_TUNNEL_ADDR")
	if addr == "" {
		t.Skip("set REPRO_TUNNEL_ADDR=127.0.0.1:PORT to run the live tunnel bridge")
	}
	webDist := os.Getenv("REPRO_WEB_DIST")
	if webDist == "" {
		webDist = "../../../web/dist"
	}

	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(middleware.Tls())               // Pro runs this (proto=https); tunnel requests bypass it
	engine.Use(middleware.MeshSessionCookie()) // issue the login-bypass cookie on mesh requests
	engine.Use(static.Serve("/", static.LocalFile(webDist, true)))

	const port = uint16(443)
	const route = "route:repro:site->kvm:0"
	const peer = "repro-peer"

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	defer ln.Close()

	// Browser-facing TCP conns keyed by tunnel conn id, so outbound SiteData is
	// written back to the right socket in order.
	var mu sync.Mutex
	tcpByConn := map[uint64]net.Conn{}

	send := func(_ string, f SiteFrame) error {
		mu.Lock()
		c := tcpByConn[f.Conn]
		mu.Unlock()
		if c == nil {
			return nil
		}
		switch f.Kind {
		case SiteEventKindData:
			_, werr := c.Write(f.Data)
			return werr
		case SiteEventKindClose:
			if tc, ok := c.(*net.TCPConn); ok {
				_ = tc.CloseWrite() // device finished this response; half-close
			} else {
				_ = c.Close()
			}
		}
		return nil
	}

	host := newSiteHost(engine, port, send)
	host.markRouteActive(route, peer)

	var connSeq uint64
	fmt.Printf("REPRO LISTENING http://%s  (web/dist=%s)\n", addr, webDist)
	os.Stdout.Sync()

	for {
		tcp, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		connID := atomic.AddUint64(&connSeq, 1)
		mu.Lock()
		tcpByConn[connID] = tcp
		mu.Unlock()

		host.handleFrame(peer, NewSiteOpen(route, 0, connID, port))

		go func(tcp net.Conn, connID uint64) {
			defer func() {
				mu.Lock()
				delete(tcpByConn, connID)
				mu.Unlock()
				_ = tcp.Close()
			}()
			r := bufio.NewReader(tcp)
			var seq uint64 = 1
			buf := make([]byte, 32*1024)
			for {
				n, rerr := r.Read(buf)
				if n > 0 {
					host.handleFrame(peer, NewSiteData(route, seq, connID, append([]byte(nil), buf[:n]...)))
					seq++
				}
				if rerr != nil {
					host.handleFrame(peer, NewSiteClose(route, seq, connID))
					return
				}
			}
		}(tcp, connID)
	}
}
