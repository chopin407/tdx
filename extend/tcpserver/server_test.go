package tcpserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func testHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /quote", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]string{"codes": r.URL.Query().Get("codes")}})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": "pong"})
	})
	return mux
}

func startTestServer(t *testing.T, opts ...Option) (string, *Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New(testHandler(), opts...)
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() { _ = s.Close() })
	return ln.Addr().String(), s
}

func TestClientCallAndArrayParams(t *testing.T) {
	addr, _ := startTestServer(t, WithToken("secret"))
	c, err := Dial(addr, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var got struct {
		Codes string `json:"codes"`
	}
	if err := c.Call(context.Background(), "quote", map[string]any{"codes": []any{"sz000001", "sh600519"}}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Codes != "sz000001,sh600519" {
		t.Fatalf("codes=%q", got.Codes)
	}
}

func TestAuthentication(t *testing.T) {
	addr, _ := startTestServer(t, WithToken("secret"))
	c, err := Dial(addr, "wrong", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call(context.Background(), "ping", nil, nil); err == nil {
		t.Fatal("expected auth error")
	}
}

func TestConcurrentCalls(t *testing.T) {
	addr, _ := startTestServer(t, WithMaxInflight(8))
	c, err := Dial(addr, "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out string
			if err := c.Call(context.Background(), "ping", nil, &out); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestFrameLimit(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go func() { _ = writeFrame(left, Request{ID: "1", Action: "ping"}, DefaultMaxFrame) }()
	if _, err := readFrame(right, 2); err == nil {
		t.Fatal("expected frame limit error")
	}
}
