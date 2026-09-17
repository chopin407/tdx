package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// BacktestConfig explicitly declares friction and execution assumptions.
type BacktestConfig struct {
	Symbol        string  `json:"symbol"`
	Start         string  `json:"start"`
	End           string  `json:"end"`
	Capital       float64 `json:"capital"`
	Commission    float64 `json:"commission"`
	MinCommission float64 `json:"min_commission"`
	SellTax       float64 `json:"sell_tax"`
	SlippageBPS   float64 `json:"slippage_bps"`
	LimitPct      float64 `json:"limit_pct"`
	Lot           int     `json:"lot"`
}

func (c BacktestConfig) validate() error {
	if kind(c.Symbol) != "stock" {
		return fmt.Errorf("v1 backtest supports A shares only")
	}
	a, err := parseDate(c.Start)
	if err != nil {
		return err
	}
	b, err := parseDate(c.End)
	if err != nil || b.Before(a) {
		return fmt.Errorf("invalid start/end")
	}
	for _, v := range []float64{c.Capital, c.Commission, c.MinCommission, c.SellTax, c.SlippageBPS, c.LimitPct} {
		if !finite(v) || v < 0 {
			return fmt.Errorf("invalid backtest numeric parameter")
		}
	}
	if c.Capital <= 0 || c.Lot < 1 || c.Lot > 10000 || c.LimitPct <= 0 || c.LimitPct > 1 || c.Commission > 0.1 || c.SellTax > 0.1 || c.SlippageBPS > 1000 {
		return fmt.Errorf("invalid capital, lot, price limit or cost")
	}
	return nil
}

type Trade struct {
	Date       string  `json:"date"`
	SignalDate string  `json:"signal_date"`
	Side       string  `json:"side"`
	Price      float64 `json:"price"`
	Shares     float64 `json:"shares"`
	Fee        float64 `json:"fee"`
}
type EquityPoint struct {
	Date      string  `json:"date"`
	Equity    float64 `json:"equity"`
	Benchmark float64 `json:"benchmark"`
	Cash      float64 `json:"cash"`
	Shares    float64 `json:"shares"`
}
type BacktestResult struct {
	Strategy        Strategy       `json:"strategy"`
	Config          BacktestConfig `json:"config"`
	Version         int64          `json:"dataset_version"`
	DataHash        string         `json:"data_sha256"`
	Engine          string         `json:"engine"`
	Return          float64        `json:"return_pct"`
	Annualized      float64        `json:"annualized_return_pct"`
	MaxDrawdown     float64        `json:"max_drawdown_pct"`
	BenchmarkReturn float64        `json:"benchmark_return_pct"`
	Trades          []Trade        `json:"trades"`
	Equity          []EquityPoint  `json:"equity"`
	Blocked         []string       `json:"blocked"`
	Notes           []string       `json:"notes"`
}

// Backtest uses only prior-bar information for next-open orders. A rejected order
// is re-evaluated at the next close; there is no same-day round trip or forced exit.
func Backtest(d *Dataset, s Strategy, c BacktestConfig) (BacktestResult, error) {
	r := BacktestResult{Strategy: s, Config: c, Version: d.Version, Engine: "daily-v1", Trades: []Trade{}, Equity: []EquityPoint{}, Blocked: []string{}, Notes: []string{
		"Single-stock research simulation. Signals use point-in-time anchored adjusted prices; fills use raw next available open.",
		"Fixed user-specified fees and limit ratio are assumptions, not a historical exchange rule database. ST, IPO exceptions and historical universe are not available.",
		"Zero-volume and one-price bars cannot fill; buys at upper and sells at lower limit are blocked conservatively. Missing bars cannot distinguish suspension from data loss.",
		"Cash dividends and bonus shares are credited on ex-date (simplified; no dividend tax). Rights issues and share consolidation during the test are rejected.",
		"Benchmark is frictionless buy-and-hold of this stock. Open positions remain marked to final close; no fabricated last-day liquidation.",
	}}
	if err := s.Validate(); err != nil {
		return r, err
	}
	if err := c.validate(); err != nil {
		return r, err
	}
	if !d.ActionsChecked[c.Symbol] {
		return r, fmt.Errorf("corporate actions must be fetched successfully before backtesting")
	}
	bars := d.Bars[c.Symbol]
	if len(bars) == 0 {
		return r, fmt.Errorf("no history")
	}
	first := -1
	last := -1
	for i, b := range bars {
		if b.Date >= c.Start && b.Date <= c.End {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || last-first < 1 {
		return r, fmt.Errorf("need at least two test bars")
	}
	if first < s.Warmup() {
		return r, fmt.Errorf("need %d warmup bars before start", s.Warmup())
	}
	actions := d.Actions[c.Symbol]
	for _, a := range actions {
		date := day(a.Time)
		if date >= bars[first].Date && date <= bars[last].Date && ((a.Category == 1 && a.C4 != 0) || a.Category == 11 || a.Category == 12) {
			return r, fmt.Errorf("unsupported rights issue/consolidation at %s; narrow test interval", date)
		}
	}
	payload, _ := json.Marshal(struct {
		Bars    []Bar
		Actions any
	}{bars[:last+1], actions})
	hash := sha256.Sum256(payload)
	r.DataHash = hex.EncodeToString(hash[:])
	cash := c.Capital
	shares := 0.0
	peak := c.Capital
	benchmarkShares := c.Capital / bars[first].Open
	benchmarkCash := 0.0
	for i := first; i <= last; i++ {
		b := bars[i]
		prev := bars[i-1]
		reference := prev.Close
		for _, a := range actions {
			date := day(a.Time)
			if date > prev.Date && date <= b.Date && a.Category == 1 {
				m := 1 + a.C3/10
				div := a.C1 / 10
				if m <= 0 {
					return r, fmt.Errorf("invalid bonus ratio")
				}
				cash += shares * div
				shares *= m
				reference = (reference - div) / m
				if i > first {
					benchmarkCash += benchmarkShares * div
					benchmarkShares *= m
				}
			}
		}
		// Recompute just the warmup window using events effective by signal day.
		start := i - s.Warmup()
		window, err := Adjust(bars[start:i], actions, "qfq", prev.Date)
		if err != nil {
			return r, err
		}
		sig := Evaluate(s, window)
		buy := shares == 0 && sig.Entry
		sell := shares > 0 && sig.Exit
		if buy || sell {
			side := "buy"
			if sell {
				side = "sell"
			}
			reason := ""
			if b.Volume == 0 {
				reason = "zero volume"
			} else if b.High == b.Low {
				reason = "one-price bar"
			} else if buy && b.Open >= math.Round(reference*(1+c.LimitPct)*100)/100-0.0001 {
				reason = "upper limit"
			} else if sell && b.Open <= math.Round(reference*(1-c.LimitPct)*100)/100+0.0001 {
				reason = "lower limit"
			}
			price := b.Open * (1 + c.SlippageBPS/10000)
			if sell {
				price = b.Open * (1 - c.SlippageBPS/10000)
			}
			if price > b.High || price < b.Low {
				reason = "slippage price outside observed bar"
			}
			if reason != "" {
				r.Blocked = append(r.Blocked, b.Date+" "+side+": "+reason)
			} else if buy {
				qty := math.Floor((cash-c.MinCommission)/(price*(1+c.Commission))/float64(c.Lot)) * float64(c.Lot)
				minQty := float64(c.Lot)
				if strings.HasPrefix(c.Symbol, "sh688") && minQty < 200 {
					minQty = 200
				}
				if qty >= minQty {
					fee := math.Max(c.MinCommission, qty*price*c.Commission)
					if qty*price+fee <= cash {
						cash -= qty*price + fee
						shares = qty
						r.Trades = append(r.Trades, Trade{b.Date, prev.Date, side, price, qty, fee})
					}
				}
			} else {
				fee := math.Max(c.MinCommission, shares*price*c.Commission) + shares*price*c.SellTax
				cash += shares*price - fee
				r.Trades = append(r.Trades, Trade{b.Date, prev.Date, side, price, shares, fee})
				shares = 0
			}
		}
		equity := cash + shares*b.Close
		benchmark := benchmarkCash + benchmarkShares*b.Close
		if equity > peak {
			peak = equity
		}
		dd := (peak - equity) / peak * 100
		if dd > r.MaxDrawdown {
			r.MaxDrawdown = dd
		}
		r.Equity = append(r.Equity, EquityPoint{b.Date, equity, benchmark, cash, shares})
	}
	endEquity := r.Equity[len(r.Equity)-1]
	r.Return = (endEquity.Equity/c.Capital - 1) * 100
	r.BenchmarkReturn = (endEquity.Benchmark/c.Capital - 1) * 100
	a, _ := parseDate(r.Equity[0].Date)
	b, _ := parseDate(endEquity.Date)
	years := (b.Sub(a).Hours()/24 + 1) / 365.25
	if endEquity.Equity > 0 {
		r.Annualized = (math.Pow(endEquity.Equity/c.Capital, 1/years) - 1) * 100
		if !finite(r.Annualized) {
			r.Annualized = 0
		}
	}
	return r, nil
}
