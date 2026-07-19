package surfserve

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/compositor"
	"github.com/xinix00/hop-os-surf/surf"
	"github.com/xinix00/hop-os-surf/window"
)

// wsMask maskeert een client-frame (RFC 6455: client→server is verplicht
// gemaskeerd — precies wat een echte browser stuurt).
func wsClientFrame(op byte, payload string) []byte {
	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	f := []byte{0x80 | op, 0x80 | byte(len(payload))}
	f = append(f, mask[:]...)
	for i := 0; i < len(payload); i++ {
		f = append(f, payload[i]^mask[i%4])
	}
	return f
}

// TestWSInput: de blijvende input-socket — handshake, gemaskeerd JSON-frame,
// en het event komt tot in de app.
func TestWSInput(t *testing.T) {
	comp := compositor.New(320, 200)
	srv := New(comp, t.Logf)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.ServeSURF(l)
	web := httptest.NewServer(srv.Handler())
	defer web.Close()

	// Eén window met focus: toetsen horen daar te landen.
	win, err := window.Open(l.Addr().String(), "w @ n", 60, 40, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Close()

	// Handshake, zoals een browser hem doet.
	conn, err := net.Dial("tcp", strings.TrimPrefix(web.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	conn.Write([]byte("GET /input HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"))
	rd := bufio.NewReader(conn)
	status, err := rd.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("handshake: %q (%v)", status, err)
	}
	var accept string
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-accept:") {
			accept = strings.TrimSpace(line[len("sec-websocket-accept:"):])
		}
		if line == "\r\n" {
			break
		}
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	if accept != base64.StdEncoding.EncodeToString(sum[:]) {
		t.Fatalf("bad accept key %q", accept)
	}

	// Toets-event over de socket → app ontvangt hem.
	conn.Write(wsClientFrame(1, `{"k":"key","c":65,"v":1}`))
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-win.Events():
			if ev.Kind == surf.InputKey && ev.Code == 65 && ev.Value == 1 {
				return
			}
		case <-deadline:
			t.Fatal("key event did not arrive via websocket")
		}
	}
}
