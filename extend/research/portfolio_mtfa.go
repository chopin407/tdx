package research

import (
	"fmt"
	"math"
	"sort"
)

type ExecutionInput struct {
	Time               string  `json:"time"`
	QuoteTime          string  `json:"quote_time"`
	Price              float64 `json:"price"`
	MarketPermission   bool    `json:"market_permission"`
	AccountPermission  bool    `json:"account_permission"`
	MainlineValid      bool    `json:"mainline_valid"`
	LiquidityNormal    bool    `json:"liquidity_normal"`
	HitStopBeforeEntry bool    `json:"hit_stop_before_entry"`
	MajorRisk          bool    `json:"major_risk"`
	SessionValid       bool    `json:"session_valid"`
}

type ExecutionDecision struct {
	State  string `json:"state"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}

func DecideExecution(p MTFAPlan, in ExecutionInput) ExecutionDecision {
	if !p.TradeAllowed {
		return ExecutionDecision{"cancelled", "do-not-order", "plan is not trade-eligible"}
	}
	if !in.SessionValid {
		return ExecutionDecision{"cancelled", "cancel-order", "plan expired or session is not the next trading session"}
	}
	if !in.MarketPermission || !in.AccountPermission || !in.MainlineValid || in.MajorRisk {
		return ExecutionDecision{"cancelled", "cancel-order", "permission or major-risk gate failed"}
	}
	if in.HitStopBeforeEntry || in.Price <= p.Stop {
		return ExecutionDecision{"cancelled", "cancel-order", "structural stop touched before entry"}
	}
	if !in.LiquidityNormal {
		return ExecutionDecision{"no-chase-wait", "wait", "liquidity abnormal"}
	}
	if in.Price > p.MaxBuy {
		return ExecutionDecision{"no-chase-wait", "wait", "price above Pmax"}
	}
	if in.Price >= p.Trigger {
		return ExecutionDecision{"order-allowed", "order-with-plan-size", "triggered within legal price band"}
	}
	return ExecutionDecision{"monitoring", "wait", "trigger not reached"}
}

type PortfolioConfig struct {
	Start                string     `json:"start"`
	End                  string     `json:"end"`
	Capital              float64    `json:"capital"`
	RiskPerTrade         float64    `json:"risk_per_trade"`
	MaxPositions         int        `json:"max_positions"`
	MaxIndustryPositions int        `json:"max_industry_positions"`
	MaxPositionPct       float64    `json:"max_position_pct"`
	Commission           float64    `json:"commission"`
	MinCommission        float64    `json:"min_commission"`
	SellTax              float64    `json:"sell_tax"`
	SlippageBPS          float64    `json:"slippage_bps"`
	Lot                  int        `json:"lot"`
	MTFA                 MTFAConfig `json:"mtfa"`
}

func DefaultPortfolioConfig() PortfolioConfig {
	return PortfolioConfig{Capital: 1e6, RiskPerTrade: .0025, MaxPositions: 3, MaxIndustryPositions: 2, MaxPositionPct: .3, Commission: .0003, MinCommission: 5, SellTax: .0005, SlippageBPS: 5, Lot: 100, MTFA: DefaultMTFAConfig()}
}

func mergePortfolioConfig(in *PortfolioConfig) PortfolioConfig {
	out := DefaultPortfolioConfig()
	if in == nil {
		return out
	}
	out.Start, out.End = in.Start, in.End
	if in.Capital > 0 {
		out.Capital = in.Capital
	}
	if in.RiskPerTrade > 0 {
		out.RiskPerTrade = in.RiskPerTrade
	}
	if in.MaxPositions > 0 {
		out.MaxPositions = in.MaxPositions
	}
	if in.MaxIndustryPositions > 0 {
		out.MaxIndustryPositions = in.MaxIndustryPositions
	}
	if in.MaxPositionPct > 0 {
		out.MaxPositionPct = in.MaxPositionPct
	}
	if in.Commission > 0 {
		out.Commission = in.Commission
	}
	if in.MinCommission > 0 {
		out.MinCommission = in.MinCommission
	}
	if in.SellTax > 0 {
		out.SellTax = in.SellTax
	}
	if in.SlippageBPS > 0 {
		out.SlippageBPS = in.SlippageBPS
	}
	if in.Lot > 0 {
		out.Lot = in.Lot
	}
	out.MTFA = mergeMTFAConfig(&in.MTFA)
	return out
}

type PortfolioTrade struct {
	Symbol     string  `json:"symbol"`
	Name       string  `json:"name"`
	Industry   string  `json:"industry"`
	SignalDate string  `json:"signal_date"`
	Date       string  `json:"date"`
	Side       string  `json:"side"`
	Reason     string  `json:"reason"`
	Price      float64 `json:"price"`
	Shares     float64 `json:"shares"`
	Fee        float64 `json:"fee"`
	R          float64 `json:"r"`
}
type PortfolioPoint struct {
	Date        string  `json:"date"`
	Equity      float64 `json:"equity"`
	Cash        float64 `json:"cash"`
	Positions   int     `json:"positions"`
	DrawdownPct float64 `json:"drawdown_pct"`
}
type PortfolioBacktestResult struct {
	Config         PortfolioConfig  `json:"config"`
	DatasetVersion int64            `json:"dataset_version"`
	ReturnPct      float64          `json:"return_pct"`
	MaxDrawdownPct float64          `json:"max_drawdown_pct"`
	Trades         []PortfolioTrade `json:"trades"`
	Equity         []PortfolioPoint `json:"equity"`
	Blocked        []string         `json:"blocked"`
	DataGaps       []string         `json:"data_gaps"`
	Signals        int              `json:"signals"`
	Filled         int              `json:"filled"`
	ClosedTrades   int              `json:"closed_trades"`
	WinRatePct     float64          `json:"win_rate_pct"`
	AverageR       float64          `json:"average_r"`
	ResearchOnly   bool             `json:"research_only"`
}

type mtfaPosition struct {
	plan                       MTFAPlan
	shares, entry, initialRisk float64
	entryDate                  string
	exitNext                   bool
}

func (c PortfolioConfig) validate() error {
	if _, e := parseDate(c.Start); e != nil {
		return e
	}
	if _, e := parseDate(c.End); e != nil || c.End < c.Start {
		return fmt.Errorf("invalid date range")
	}
	if c.Capital <= 0 || c.RiskPerTrade <= 0 || c.RiskPerTrade > .02 || c.MaxPositions < 1 || c.MaxPositions > 20 || c.MaxIndustryPositions < 1 || c.MaxPositionPct <= 0 || c.MaxPositionPct > 1 || c.Lot < 1 {
		return fmt.Errorf("invalid portfolio configuration")
	}
	return nil
}

func datasetThrough(d *Dataset, date string) *Dataset {
	x := &Dataset{Version: d.Version, Bars: map[string][]Bar{}, Actions: d.Actions, ActionsChecked: d.ActionsChecked, Profiles: d.Profiles}
	for s, b := range d.Bars {
		i := sort.Search(len(b), func(i int) bool { return b[i].Date > date })
		if i > 0 {
			x.Bars[s] = b[:i]
		}
	}
	return x
}

func barAt(b []Bar, date string) (Bar, bool) {
	i := sort.Search(len(b), func(i int) bool { return b[i].Date >= date })
	if i < len(b) && b[i].Date == date {
		return b[i], true
	}
	return Bar{}, false
}

func tradingDates(d *Dataset, start, end string) []string {
	set := map[string]bool{}
	for s, b := range d.Bars {
		if kind(s) != "stock" {
			continue
		}
		for _, x := range b {
			if x.Date >= start && x.Date <= end {
				set[x.Date] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for x := range set {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

func BacktestMTFAPortfolio(d *Dataset, c PortfolioConfig) (PortfolioBacktestResult, error) {
	r := PortfolioBacktestResult{Config: c, DatasetVersion: d.Version, Trades: []PortfolioTrade{}, Equity: []PortfolioPoint{}, Blocked: []string{}, DataGaps: []string{"current profile/float shares used historically", "historical ST and industry membership unavailable", "official exchange calendar unavailable", "daily OHLC cannot resolve all intraday paths", "historical star-map snapshots unavailable"}, ResearchOnly: true}
	if err := c.validate(); err != nil {
		return r, err
	}
	dates := tradingDates(d, c.Start, c.End)
	if len(dates) < 2 {
		return r, fmt.Errorf("insufficient trading dates")
	}
	cash := c.Capital
	peak := c.Capital
	positions := map[string]*mtfaPosition{}
	pending := []MTFAPlan{}
	for di, date := range dates {
		// Execute exits and prior-close plans at today's observable prices.
		for symbol, pos := range positions {
			raw, ok := barAt(d.Bars[symbol], date)
			if !ok {
				continue
			}
			if pos.exitNext {
				price := raw.Open * (1 - c.SlippageBPS/10000)
				fee := math.Max(c.MinCommission, pos.shares*price*c.Commission) + pos.shares*price*c.SellTax
				cash += pos.shares*price - fee
				rr := (price - pos.entry) * pos.shares / pos.initialRisk
				r.Trades = append(r.Trades, PortfolioTrade{Symbol: symbol, Name: pos.plan.Name, Industry: pos.plan.Industry, SignalDate: pos.plan.SignalDate, Date: date, Side: "sell", Reason: "MA10 close exit", Price: price, Shares: pos.shares, Fee: fee, R: rr})
				delete(positions, symbol)
				continue
			}
			if date > pos.entryDate && raw.Low <= pos.plan.Stop {
				price := pos.plan.Stop * (1 - c.SlippageBPS/10000)
				if raw.Open < pos.plan.Stop {
					price = raw.Open * (1 - c.SlippageBPS/10000)
				}
				fee := math.Max(c.MinCommission, pos.shares*price*c.Commission) + pos.shares*price*c.SellTax
				cash += pos.shares*price - fee
				rr := (price - pos.entry) * pos.shares / pos.initialRisk
				r.Trades = append(r.Trades, PortfolioTrade{Symbol: symbol, Name: pos.plan.Name, Industry: pos.plan.Industry, SignalDate: pos.plan.SignalDate, Date: date, Side: "sell", Reason: "hard stop", Price: price, Shares: pos.shares, Fee: fee, R: rr})
				delete(positions, symbol)
			}
		}
		if len(pending) > 0 && len(positions) < c.MaxPositions {
			sort.Slice(pending, func(i, j int) bool { return pending[i].ShapeScore > pending[j].ShapeScore })
			industryCount := map[string]int{}
			for _, p := range positions {
				industryCount[p.plan.Industry]++
			}
			for _, plan := range pending {
				if len(positions) >= c.MaxPositions {
					break
				}
				if _, ok := positions[plan.Symbol]; ok || industryCount[plan.Industry] >= c.MaxIndustryPositions {
					continue
				}
				raw, ok := barAt(d.Bars[plan.Symbol], date)
				if !ok {
					r.Blocked = append(r.Blocked, date+" "+plan.Symbol+" missing bar")
					continue
				}
				price := 0.0
				if raw.Open >= plan.Trigger && raw.Open <= plan.MaxBuy {
					price = raw.Open
				} else if raw.Open < plan.Trigger && raw.High >= plan.Trigger && raw.Low > plan.Stop {
					price = plan.Trigger
				} else if raw.High >= plan.Trigger && raw.Low <= plan.Stop {
					r.Blocked = append(r.Blocked, date+" "+plan.Symbol+" ambiguous trigger/stop path")
					continue
				} else {
					continue
				}
				price *= 1 + c.SlippageBPS/10000
				if price > plan.MaxBuy || price > raw.High {
					continue
				}
				riskPerShare := price - plan.Stop
				if riskPerShare <= 0 {
					continue
				}
				byRisk := math.Floor((c.Capital*c.RiskPerTrade/riskPerShare)/float64(c.Lot)) * float64(c.Lot)
				byCap := math.Floor((cash*c.MaxPositionPct/price)/float64(c.Lot)) * float64(c.Lot)
				shares := math.Min(byRisk, byCap)
				if shares < float64(c.Lot) {
					continue
				}
				fee := math.Max(c.MinCommission, shares*price*c.Commission)
				if shares*price+fee > cash {
					continue
				}
				cash -= shares*price + fee
				initialRisk := shares * riskPerShare
				positions[plan.Symbol] = &mtfaPosition{plan, shares, price, initialRisk, date, false}
				industryCount[plan.Industry]++
				r.Filled++
				r.Trades = append(r.Trades, PortfolioTrade{Symbol: plan.Symbol, Name: plan.Name, Industry: plan.Industry, SignalDate: plan.SignalDate, Date: date, Side: "buy", Reason: "MTF-A trigger", Price: price, Shares: shares, Fee: fee})
			}
		}
		pending = nil
		// Close-of-day exit evaluation.
		view := datasetThrough(d, date)
		for symbol, pos := range positions {
			bars, err := Adjust(view.Bars[symbol], view.Actions[symbol], "qfq", date)
			if err != nil {
				r.Blocked = append(r.Blocked, date+" "+symbol+" qfq exit unavailable: "+err.Error())
				continue
			}
			if len(bars) >= 10 && bars[len(bars)-1].Close < smaBars(bars, 10) {
				pos.exitNext = true
			}
		}
		// Signal generation at close, executable only on the next date.
		if di < len(dates)-1 {
			scan, err := ScanMTFA(datasetThrough(d, date), date, c.MTFA)
			if err != nil {
				return r, err
			}
			for _, p := range scan.Candidates {
				if p.TradeAllowed {
					pending = append(pending, p)
					r.Signals++
				}
			}
		}
		equity := cash
		for symbol, pos := range positions {
			if b, ok := barAt(d.Bars[symbol], date); ok {
				equity += pos.shares * b.Close
			}
		}
		if equity > peak {
			peak = equity
		}
		dd := (peak - equity) / peak * 100
		if dd > r.MaxDrawdownPct {
			r.MaxDrawdownPct = dd
		}
		r.Equity = append(r.Equity, PortfolioPoint{date, equity, cash, len(positions), dd})
	}
	if len(r.Equity) > 0 {
		r.ReturnPct = (r.Equity[len(r.Equity)-1].Equity/c.Capital - 1) * 100
	}
	wins, totalR := 0, 0.0
	for _, trade := range r.Trades {
		if trade.Side != "sell" {
			continue
		}
		r.ClosedTrades++
		totalR += trade.R
		if trade.R > 0 {
			wins++
		}
	}
	if r.ClosedTrades > 0 {
		r.WinRatePct = float64(wins) / float64(r.ClosedTrades) * 100
		r.AverageR = totalR / float64(r.ClosedTrades)
	}
	return r, nil
}
