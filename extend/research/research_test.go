package research

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/injoyai/tdx/protocol"
)

func fixtureBars(n int) []Bar {
	out := []Bar{}
	start, _ := parseDate("2025-01-01")
	for i := 0; i < n; i++ {
		p := 10 + float64(i)*0.1
		out = append(out, Bar{Symbol: "sz000001", Date: day(start.AddDate(0, 0, i)), Open: p, High: p + 0.5, Low: p - 0.5, Close: p + 0.1, Volume: 100000, Amount: 1000000, Source: "fixture"})
	}
	return out
}
func fixtureStrategy() Strategy {
	return Strategy{ID: "test", Kind: "ma_trend", Fast: 2, Slow: 3, Lookback: 3}
}
func fixtureDataset(n int) *Dataset {
	return &Dataset{Version: 1, Bars: map[string][]Bar{"sz000001": fixtureBars(n)}, Actions: map[string][]*protocol.Gbbq{}, ActionsChecked: map[string]bool{"sz000001": true}}
}
func fixtureConfig() BacktestConfig {
	return BacktestConfig{Symbol: "sz000001", Start: "2025-01-05", End: "2025-01-10", Capital: 100000, Commission: 0.0003, MinCommission: 5, SellTax: 0.0005, SlippageBPS: 2, LimitPct: 0.1, Lot: 100}
}
func dayBytes() []byte {
	p := make([]byte, 32)
	for offset, v := range map[int]uint32{0: 20250102, 4: 1000, 8: 1100, 12: 900, 16: 1050, 20: math.Float32bits(100000), 24: 12345} {
		binary.LittleEndian.PutUint32(p[offset:], v)
	}
	return p
}

func TestDayUnitsAndRejectCorruption(t *testing.T) {
	raw := dayBytes()
	b, err := ParseDay("sz000001", raw, 100)
	if err != nil || b[0].Close != 10.5 || b[0].Volume != 12345 {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err = ParseDay("sz000001", raw[:31], 100); err == nil {
		t.Fatal("truncated file accepted")
	}
	binary.LittleEndian.PutUint32(raw[0:], 20250230)
	if _, err = ParseDay("sz000001", raw, 100); err == nil {
		t.Fatal("invalid date accepted")
	}
	raw = dayBytes()
	binary.LittleEndian.PutUint32(raw[28:], 0xc3640007)
	b, err = ParseDay("sz000001", raw, 100)
	if err != nil || b[0].Volume != 1234507 {
		t.Fatal(b, err)
	}
}
func TestClosedThrough(t *testing.T) {
	for _, v := range []struct {
		hour, min int
		want      string
	}{{15, 0, "2025-01-01"}, {16, 29, "2025-01-01"}, {16, 30, "2025-01-02"}} {
		now := time.Date(2025, 1, 2, v.hour, v.min, 0, 0, Shanghai)
		if got := ClosedThrough(now); got != v.want {
			t.Fatal(got)
		}
	}
}

func TestFundPriceScale(t *testing.T) {
	raw := dayBytes()
	stock, err := ParseDay("sz000001", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	fund, err := ParseDay("sh510300", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(stock[0].Close/fund[0].Close-10) > 0.0001 {
		t.Fatal("fund decimal scale lost")
	}
}

func TestQuarantinePreservesGoodRecords(t *testing.T) {
	good := dayBytes()
	bad := dayBytes()
	binary.LittleEndian.PutUint32(bad[0:], 20250103)
	binary.LittleEndian.PutUint32(bad[16:], 800)
	bars, issues, err := ParseDayWithIssues("sz000001", append(good, bad...), 0)
	if err != nil || len(bars) != 1 || len(issues) != 1 || issues[0].Row != 1 || len(issues[0].RawHex) != 64 {
		t.Fatal(bars, issues, err)
	}
}

func TestFundAdjustmentRetainsMilliPrecision(t *testing.T) {
	bars := []Bar{{Symbol: "sh510300", Date: "2025-01-02", Open: 4.551, High: 4.559, Low: 4.501, Close: 4.555}}
	out, err := Adjust(bars, nil, "qfq", "2025-01-02")
	if err != nil || out[0].Close != 4.555 {
		t.Fatal(out, err)
	}
}

func TestBreakoutUsesPreviousHigh(t *testing.T) {
	s := Strategy{ID: "break", Kind: "breakout", Fast: 2, Slow: 3, Lookback: 3}
	bars := fixtureBars(5)
	bars[4].Close = 12
	bars[4].High = 12.1
	sig := Evaluate(s, bars)
	if !sig.Ready || !sig.Entry {
		t.Fatal(sig)
	}
	bars[4].Close = 10.4
	sig = Evaluate(s, bars)
	if sig.Entry {
		t.Fatal("false breakout")
	}
}
func TestAdjustmentExcludesFutureEvents(t *testing.T) {
	bars := fixtureBars(5)
	date, _ := parseDate("2025-01-05")
	actions := []*protocol.Gbbq{{Code: "sz000001", Time: date.Add(15 * time.Hour), Category: 1, C1: 10}}
	a, err := Adjust(bars, actions, "qfq", "2025-01-04")
	if err != nil || len(a) != 4 || a[0].Close != bars[0].Close {
		t.Fatal(a, err)
	}
	a, err = Adjust(bars, actions, "qfq", "2025-01-05")
	if err != nil || math.Abs(a[0].Close-(bars[0].Close-1)) > 0.001 {
		t.Fatal(a, err)
	}
}
func TestScreenExcludesStaleAndUnchecked(t *testing.T) {
	d := fixtureDataset(10)
	r, err := Screen(d, fixtureStrategy(), "2025-01-11")
	if err != nil || len(r.Candidates) != 0 || len(r.Excluded) != 1 {
		t.Fatal(r, err)
	}
	d.ActionsChecked["sz000001"] = false
	r, err = Screen(d, fixtureStrategy(), "2025-01-10")
	if err != nil || len(r.Excluded) != 1 {
		t.Fatal(r, err)
	}
}
func TestNextOpenNoLookaheadAndFriction(t *testing.T) {
	d := fixtureDataset(10)
	c := fixtureConfig()
	r, err := Backtest(d, fixtureStrategy(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 1 || r.Trades[0].Date != "2025-01-05" || r.Trades[0].SignalDate != "2025-01-04" || r.Trades[0].Price <= d.Bars[c.Symbol][4].Open {
		t.Fatal(r)
	}
	if r.Equity[0].Cash < 0 {
		t.Fatal("negative cash")
	}
	d.Bars[c.Symbol][9].Close = 1
	d.Bars[c.Symbol][9].Low = 0.5
	r2, err := Backtest(d, fixtureStrategy(), c)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Trades[0] != r.Trades[0] {
		t.Fatal("future bar changed early fill")
	}
	if r.DataHash == r2.DataHash {
		t.Fatal("data fingerprint unchanged")
	}
}
func TestNoFillOnLockedBar(t *testing.T) {
	d := fixtureDataset(10)
	b := &d.Bars["sz000001"][4]
	b.Open = 12
	b.Close = 12
	b.High = 12
	b.Low = 12
	r, err := Backtest(d, fixtureStrategy(), fixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Blocked) == 0 {
		t.Fatal("expected blocked order")
	}
	for _, trade := range r.Trades {
		if trade.Date == b.Date {
			t.Fatal("filled locked bar")
		}
	}
}
func TestRejectUncheckedAndRights(t *testing.T) {
	d := fixtureDataset(10)
	d.ActionsChecked["sz000001"] = false
	if _, err := Backtest(d, fixtureStrategy(), fixtureConfig()); err == nil {
		t.Fatal("unchecked actions accepted")
	}
	d.ActionsChecked["sz000001"] = true
	date, _ := parseDate("2025-01-06")
	d.Actions["sz000001"] = []*protocol.Gbbq{{Code: "sz000001", Time: date, Category: 1, C4: 1}}
	if _, err := Backtest(d, fixtureStrategy(), fixtureConfig()); err == nil {
		t.Fatal("rights issue accepted")
	}
}
func TestDividendAccounting(t *testing.T) {
	d := fixtureDataset(10)
	c := fixtureConfig()
	date, _ := parseDate("2025-01-07")
	d.Actions[c.Symbol] = []*protocol.Gbbq{{Code: c.Symbol, Time: date.Add(15 * time.Hour), Category: 1, C1: 10}}
	for i := 6; i < 10; i++ {
		b := &d.Bars[c.Symbol][i]
		b.Open -= 1
		b.High -= 1
		b.Low -= 1
		b.Close -= 1
	}
	r, err := Backtest(d, fixtureStrategy(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 1 {
		t.Fatal(r.Trades)
	}
	if r.Equity[2].Cash <= r.Equity[1].Cash {
		t.Fatal("dividend not credited")
	}
	if r.Equity[2].Equity < r.Equity[1].Equity {
		t.Fatal("ex-date caused artificial loss")
	}
}
