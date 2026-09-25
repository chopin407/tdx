package research

import (
	"fmt"
	"math"
)

// evaluateWeeklyDoubleBottom searches completed weeks for two separated lows,
// a meaningful intervening neckline, and a first volume-backed close above it.
// A match is a research signal at the weekly close, not an execution price.
func evaluateWeeklyDoubleBottom(s Strategy, bars []Bar) Signal {
	v := Signal{}
	if len(bars) == 0 {
		return v
	}
	last := bars[len(bars)-1]
	v.Symbol, v.Date = last.Symbol, last.Date
	weeks := completedWeeks(bars)
	if len(weeks) < 11 || weeks[len(weeks)-1].Date != last.Date {
		v.Reason = "need at least 11 completed Friday weeks ending on signal date"
		return v
	}
	v.Ready = true
	if len(weeks) > 27 {
		weeks = weeks[len(weeks)-27:]
	}
	signal := weeks[len(weeks)-1]
	previous := weeks[:len(weeks)-1]
	avgVolume := 0.0
	for _, week := range previous[len(previous)-4:] {
		avgVolume += float64(week.Volume) / 4
	}
	if signal.Close <= signal.Open || signal.High <= signal.Low ||
		(signal.Close-signal.Low)/(signal.High-signal.Low) < 0.7 ||
		float64(signal.Volume) < 1.3*avgVolume || signal.Amount < s.MinAmount*3 {
		v.Reason = "signal week lacks bullish close, volume confirmation or liquidity"
		return v
	}
	best := -1.0
	for first := 2; first < len(previous)-4; first++ {
		low1 := previous[first].Low
		if low1 <= 0 || low1 > previous[first-1].Low || low1 > previous[first+1].Low {
			continue
		}
		leftPeak := 0.0
		for _, week := range previous[:first] {
			leftPeak = math.Max(leftPeak, week.High)
		}
		if leftPeak < low1*1.15 {
			continue // No prior decline into the first bottom.
		}
		for second := first + 3; second <= first+10 && second < len(previous); second++ {
			age := len(previous) - second
			if age < 2 || age > 5 {
				continue
			}
			low2 := previous[second].Low
			if low2 < low1*0.94 || low2 > low1*1.08 ||
				low2 > previous[second-1].Low || low2 > previous[second+1].Low ||
				float64(previous[second].Volume) > float64(previous[first].Volume)*1.4 {
				continue
			}
			neckline := 0.0
			for _, week := range previous[first+1 : second] {
				neckline = math.Max(neckline, week.High)
			}
			if neckline < math.Max(low1, low2)*1.12 || neckline > math.Min(low1, low2)*1.5 {
				continue
			}
			valid := true
			for _, week := range previous[second+1:] {
				if week.Low < low2*0.98 || week.Close > neckline {
					valid = false
				}
			}
			volumeRatio := float64(signal.Volume) / math.Max(avgVolume, 1)
			if !valid || previous[len(previous)-1].Close > neckline ||
				signal.Close <= neckline || signal.Close > neckline*1.08 {
				continue
			}
			score := volumeRatio + (neckline/math.Min(low1, low2) - 1) + (signal.Close/neckline - 1)
			if score > best {
				best = score
				v.Entry = true
				v.Exit = signal.Close < low2
				v.Reason = fmt.Sprintf("W lows %s=%.2f / %s=%.2f (gap %dw, %.1f%% apart); neckline=%.2f; breakout_close=%.2f (+%.1f%%), volume=%.2fx",
					previous[first].Date, low1, previous[second].Date, low2, second-first,
					math.Abs(low2/low1-1)*100, neckline, signal.Close, (signal.Close/neckline-1)*100, volumeRatio)
			}
		}
	}
	if !v.Entry {
		v.Reason = "no separated W lows with neckline and first volume-backed breakout"
	}
	return v
}
