package mesh

import (
	"bufio"
	"bytes"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestSiteTunnelServesLargeResponseIntact drives a real gin engine over the site
// tunnel and reassembles the response frames exactly like the AllMyStuff side
// would (by per-conn seq), asserting a large asset comes back byte-for-byte
// intact. This is the "does the tunnel truncate the Pro's big JS bundle?" check —
// the NanoKVM's smaller bundle wouldn't reveal a size-dependent bug.
func TestSiteTunnelServesLargeResponseIntact(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// A ~2 MB asset — bigger than the Pro web bundle's largest chunk, and ~50
	// SiteChunkBytes frames, so any tail-drop / ordering bug shows up.
	body := make([]byte, 2*1024*1024)
	for i := range body {
		body[i] = byte('A' + (i % 26))
	}

	engine := gin.New()
	engine.GET("/big.js", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/javascript", body)
	})

	const allowed = uint16(80)
	const route = "route:peer:site->kvm:0"
	const peer = "peer-AB12C"
	const conn = uint64(1)

	var mu sync.Mutex
	var out []SiteFrame
	send := func(p string, f SiteFrame) error {
		// Mimic production: sendSiteFrame json.Marshals the frame synchronously,
		// which copies f.Data before meshConn's caller buffer is reused. A capture
		// that just held the slice would alias that buffer and see corruption.
		cp := append([]byte(nil), f.Data...)
		f.Data = cp
		mu.Lock()
		out = append(out, f)
		mu.Unlock()
		return nil
	}

	h := newSiteHost(engine, allowed, nil, send)
	h.markRouteActive(route, peer)

	// Open the tunneled connection, then feed a plain HTTP/1.1 request for the
	// big asset (Connection: close so the server writes one response and closes).
	h.handleFrame(peer, NewSiteOpen(route, 0, conn, allowed))
	req := "GET /big.js HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	h.handleFrame(peer, NewSiteData(route, 1, conn, []byte(req)))

	// Wait until the server has emitted a Close frame for this conn (its response
	// is fully written by then), bounded so a hang fails the test.
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		done := false
		for _, f := range out {
			if f.Conn == conn && f.Kind == SiteEventKindClose {
				done = true
			}
		}
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Reassemble the response the way AllMyStuff does: Data frames for this conn,
	// ordered by seq, concatenated.
	mu.Lock()
	frames := append([]SiteFrame(nil), out...)
	mu.Unlock()

	var data []SiteFrame
	for _, f := range frames {
		if f.Conn == conn && f.Kind == SiteEventKindData {
			data = append(data, f)
		}
	}
	sort.Slice(data, func(i, j int) bool { return data[i].Seq < data[j].Seq })

	var raw bytes.Buffer
	for _, f := range data {
		raw.Write(f.Data)
	}

	seqs := make([]uint64, len(data))
	for i, f := range data {
		seqs[i] = f.Seq
	}
	head := raw.Bytes()
	if len(head) > 160 {
		head = head[:160]
	}
	t.Logf("frames=%d raw=%d seqs=%v", len(data), raw.Len(), seqs)
	t.Logf("first bytes: %q", head)

	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw.Bytes())), nil)
	if err != nil {
		t.Fatalf("could not parse tunneled HTTP response (%d bytes across %d frames): %v", raw.Len(), len(data), err)
	}
	got := make([]byte, 0, len(body))
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		got = append(got, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	_ = resp.Body.Close()

	if len(got) != len(body) {
		t.Fatalf("truncated tunneled response: got %d bytes, want %d (frames=%d, raw=%d)", len(got), len(body), len(data), raw.Len())
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("tunneled response body corrupted (len ok at %d, but bytes differ)", len(got))
	}
}
