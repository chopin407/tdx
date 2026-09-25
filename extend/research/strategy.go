package research

import (
	"fmt"
	"sort"
)

// Strategy is the shared, versioned signal definition for screening and tests.
// ma_trend and breakout are daily rules; weekly patterns require completed weeks.
type Strategy struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Fast      int     `json:"fast"`
	Slow      int     `json:"slow"`
	Lookback  int     `json:"lookback"`
	MinAmount float64 `json:"min_amount"`
}

func (s Strategy) Validate() error {
	if !idRE.MatchString(s.ID) {
		return fmt.Errorf("strategy id: use 1-64 letters, digits, _ or -")
	}
	if s.Kind != "ma_trend" && s.Kind != "breakout" && s.Kind != "weekly_cup_handle" && s.Kind != "weekly_double_bottom" && s.Kind != "weekly_platform_hold" {
		return fmt.Errorf("kind must be ma_trend, breakout, weekly_cup_handle, weekly_double_bottom or weekly_platform_hold")
	}
	if s.Fast < 1 || s.Slow <= s.Fast || s.Slow > 500 || s.Lookback < 1 || s.Lookback > 500 || !finite(s.MinAmount) || s.MinAmount < 0 {
		return fmt.Errorf("require 1 <= fast < slow <= 500, 1 <= lookback <= 500, min_amount >= 0")
	}
	return nil
}
func (s Strategy) Warmup() int {
	if s.Kind == "weekly_cup_handle" || s.Kind == "weekly_double_bottom" || s.Kind == "weekly_platform_hold" {
		return 160 // Enough history for bounded weekly pattern search.
	}
	n := s.Slow
	if s.Kind == "breakout" && s.Lookback+1 > n {
		n = s.Lookback + 1
	}
	return n
}

// Signal describes a close-of-day decision, never an execution at that close.
type Signal struct {
	Symbol string  `json:"symbol"`
	Date   string  `json:"date"`
	Entry  bool    `json:"entry"`
	Exit   bool    `json:"exit"`
	Ready  bool    `json:"ready"`
	Reason string  `json:"reason"`
	FastMA float64 `json:"fast_ma"`
	SlowMA float64 `json:"slow_ma"`
}

func Evaluate(s Strategy, bars []Bar) Signal {
	v := Signal{}
	if len(bars) == 0 {
		return v
	}
	last := bars[len(bars)-1]
	v.Symbol = last.Symbol
	v.Date = last.Date
	if len(bars) < s.Warmup() {
		v.Reason = "insufficient warmup"
		return v
	}
	v.Ready = true
	if s.Kind == "weekly_cup_handle" {
		return evaluateWeeklyCupHandle(s, bars)
	}
	if s.Kind == "weekly_double_bottom" {
		return evaluateWeeklyDoubleBottom(s, bars)
	}
	if s.Kind == "weekly_platform_hold" {
		return evaluateWeeklyPlatformHold(s, bars)
	}
	for _, b := range bars[len(bars)-s.Fast:] {
		v.FastMA += b.Close / float64(s.Fast)
	}
	for _, b := range bars[len(bars)-s.Slow:] {
		v.SlowMA += b.Close / float64(s.Slow)
	}
	if s.Kind == "ma_trend" {
		v.Entry = v.FastMA > v.SlowMA
		v.Exit = v.FastMA <= v.SlowMA
		v.Reason = fmt.Sprintf("MA%d=%.3f, MA%d=%.3f", s.Fast, v.FastMA, s.Slow, v.SlowMA)
	} else {
		high := 0.0
		for _, b := range bars[len(bars)-s.Lookback-1 : len(bars)-1] {
			if b.High > high {
				high = b.High
			}
		}
		v.Entry = last.Close > high
		v.Exit = last.Close < v.FastMA
		v.Reason = fmt.Sprintf("close=%.3f, prior_%d_high=%.3f, exit_MA=%.3f", last.Close, s.Lookback, high, v.FastMA)
	}
	if last.Amount < s.MinAmount || last.Volume == 0 {
		v.Entry = false
		v.Reason += "; entry liquidity filter"
	}
	return v
}

type ScreenResult struct {
	Strategy   Strategy          `json:"strategy"`
	Date       string            `json:"date"`
	Version    int64             `json:"dataset_version"`
	Candidates []Signal          `json:"candidates"`
	Excluded   map[string]string `json:"excluded"`
	Universe   int               `json:"universe"`
}

// Screen requires exact-date data and checked actions, and screens A shares only.
func Screen(d *Dataset, s Strategy, date string) (ScreenResult, error) {
	r := ScreenResult{Strategy: s, Date: date, Version: d.Version, Candidates: []Signal{}, Excluded: map[string]string{}}
	if err := s.Validate(); err != nil {
		return r, err
	}
	if _, err := parseDate(date); err != nil {
		return r, err
	}
	for symbol, bars := range d.Bars {
		if kind(symbol) != "stock" {
			continue
		}
		r.Universe++
		if len(bars) == 0 || bars[len(bars)-1].Date != date {
			r.Excluded[symbol] = "no bar on requested date (suspension, delisting or missing data)"
			continue
		}
		if !d.ActionsChecked[symbol] {
			r.Excluded[symbol] = "corporate actions not checked"
			continue
		}
		adjusted, err := Adjust(bars, d.Actions[symbol], "qfq", date)
		if err != nil {
			r.Excluded[symbol] = err.Error()
			continue
		}
		sig := Evaluate(s, adjusted)
		if !sig.Ready {
			r.Excluded[symbol] = sig.Reason
		} else if sig.Entry {
			r.Candidates = append(r.Candidates, sig)
		}
	}
	sort.Slice(r.Candidates, func(i, j int) bool { return r.Candidates[i].Symbol < r.Candidates[j].Symbol })
	return r, nil
}

type ReviewResult struct {
	Date         string         `json:"date"`
	Version      int64          `json:"dataset_version"`
	Universe     int            `json:"universe"`
	Observed     int            `json:"observed"`
	Stale        int            `json:"stale_or_missing"`
	Unadjustable int            `json:"unadjustable"`
	Advances     int            `json:"advances"`
	Declines     int            `json:"declines"`
	Unchanged    int            `json:"unchanged"`
	Amount       float64        `json:"amount_yuan"`
	Top          []Mover        `json:"top_gainers"`
	Bottom       []Mover        `json:"top_losers"`
	Screens      []ScreenResult `json:"screens"`
	Notes        []string       `json:"notes"`
}
type Mover struct {
	Symbol string  `json:"symbol"`
	Change float64 `json:"change_pct"`
}

func Review(d *Dataset, date string, strategies []Strategy) (ReviewResult, error) {
	r := ReviewResult{Date: date, Version: d.Version, Top: []Mover{}, Bottom: []Mover{}, Screens: []ScreenResult{}, Notes: []string{"Universe is locally stored A shares, not a guaranteed complete historical exchange universe.", "No bar cannot distinguish suspension, delisting and missing data. Returns compare consecutive available bars; gaps may span multiple sessions."}}
	if _, err := parseDate(date); err != nil {
		return r, err
	}
	moves := []Mover{}
	for symbol, bars := range d.Bars {
		if kind(symbol) != "stock" {
			continue
		}
		r.Universe++
		if len(bars) == 0 || bars[len(bars)-1].Date != date {
			r.Stale++
			continue
		}
		r.Observed++
		r.Amount += bars[len(bars)-1].Amount
		if !d.ActionsChecked[symbol] {
			r.Unadjustable++
			continue
		}
		if len(bars) < 2 {
			continue
		}
		a, err := Adjust(bars[len(bars)-2:], d.Actions[symbol], "qfq", date)
		if err != nil {
			r.Unadjustable++
			continue
		}
		if a[0].Close <= 0 {
			continue
		}
		change := (a[1].Close/a[0].Close - 1) * 100
		if change > 0.00001 {
			r.Advances++
		} else if change < -0.00001 {
			r.Declines++
		} else {
			r.Unchanged++
		}
		moves = append(moves, Mover{symbol, change})
	}
	sort.Slice(moves, func(i, j int) bool {
		if moves[i].Change == moves[j].Change {
			return moves[i].Symbol < moves[j].Symbol
		}
		return moves[i].Change > moves[j].Change
	})
	n := 10
	if len(moves) < n {
		n = len(moves)
	}
	r.Top = append(r.Top, moves[:n]...)
	for i := len(moves) - 1; i >= len(moves)-n; i-- {
		r.Bottom = append(r.Bottom, moves[i])
	}
	for _, s := range strategies {
		v, err := Screen(d, s, date)
		if err != nil {
			return r, err
		}
		r.Screens = append(r.Screens, v)
	}
	return r, nil
}
