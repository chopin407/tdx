package research

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// MTFAConfig freezes the executable defaults from the trading manual.
type MTFAConfig struct {
	MinAverageAmount  float64 `json:"min_average_amount"`
	MinFloatMarketCap float64 `json:"min_float_market_cap"`
	MinListedBars     int     `json:"min_listed_bars"`
	MinCoverage       float64 `json:"min_coverage_pct"`
	MinMainlineScore  int     `json:"min_mainline_score"`
	MaxStopPct        float64 `json:"max_stop_pct"`
	MinRewardRisk     float64 `json:"min_reward_risk"`
	CostRate          float64 `json:"cost_rate"`
}

func DefaultMTFAConfig() MTFAConfig {
	return MTFAConfig{MinAverageAmount: 5e8, MinFloatMarketCap: 5e9, MinListedBars: 60,
		MinCoverage: 80, MinMainlineScore: 7, MaxStopPct: .05, MinRewardRisk: 2, CostRate: .0015}
}

func mergeMTFAConfig(in *MTFAConfig) MTFAConfig {
	out := DefaultMTFAConfig()
	if in == nil {
		return out
	}
	if in.MinAverageAmount > 0 {
		out.MinAverageAmount = in.MinAverageAmount
	}
	if in.MinFloatMarketCap > 0 {
		out.MinFloatMarketCap = in.MinFloatMarketCap
	}
	if in.MinListedBars > 0 {
		out.MinListedBars = in.MinListedBars
	}
	if in.MinCoverage > 0 {
		out.MinCoverage = in.MinCoverage
	}
	if in.MinMainlineScore > 0 {
		out.MinMainlineScore = in.MinMainlineScore
	}
	if in.MaxStopPct > 0 {
		out.MaxStopPct = in.MaxStopPct
	}
	if in.MinRewardRisk > 0 {
		out.MinRewardRisk = in.MinRewardRisk
	}
	if in.CostRate > 0 {
		out.CostRate = in.CostRate
	}
	return out
}

type ScoreItem struct {
	Name      string  `json:"name"`
	Value     any     `json:"value,omitempty"`
	Score     float64 `json:"score"`
	Weight    float64 `json:"weight"`
	Available bool    `json:"available"`
	Note      string  `json:"note,omitempty"`
}

type MTFAPlan struct {
	Symbol             string      `json:"symbol"`
	Name               string      `json:"name"`
	Industry           string      `json:"industry"`
	SignalDate         string      `json:"signal_date"`
	ModelVersion       string      `json:"model_version"`
	Setup              string      `json:"setup"`
	TradeAllowed       bool        `json:"trade_allowed"`
	Status             string      `json:"status"`
	Grade              string      `json:"grade"`
	ShapeScore         float64     `json:"shape_score"`
	CoveragePct        float64     `json:"coverage_pct"`
	MainlineScore      int         `json:"mainline_score"`
	Role               string      `json:"role"`
	MonthlyBackground  string      `json:"monthly_background"`
	MonthlyReason      string      `json:"monthly_reason"`
	CompletedMonth     string      `json:"completed_month"`
	Trigger            float64     `json:"trigger"`
	Stop               float64     `json:"stop"`
	Target             float64     `json:"target"`
	MaxBuy             float64     `json:"max_buy"`
	StopPct            float64     `json:"stop_pct"`
	RewardRisk         float64     `json:"reward_risk"`
	ATR14              float64     `json:"atr14"`
	FloatMarketCap     float64     `json:"float_market_cap"`
	AverageAmount20    float64     `json:"average_amount_20"`
	ConsolidationStart string      `json:"consolidation_start"`
	ConsolidationEnd   string      `json:"consolidation_end"`
	Support            float64     `json:"support"`
	Scores             []ScoreItem `json:"scores"`
	Missing            []string    `json:"missing"`
	Warnings           []string    `json:"warnings"`
	RejectReasons      []string    `json:"reject_reasons"`
}

type MTFAScanResult struct {
	Date           string            `json:"date"`
	DatasetVersion int64             `json:"dataset_version"`
	Config         MTFAConfig        `json:"config"`
	Universe       int               `json:"universe"`
	Profiled       int               `json:"profiled"`
	Excluded       map[string]string `json:"excluded"`
	Candidates     []MTFAPlan        `json:"candidates"`
	DataGaps       []string          `json:"data_gaps"`
}

type industryState struct {
	score     int
	role      map[string]string
	available bool
}

func meanAmount(b []Bar) float64 {
	if len(b) == 0 {
		return 0
	}
	v := 0.0
	for _, x := range b {
		v += x.Amount
	}
	return v / float64(len(b))
}

func smaBars(b []Bar, n int) float64 {
	if len(b) < n {
		return 0
	}
	v := 0.0
	for _, x := range b[len(b)-n:] {
		v += x.Close
	}
	return v / float64(n)
}

func wilderATR(b []Bar, n int) float64 {
	if len(b) < n+1 {
		return 0
	}
	trs := make([]float64, 0, len(b)-1)
	for i := 1; i < len(b); i++ {
		tr := math.Max(b[i].High-b[i].Low, math.Max(math.Abs(b[i].High-b[i-1].Close), math.Abs(b[i].Low-b[i-1].Close)))
		trs = append(trs, tr)
	}
	if len(trs) < n {
		return 0
	}
	atr := 0.0
	for _, v := range trs[:n] {
		atr += v / float64(n)
	}
	for _, v := range trs[n:] {
		atr = (atr*float64(n-1) + v) / float64(n)
	}
	return atr
}

func completeWeeks(b []Bar) []Bar {
	groups := map[string][]Bar{}
	keys := []string{}
	for _, x := range b {
		t, err := parseDate(x.Date)
		if err != nil {
			continue
		}
		y, w := t.ISOWeek()
		k := fmt.Sprintf("%04d-%02d", y, w)
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], x)
	}
	if len(keys) == 0 {
		return nil
	}
	lastDate, _ := parseDate(b[len(b)-1].Date)
	if lastDate.Weekday() != time.Friday {
		keys = keys[:len(keys)-1]
	}
	out := []Bar{}
	for _, k := range keys {
		g := groups[k]
		if len(g) == 0 {
			continue
		}
		v := Bar{Symbol: g[0].Symbol, Date: g[len(g)-1].Date, Open: g[0].Open, High: g[0].High, Low: g[0].Low, Close: g[len(g)-1].Close, Source: "daily-aggregate"}
		for _, x := range g {
			if x.High > v.High {
				v.High = x.High
			}
			if x.Low < v.Low {
				v.Low = x.Low
			}
			v.Volume += x.Volume
			v.Amount += x.Amount
		}
		out = append(out, v)
	}
	return out
}

func completeMonths(b []Bar) []Bar {
	groups := map[string][]Bar{}
	keys := []string{}
	for _, x := range b {
		if len(x.Date) < 7 {
			continue
		}
		k := x.Date[:7]
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], x)
	}
	// Without an exchange calendar the current calendar month is never treated
	// as completed. This is conservative and creates at most one-session lag.
	if len(keys) > 0 {
		keys = keys[:len(keys)-1]
	}
	out := []Bar{}
	for _, k := range keys {
		g := groups[k]
		v := Bar{Symbol: g[0].Symbol, Date: g[len(g)-1].Date, Open: g[0].Open, High: g[0].High, Low: g[0].Low, Close: g[len(g)-1].Close, Source: "daily-aggregate"}
		for _, x := range g {
			if x.High > v.High {
				v.High = x.High
			}
			if x.Low < v.Low {
				v.Low = x.Low
			}
			v.Volume += x.Volume
			v.Amount += x.Amount
		}
		out = append(out, v)
	}
	return out
}

func monthlyBackground(b []Bar) (label, reason, completed string) {
	m := completeMonths(b)
	if len(m) < 12 {
		return "unknown", "fewer than 12 completed calendar months", ""
	}
	last := m[len(m)-1]
	ma6 := smaBars(m, 6)
	ma12 := smaBars(m, 12)
	previous6 := smaBars(m[:len(m)-1], 6)
	completed = last.Date[:7]
	switch {
	case last.Close > ma6 && ma6 > ma12 && ma6 > previous6:
		return "rising", fmt.Sprintf("close %.2f > SMA6M %.2f > SMA12M %.2f and SMA6M rising", last.Close, ma6, ma12), completed
	case last.Close < ma6 && ma6 < ma12 && ma6 < previous6:
		return "declining", fmt.Sprintf("close %.2f < SMA6M %.2f < SMA12M %.2f and SMA6M falling", last.Close, ma6, ma12), completed
	default:
		return "sideways", fmt.Sprintf("mixed monthly structure: close %.2f, SMA6M %.2f, SMA12M %.2f", last.Close, ma6, ma12), completed
	}
}

func nearestPressure(b []Bar, end int, price float64) float64 {
	start := end - 120
	if start < 1 {
		start = 1
	}
	t := math.MaxFloat64
	for i := start + 1; i < end-1; i++ {
		if b[i].High >= b[i-1].High && b[i].High >= b[i+1].High && b[i].High > price && b[i].High < t {
			t = b[i].High
		}
	}
	if t == math.MaxFloat64 {
		return 0
	}
	return t
}

func grade(score, coverage float64) string {
	if coverage < 80 {
		return "数据不足"
	}
	if score >= 80 {
		return "S"
	}
	if score >= 70 {
		return "A"
	}
	if score >= 60 {
		return "B"
	}
	return "淘汰"
}

func addScore(items *[]ScoreItem, name string, value any, score, weight float64, available bool, note string) {
	*items = append(*items, ScoreItem{name, value, score, weight, available, note})
}

func evaluateMTFA(b []Bar, p InstrumentProfile, date string, rawClose float64, cfg MTFAConfig, ind industryState) (MTFAPlan, error) {
	v := MTFAPlan{Symbol: p.Symbol, Name: p.Name, Industry: p.Industry, SignalDate: date, ModelVersion: "MTF-A-v1.0/shape-v0.1", Setup: "weekly-uptrend-daily-pullback-restart", Status: "research-only", Role: ind.role[p.Symbol], Scores: []ScoreItem{}, Missing: []string{}, Warnings: []string{}, RejectReasons: []string{}}
	if len(b) < 130 {
		return v, fmt.Errorf("need at least 130 daily bars")
	}
	last := b[len(b)-1]
	v.MonthlyBackground, v.MonthlyReason, v.CompletedMonth = monthlyBackground(b)
	if v.MonthlyBackground == "unknown" {
		v.Warnings = append(v.Warnings, "monthly background unavailable; it is not a v1.0 hard gate")
	}
	if last.Date != date {
		return v, fmt.Errorf("no exact-date bar")
	}
	if p.Name == "" {
		v.Missing = append(v.Missing, "name")
	}
	if p.Industry == "" {
		v.Missing = append(v.Missing, "industry")
	}
	if p.IsST || strings.Contains(strings.ToUpper(p.Name), "ST") || strings.Contains(p.Name, "退") {
		v.RejectReasons = append(v.RejectReasons, "ST/delisting security")
	}
	v.AverageAmount20 = meanAmount(b[len(b)-20:])
	v.FloatMarketCap = p.FloatShares * rawClose
	if v.AverageAmount20 < cfg.MinAverageAmount {
		v.RejectReasons = append(v.RejectReasons, "20-day average amount below threshold")
	}
	if p.FloatShares <= 0 {
		v.Missing = append(v.Missing, "float_shares")
	} else if v.FloatMarketCap < cfg.MinFloatMarketCap {
		v.RejectReasons = append(v.RejectReasons, "float market cap below threshold")
	}
	if len(b) < cfg.MinListedBars {
		v.RejectReasons = append(v.RejectReasons, "insufficient listed bars")
	}
	if !p.PointInTime {
		v.Warnings = append(v.Warnings, "profile and float shares are current snapshots; historical use has look-ahead risk")
	}

	w := completeWeeks(b)
	weeklyOK := false
	ma10w, ma20w := 0.0, 0.0
	if len(w) >= 23 {
		ma10w = smaBars(w, 10)
		ma20w = smaBars(w, 20)
		old := smaBars(w[:len(w)-3], 10)
		weeklyOK = w[len(w)-1].Close > ma10w && w[len(w)-1].Close > ma20w && ma10w > old
	}
	addScore(&v.Scores, "weekly_trend", weeklyOK, map[bool]float64{true: 4, false: 0}[weeklyOK], 8, len(w) >= 23, fmt.Sprintf("MA10W %.3f MA20W %.3f", ma10w, ma20w))
	if len(w) >= 20 && ma10w > ma20w {
		v.Scores[len(v.Scores)-1].Score += 2
	}
	if len(w) > 0 && ma10w > 0 {
		d := w[len(w)-1].Close/ma10w - 1
		if d <= .10 {
			v.Scores[len(v.Scores)-1].Score += 2
		} else if d <= .15 {
			v.Scores[len(v.Scores)-1].Score++
		}
	}
	if !weeklyOK {
		v.RejectReasons = append(v.RejectReasons, "weekly trend permission failed")
	}

	ma20 := smaBars(b, 20)
	ma60 := smaBars(b, 60)
	old20 := smaBars(b[:len(b)-5], 20)
	old2010 := smaBars(b[:len(b)-10], 20)
	dailyOK := last.Close > ma20 && ma20 > old20
	dailyScore := 0.0
	if dailyOK {
		dailyScore = 3
	}
	if ma20 > ma60 {
		dailyScore += 2
	}
	if ma20 > old2010 {
		dailyScore += 2
	} else if ma20 > old20 {
		dailyScore++
	}
	addScore(&v.Scores, "daily_trend", dailyOK, dailyScore, 7, true, fmt.Sprintf("MA20 %.3f MA60 %.3f", ma20, ma60))
	if !dailyOK {
		v.RejectReasons = append(v.RejectReasons, "daily trend permission failed")
	}
	bias := last.Close/ma20 - 1
	biasScore := 0.0
	if bias <= .03 {
		biasScore = 5
	} else if bias <= .05 {
		biasScore = 3
	} else if bias <= .08 {
		biasScore = 1
	}
	addScore(&v.Scores, "ma20_bias", bias, biasScore, 5, true, "")

	atr := wilderATR(b, 14)
	v.ATR14 = atr
	consLen := 0
	support := 0.0
	for n := 8; n >= 3; n-- {
		start := len(b) - 1 - n
		if start < 1 {
			continue
		}
		s := math.Min(b[start-1].Low, b[start].Low)
		ok := true
		for i := start; i < len(b); i++ {
			if b[i].Low < s {
				ok = false
				break
			}
		}
		if ok {
			consLen = n
			support = s
			v.ConsolidationStart = b[start].Date
			v.ConsolidationEnd = b[len(b)-2].Date
			break
		}
	}
	if consLen == 0 {
		v.RejectReasons = append(v.RejectReasons, "no deterministic 3-8 day consolidation")
	}
	v.Support = support
	avg3 := meanAmount(b[len(b)-4 : len(b)-1])
	avg10 := meanAmount(b[len(b)-14 : len(b)-4])
	contraction := 0.0
	if avg10 > 0 {
		contraction = avg3 / avg10
	}
	contractScore := 0.0
	if contraction <= .60 {
		contractScore = 8
	} else if contraction <= .70 {
		contractScore = 6
	} else if contraction <= .80 {
		contractScore = 4
	}
	addScore(&v.Scores, "contraction", contraction, contractScore, 8, avg10 > 0, "")
	if contraction > .80 || avg10 == 0 {
		v.RejectReasons = append(v.RejectReasons, "contraction window failed")
	}
	clv := 0.0
	if last.High > last.Low {
		clv = (last.Close - last.Low) / (last.High - last.Low)
	}
	clvScore := 0.0
	if clv >= .85 {
		clvScore = 6
	} else if clv >= .75 {
		clvScore = 4
	} else if clv >= 2.0/3 {
		clvScore = 2
	}
	addScore(&v.Scores, "close_location", clv, clvScore, 6, last.High > last.Low, "")
	if clv < 2.0/3 {
		v.RejectReasons = append(v.RejectReasons, "close location below two thirds")
	}
	prevHigh := b[len(b)-2].High
	breakout := last.Close/prevHigh - 1
	breakScore := 0.0
	if breakout > 0 && breakout <= .03 {
		breakScore = 5
	} else if breakout <= .05 && breakout > 0 {
		breakScore = 3
	} else if breakout <= .08 && breakout > 0 {
		breakScore = 1
	}
	addScore(&v.Scores, "breakout", breakout, breakScore, 5, true, "")
	if last.Close <= prevHigh {
		v.RejectReasons = append(v.RejectReasons, "signal close did not exceed prior high")
	}
	expand := 0.0
	if avg3 > 0 {
		expand = last.Amount / avg3
	}
	expScore := 0.0
	if expand >= 1 && expand <= 1.8 {
		expScore = 4
	} else if expand >= .8 && expand < 1 {
		expScore = 2
	} else if expand > 1.8 && expand <= 2.5 {
		expScore = 1
	}
	addScore(&v.Scores, "signal_amount_expansion", expand, expScore, 4, avg3 > 0, "")
	upper := 0.0
	if atr > 0 {
		upper = (last.High - last.Close) / atr
	}
	upperScore := 0.0
	if upper <= .3 {
		upperScore = 2
	} else if upper <= .6 {
		upperScore = 1
	}
	addScore(&v.Scores, "upper_shadow_atr", upper, upperScore, 2, atr > 0, "")

	v.Trigger = prevHigh
	v.Stop = support - .3*atr
	if v.Stop <= 0 || v.Trigger <= v.Stop {
		v.RejectReasons = append(v.RejectReasons, "invalid structural stop")
	}
	v.Target = nearestPressure(b, len(b)-consLen-1, v.Trigger)
	if v.Target <= v.Trigger {
		v.RejectReasons = append(v.RejectReasons, "no point-in-time overhead target")
	}
	if v.Target > v.Trigger && v.Stop > 0 {
		v.MaxBuy = (v.Target + 2*v.Stop - 3*cfg.CostRate*v.Trigger) / 3
		capByStop := v.Stop / (1 - cfg.MaxStopPct)
		if v.MaxBuy > capByStop {
			v.MaxBuy = capByStop
		}
		if v.MaxBuy < v.Trigger {
			v.RejectReasons = append(v.RejectReasons, "Pmax below trigger")
		}
		v.StopPct = (v.Trigger - v.Stop) / v.Trigger
		v.RewardRisk = (v.Target - v.Trigger - cfg.CostRate*v.Trigger) / (v.Trigger - v.Stop + cfg.CostRate*v.Trigger)
	}
	rSpace := v.RewardRisk
	rScore := 0.0
	if rSpace >= 3 {
		rScore = 5
	} else if rSpace >= 2.5 {
		rScore = 3
	} else if rSpace >= 2 {
		rScore = 1
	}
	addScore(&v.Scores, "pressure_r_space", rSpace, rScore, 5, v.Target > v.Trigger, "")
	if v.StopPct > cfg.MaxStopPct {
		v.RejectReasons = append(v.RejectReasons, "stop distance above 5 percent")
	}
	if v.RewardRisk < cfg.MinRewardRisk {
		v.RejectReasons = append(v.RejectReasons, "reward/risk below 2")
	}
	liqScore := 0.0
	if v.AverageAmount20 >= 2e9 {
		liqScore = 5
	} else if v.AverageAmount20 >= 1e9 {
		liqScore = 4
	} else if v.AverageAmount20 >= 5e8 {
		liqScore = 3
	}
	addScore(&v.Scores, "average_amount_20", v.AverageAmount20, liqScore, 5, true, "")
	stopScore := 0.0
	if v.StopPct <= .03 {
		stopScore = 3
	} else if v.StopPct <= .04 {
		stopScore = 2
	} else if v.StopPct <= .05 {
		stopScore = 1
	}
	addScore(&v.Scores, "stop_distance", v.StopPct, stopScore, 3, v.StopPct > 0, "")
	addScore(&v.Scores, "trade_accessibility", nil, 0, 3, false, "daily bars cannot reconstruct queue priority")
	addScore(&v.Scores, "turnover_and_slippage", nil, 0, 4, false, "historical turnover and order-book slippage unavailable")
	addScore(&v.Scores, "industry_mainline", ind.score, float64(minInt(ind.score, 8)), 8, ind.available, "daily industry proxy; no historical intraday snapshots")
	roleScore := map[string]float64{"leader": 6, "capacity_core": 5, "trend_core": 5, "front": 3}[v.Role]
	addScore(&v.Scores, "industry_role", v.Role, roleScore, 6, v.Role != "", "")
	addScore(&v.Scores, "industry_relative_strength", nil, 0, 4, false, "historical fixed benchmark mapping pending")
	addScore(&v.Scores, "etf_confirmation", nil, 0, 2, false, "industry ETF mapping unavailable")
	continuity := 0
	if b[len(b)-2].Amount >= b[len(b)-3].Amount {
		continuity++
	}
	if b[len(b)-3].Amount >= b[len(b)-4].Amount {
		continuity++
	}
	addScore(&v.Scores, "amount_continuity", continuity, float64(continuity*2), 4, true, "")
	addScore(&v.Scores, "same_time_volume", nil, 0, 4, false, "historical same-time snapshots unavailable")
	r1 := last.Close/b[len(b)-2].Close - 1
	r3 := last.Close/b[len(b)-4].Close - 1
	r5 := last.Close/b[len(b)-6].Close - 1
	composite := .2*r1 + .4*r3 + .4*r5
	relScore := 0.0
	if composite >= .08 {
		relScore = 4
	} else if composite >= .04 {
		relScore = 3
	} else if composite > 0 {
		relScore = 1
	}
	addScore(&v.Scores, "relative_strength_1_3_5_proxy", composite, relScore, 4, true, "absolute proxy until cross-sectional percentiles persist")
	addScore(&v.Scores, "supplier_main_net_ratio", nil, 0, 1, false, "unavailable")
	addScore(&v.Scores, "shareholder_count_trend", nil, 0, 2, false, "announcement-time history unavailable")

	total, covered := 0.0, 0.0
	for _, x := range v.Scores {
		total += x.Score
		if x.Available {
			covered += x.Weight
		}
	}
	v.ShapeScore = math.Round(total*100) / 100
	v.CoveragePct = math.Round(covered*100) / 100
	v.Grade = grade(v.ShapeScore, v.CoveragePct)
	v.MainlineScore = ind.score
	if ind.score < cfg.MinMainlineScore {
		v.RejectReasons = append(v.RejectReasons, "mainline score below threshold")
	}
	if v.CoveragePct < cfg.MinCoverage {
		v.RejectReasons = append(v.RejectReasons, "coverage below threshold")
	}
	v.TradeAllowed = len(v.RejectReasons) == 0
	if v.TradeAllowed {
		v.Status = "pending-next-day-validation"
	}
	return v, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildIndustryStates(d *Dataset, date string) map[string]industryState {
	type member struct {
		symbol                                  string
		amount, previousAmount, ret1, ret3, clv float64
	}
	groups := map[string][]member{}
	for symbol, b := range d.Bars {
		p, ok := d.Profiles[symbol]
		if !ok || p.Industry == "" || len(b) < 6 || b[len(b)-1].Date != date || !d.ActionsChecked[symbol] {
			continue
		}
		b, err := Adjust(b, d.Actions[symbol], "qfq", date)
		if err != nil || len(b) < 6 {
			continue
		}
		x := b[len(b)-1]
		clv := 0.5
		if x.High > x.Low {
			clv = (x.Close - x.Low) / (x.High - x.Low)
		}
		groups[p.Industry] = append(groups[p.Industry], member{symbol, meanAmount(b[len(b)-5:]), meanAmount(b[len(b)-6 : len(b)-1]), x.Close/b[len(b)-2].Close - 1, x.Close/b[len(b)-4].Close - 1, clv})
	}
	out := map[string]industryState{}
	for industry, members := range groups {
		if len(members) < 5 {
			continue
		}
		sort.Slice(members, func(i, j int) bool { return members[i].amount > members[j].amount })
		adv := 0
		rets := []float64{}
		for _, m := range members {
			if m.ret1 > 0 {
				adv++
			}
			rets = append(rets, m.ret1)
		}
		sort.Float64s(rets)
		median := rets[len(rets)/2]
		score := 0
		if median > 0 {
			score += 2
		}
		if float64(adv)/float64(len(members)) >= .6 {
			score++
		}
		if members[0].ret1 > 0 && members[0].clv >= 2.0/3 {
			score += 2
		}
		sum := 0.0
		prev := 0.0
		for _, m := range members {
			sum += m.amount
			prev += m.previousAmount
		}
		if prev > 0 && sum/prev >= 1.05 {
			score += 2
		}
		positive3 := 0
		for _, m := range members {
			if m.ret3 > 0 {
				positive3++
			}
		}
		if float64(positive3)/float64(len(members)) >= .6 {
			score += 1
		}
		roles := map[string]string{}
		for i, m := range members {
			if i == 0 {
				roles[m.symbol] = "leader"
			} else if i < 3 {
				roles[m.symbol] = "capacity_core"
			} else if m.ret3 > 0 {
				roles[m.symbol] = "front"
			}
		}
		out[industry] = industryState{score: score, role: roles, available: true}
	}
	return out
}

func ScanMTFA(d *Dataset, date string, cfg MTFAConfig) (MTFAScanResult, error) {
	r := MTFAScanResult{Date: date, DatasetVersion: d.Version, Config: cfg, Excluded: map[string]string{}, Candidates: []MTFAPlan{}, DataGaps: []string{"historical ST/rule table unavailable", "historical industry membership unavailable", "historical float shares/market cap unavailable", "official exchange calendar unavailable", "historical intraday snapshots unavailable", "shareholder-count announcement history unavailable"}}
	if _, err := parseDate(date); err != nil {
		return r, err
	}
	industries := buildIndustryStates(d, date)
	for symbol, bars := range d.Bars {
		if kind(symbol) != "stock" {
			continue
		}
		r.Universe++
		p, ok := d.Profiles[symbol]
		if !ok {
			r.Excluded[symbol] = "current instrument profile missing; run online update"
			continue
		}
		r.Profiled++
		if p.IsST || strings.Contains(strings.ToUpper(p.Name), "ST") || strings.Contains(p.Name, "退") {
			r.Excluded[symbol] = "ST/delisting security"
			continue
		}
		if len(bars) == 0 || bars[len(bars)-1].Date != date {
			r.Excluded[symbol] = "no exact-date bar"
			continue
		}
		if !d.ActionsChecked[symbol] {
			r.Excluded[symbol] = "corporate actions unchecked"
			continue
		}
		adj, err := Adjust(bars, d.Actions[symbol], "qfq", date)
		if err != nil {
			r.Excluded[symbol] = err.Error()
			continue
		}
		plan, err := evaluateMTFA(adj, p, date, bars[len(bars)-1].Close, cfg, industries[p.Industry])
		if err != nil {
			r.Excluded[symbol] = err.Error()
			continue
		}
		if plan.TradeAllowed || plan.ShapeScore >= 60 {
			r.Candidates = append(r.Candidates, plan)
		} else {
			r.Excluded[symbol] = strings.Join(plan.RejectReasons, "; ")
		}
	}
	sort.Slice(r.Candidates, func(i, j int) bool {
		if r.Candidates[i].TradeAllowed != r.Candidates[j].TradeAllowed {
			return r.Candidates[i].TradeAllowed
		}
		if r.Candidates[i].ShapeScore != r.Candidates[j].ShapeScore {
			return r.Candidates[i].ShapeScore > r.Candidates[j].ShapeScore
		}
		return r.Candidates[i].Symbol < r.Candidates[j].Symbol
	})
	return r, nil
}
