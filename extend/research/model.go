// Package research provides the local daily-data research workflow. Prices in
// storage are integer milli-yuan; API prices are yuan, stock volume is shares.
package research

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/injoyai/tdx/protocol"
)

var Shanghai = time.FixedZone("Asia/Shanghai", 8*3600)
var symbolRE = regexp.MustCompile(`^(sh|sz|bj)[0-9]{6}$`)
var idRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// Bar is a completed, unadjusted daily observation. Index volume is lots.
type Bar struct {
	Symbol string  `json:"symbol"`
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
	Amount float64 `json:"amount"`
	Source string  `json:"source"`
}

func parseDate(s string) (time.Time, error) { return time.ParseInLocation(time.DateOnly, s, Shanghai) }
func day(t time.Time) string                { return t.In(Shanghai).Format(time.DateOnly) }
func finite(x float64) bool                 { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func (b Bar) validate() error {
	if !symbolRE.MatchString(b.Symbol) {
		return fmt.Errorf("invalid symbol %q", b.Symbol)
	}
	if _, err := parseDate(b.Date); err != nil {
		return err
	}
	for _, v := range []float64{b.Open, b.High, b.Low, b.Close, b.Amount} {
		if !finite(v) || v < 0 {
			return fmt.Errorf("invalid numeric value for %s %s", b.Symbol, b.Date)
		}
	}
	if b.Open <= 0 || b.Low <= 0 || b.Close <= 0 || b.High < math.Max(b.Open, b.Close) || b.Low > math.Min(b.Open, b.Close) || b.Volume < 0 {
		return fmt.Errorf("invalid OHLCV for %s %s", b.Symbol, b.Date)
	}
	return nil
}
func kind(symbol string) string {
	if protocol.IsIndex(symbol) {
		return "index"
	}
	if protocol.IsETF(symbol) {
		return "etf"
	}
	if protocol.IsStock(symbol) {
		return "stock"
	}
	if len(symbol) == 8 && (strings.HasPrefix(symbol, "bj4") || strings.HasPrefix(symbol, "bj8")) {
		return "stock"
	}
	return "other"
}
func milli(v float64) int64 { return int64(math.Round(v * 1000)) }

// Dataset is a coherent in-memory snapshot, independent of subsequent updates.
type Dataset struct {
	Version        int64                        `json:"version"`
	Bars           map[string][]Bar             `json:"bars"`
	Actions        map[string][]*protocol.Gbbq  `json:"actions"`
	ActionsChecked map[string]bool              `json:"actions_checked"`
	Profiles       map[string]InstrumentProfile `json:"profiles"`
}

// InstrumentProfile is current reference data. Historical simulations must
// explicitly report that current names/industry/float shares are not point-in-time.
type InstrumentProfile struct {
	Symbol      string  `json:"symbol"`
	Name        string  `json:"name"`
	Industry    string  `json:"industry"`
	IPODate     string  `json:"ipo_date"`
	IsST        bool    `json:"is_st"`
	FloatShares float64 `json:"float_shares"`
	TotalShares float64 `json:"total_shares"`
	FinanceDate string  `json:"finance_date"`
	Source      string  `json:"source"`
	PointInTime bool    `json:"point_in_time"`
}

// Adjust reuses tdx's affine adjustment, anchored at asOf, excluding later events.
func Adjust(bars []Bar, actions []*protocol.Gbbq, mode, asOf string) ([]Bar, error) {
	if mode != "none" && mode != "qfq" && mode != "hfq" {
		return nil, fmt.Errorf("adjust must be none, qfq or hfq")
	}
	out := make([]Bar, 0, len(bars))
	ks := protocol.Klines{}
	xs := protocol.XRXDs{}
	for _, b := range bars {
		if b.Date > asOf {
			continue
		}
		out = append(out, b)
		t, err := parseDate(b.Date)
		if err != nil {
			return nil, err
		}
		t = t.Add(15 * time.Hour)
		ks = append(ks, &protocol.Kline{Time: t, Open: protocol.Price(milli(b.Open)), High: protocol.Price(milli(b.High)), Low: protocol.Price(milli(b.Low)), Close: protocol.Price(milli(b.Close))})
	}
	if mode == "none" {
		return out, nil
	}
	for _, a := range actions {
		if day(a.Time) <= asOf && (a.Category == 11 || a.Category == 12) {
			return nil, fmt.Errorf("unsupported share consolidation/fund conversion at %s", day(a.Time))
		}
		if day(a.Time) <= asOf && a.IsXRXD() {
			x := a.XRXD()
			t, _ := parseDate(day(a.Time))
			x.Time = t.Add(15 * time.Hour)
			xs = append(xs, x)
		}
	}
	fs := xs.Pre(ks).Factors()
	if mode == "qfq" {
		ks = protocol.ApplyQFQ(ks, fs)
	} else {
		ks = protocol.ApplyHFQ(ks, fs)
	}
	for i, k := range ks {
		out[i].Open = k.Open.Float64()
		out[i].High = k.High.Float64()
		out[i].Low = k.Low.Float64()
		out[i].Close = k.Close.Float64()
		// Existing tdx price helpers round to cents. Funds need milli-yuan;
		// keep the same affine factors while retaining their trading precision.
		if kind(out[i].Symbol) == "etf" {
			f := fs[i]
			mul, add := f.QFQMul, f.QFQAdd
			if mode == "hfq" {
				mul, add = f.HFQMul, f.HFQAdd
			}
			convert := func(v float64) float64 { return math.Round((mul*v+add)*1000) / 1000 }
			out[i].Open = convert(bars[i].Open)
			out[i].High = convert(bars[i].High)
			out[i].Low = convert(bars[i].Low)
			out[i].Close = convert(bars[i].Close)
		}
	}
	return out, nil
}
