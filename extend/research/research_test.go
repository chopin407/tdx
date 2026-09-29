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
	good, bad := dayBytes(), dayBytes()
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
