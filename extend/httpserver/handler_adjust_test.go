package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/injoyai/tdx/protocol"
)

type adjustmentFixture struct {
	calls int
	fail  bool
}

func (f *adjustmentFixture) GetKlineDay(_ string, start, count uint16) (*protocol.KlineResp, error) {
	f.calls++
	d := time.Date(2025, 1, 1, 15, 0, 0, 0, time.UTC)
	return &protocol.KlineResp{List: []*protocol.Kline{
		{Time: d, Open: 10000, High: 10000, Low: 10000, Close: 10000, Volume: 123},
		{Time: d.AddDate(0, 0, 1), Open: 9000, High: 9000, Low: 9000, Close: 9000, Volume: 456},
	}}, nil
}
func (f *adjustmentFixture) GetGbbq(string) (*protocol.GbbqResp, error) {
	if f.fail {
		return nil, errors.New("upstream failed")
	}
	return &protocol.GbbqResp{List: []*protocol.Gbbq{
		{Time: time.Date(2025, 1, 2, 15, 0, 0, 0, time.UTC), Category: 1, C1: 10},
		{Time: time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC), Category: 1, C1: 50},
	}}, nil
}
func TestAdjustedDailyFactorsAndPaging(t *testing.T) {
	f := &adjustmentFixture{}
	ks, fs, err := loadAdjustedDaily(context.Background(), f, "sz000001")
	if err != nil {
		t.Fatal(err)
	}
	q := protocol.ApplyQFQ(ks, fs)
	h := protocol.ApplyHFQ(ks, fs)
	if q[0].Close != 9000 || q[1].Close != 9000 || h[0].Close != 10000 || h[1].Close != 10000 {
		t.Fatalf("incorrect adjustment: %v %v", q, h)
	}
	if ks[0].Close != 10000 || q[0].Volume != 123 {
		t.Fatal("raw price or volume modified")
	}
	if dailyPage(h, 0, 1)[0].Close != 10000 || dailyPage(h, 1, 1)[0].Time != ks[0].Time || len(dailyPage(h, 2, 1)) != 0 {
		t.Fatal("paging")
	}
	f.fail = true
	if _, _, err = loadAdjustedDaily(context.Background(), f, "sz000001"); err == nil {
		t.Fatal("must propagate action failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.calls = 0
	if _, _, err = loadAdjustedDaily(ctx, f, "sz000001"); err == nil || f.calls != 0 {
		t.Fatal("cancelled request fetched data")
	}
}
func TestAdditionalRoutesValidation(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	for _, path := range []string{
		"/kline/day/qfq", "/kline/day/hfq/all", "/kline/day/factors",
		"/index/minute/all", "/index/5minute/all", "/index/15minute/all", "/index/30minute/all", "/index/60minute/all",
		"/index/week", "/index/month", "/index/quarter", "/index/year",
		"/kline/day?adjust=bad", "/kline/day/all?adjust=bad",
		"/kline/day/qfq?code=sz000001&start=0&count=801",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Errorf("%s: status %d", path, w.Code)
		}
	}
	for _, path := range []string{"/missing", "/ex/markets"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("%s should be 404", path)
		}
	}
}
