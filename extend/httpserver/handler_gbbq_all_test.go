package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
)

func TestGbbqBatch(t *testing.T) {
	codes, err := parseGbbqCodes("sz000001, 600519,sz000001")
	if err != nil || len(codes) != 2 || codes[1] != "sh600519" {
		t.Fatalf("%v %v", codes, err)
	}
	for _, raw := range []string{"sz000001,", "xxx", strings.Repeat("sz000001,", 100) + "sz000001"} {
		if _, err := parseGbbqCodes(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	calls := 0
	result, err := collectGbbq(context.Background(), codes, func(code string) (*protocol.GbbqResp, error) { calls++; return &protocol.GbbqResp{}, nil })
	if err != nil || calls != 2 || result["sz000001"] == nil {
		t.Fatal("empty events should be []")
	}
	calls = 0
	result, err = collectGbbq(context.Background(), codes, func(code string) (*protocol.GbbqResp, error) { calls++; return nil, errors.New("offline") })
	if err == nil || result != nil || calls != 1 {
		t.Fatal("partial success must not be published")
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls = 0
	_, err = collectGbbq(ctx, codes, func(code string) (*protocol.GbbqResp, error) { calls++; cancel(); return &protocol.GbbqResp{}, nil })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancel did not stop next security")
	}
}

type unavailablePool struct{}

func (unavailablePool) Get() (*tdx.Client, error)        { return nil, errors.New("offline") }
func (unavailablePool) Put(*tdx.Client)                  {}
func (unavailablePool) Do(func(*tdx.Client) error) error { return errors.New("offline") }
func (unavailablePool) Go(func(*tdx.Client)) error       { return errors.New("offline") }

func TestGbbqAllHTTP(t *testing.T) {
	s := &Server{pool: unavailablePool{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	for _, tc := range []struct {
		url    string
		status int
	}{
		{"/gbbq/all?codes=bad!", 400}, {"/gbbq/all?codes=sz000001", 502}, {"/gbbq/all", 502},
		{"/kline/hour", 400}, {"/kline/hour/all", 400},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", tc.url, nil))
		if w.Code != tc.status {
			t.Errorf("%s: %d", tc.url, w.Code)
		}
	}
}
