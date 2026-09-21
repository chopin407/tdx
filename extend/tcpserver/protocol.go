// Package tcpserver exposes the HTTP server's complete read-only API over a
// persistent, length-prefixed TCP connection.
package tcpserver

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	headerSize      = 4
	DefaultMaxFrame = 16 << 20
)

// Request is one TCP request. Action is an HTTP route without the leading
// slash (for example "quote" or "kline/day/qfq").
type Request struct {
	ID     string         `json:"id"`
	Action string         `json:"action"`
	Token  string         `json:"token,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

// Response is one TCP response. Responses can arrive out of request order;
// ID correlates them with requests.
type Response struct {
	ID     string          `json:"id"`
	Status int             `json:"status"`
	Code   int             `json:"code"`
	Msg    string          `json:"msg"`
	Data   json.RawMessage `json:"data,omitempty"`
}

func readFrame(r io.Reader, max int) ([]byte, error) {
	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || uint64(n) > uint64(max) {
		return nil, fmt.Errorf("tcpserver: invalid frame size %d (max %d)", n, max)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func writeFrame(w io.Writer, v any, max int) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) == 0 || len(b) > max {
		return fmt.Errorf("tcpserver: response frame size %d exceeds max %d", len(b), max)
	}
	var header [headerSize]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	if _, err = w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
