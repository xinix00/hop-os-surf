package surfserve

// WebSocket-ontvanger voor /input (Derek 19-07: "kunnen we de INPUT niet ook
// streamen? goedkoper dan steeds een nieuwe request"). Eén blijvende socket
// waar de browser JSON-events op schrijft — geen HTTP-overhead per
// muisbeweging. Bewust hand-gerold (RFC 6455 is aan de leeskant klein):
// handshake = SHA-1 + base64 uit de stdlib, daarna gemaskeerde frames lezen.
// Wij consumeren alleen; het enige dat we ooit terugschrijven is een pong.

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsUpgrade meldt of dit verzoek een WebSocket-handshake is.
func wsUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// serveInputWS neemt de verbinding over en verwerkt input-events tot hij
// sluit. Elk text/binary-frame is één inputMsg (zelfde JSON als de POST).
func (s *Server) serveInputWS(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	hj, ok := w.(http.Hijacker)
	if key == "" || !ok {
		http.Error(w, "bad websocket handshake", http.StatusBadRequest)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	sum := sha1.Sum([]byte(key + wsGUID))
	brw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
	if err := brw.Flush(); err != nil {
		return
	}

	for {
		op, payload, err := wsReadFrame(brw.Reader)
		if err != nil {
			return
		}
		switch op {
		case 1, 2: // text/binary: één JSON-event
			var m inputMsg
			if json.Unmarshal(payload, &m) == nil {
				if ev, ok := m.event(); ok {
					s.Input(ev)
				}
			}
		case 8: // close
			return
		case 9: // ping → pong (browsers doen dit zelden, maar wees netjes)
			wsWriteFrame(conn, 10, payload)
		}
	}
}

// wsReadFrame leest één (client→server, dus gemaskeerd) frame.
func wsReadFrame(rd io.Reader) (op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(rd, h[:]); err != nil {
		return
	}
	op = h[0] & 0x0F
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(rd, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(rd, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > 1<<16 {
		return 0, nil, errors.New("ws frame too large") // input-events zijn bytes, geen megabytes
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(rd, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(rd, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return op, payload, nil
}

// wsWriteFrame schrijft één (server→client, ongemaskeerd) kort frame.
func wsWriteFrame(w io.Writer, op byte, p []byte) error {
	if len(p) > 125 {
		p = p[:125]
	}
	hdr := [2]byte{0x80 | op, byte(len(p))}
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(p)
	return err
}
