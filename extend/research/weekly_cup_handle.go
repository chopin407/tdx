package research

import (
	"fmt"
	"math"
	"time"
)

// completedWeeks aggregates daily bars without using any later trading day.
// A Friday close is required; holiday-shortened weeks are intentionally skipped.
func completedWeeks(bars []Bar) []Bar {
	weeks := []Bar{}
	lastKey := ""
	for _, b := range bars {
		t, err := parseDate(b.Date)
		if err != nil {
			continue
		}
		year, week := t.ISOWeek()
		key := fmt.Sprintf("%d-%02d", year, week)
		if key != lastKey {
			weeks = append(weeks, b)
			lastKey = key
		} else {
			w := &weeks[len(weeks)-1]
			w.Date = b.Date
			w.High = math.Max(w.High, b.High)
			w.Low = math.Min(w.Low, b.Low)
			w.Close = b.Close
			w.Volume += b.Volume
			w.Amount += b.Amount
		}
	}
	if len(weeks) > 0 {
		t, _ := parseDate(weeks[len(weeks)-1].Date)
		if t.Weekday() != time.Friday {
			weeks = weeks[:len(weeks)-1]
		}
	}
	return weeks
}

// evaluateWeeklyCupHandle searches bounded cup and handle lengths, then checks
// a bullish weekly engulfing close. It is a close signal, not a fill price.
func evaluateWeeklyCupHandle(s Strategy, bars []Bar) Signal {
	v := Signal{}
	if len(bars) == 0 {
		return v
	}
	last := bars[len(bars)-1]
	v.Symbol, v.Date = last.Symbol, last.Date
	w := completedWeeks(bars)
	if len(w) < 11 || w[len(w)-1].Date != last.Date {
		v.Reason = "need at least 11 completed Friday weeks ending on signal date"
		return v
	}
	v.Ready = true
	reversal := w[len(w)-1]
	best := -1.0
	for handleWeeks := 1; handleWeeks <= 3; handleWeeks++ {
		breakoutIndex := len(w) - handleWeeks - 2
		if breakoutIndex < 8 {
			continue
		}
		breakout := w[breakoutIndex]
		handle := w[breakoutIndex+1 : len(w)-1]
		handleFloor, handleHigh := math.MaxFloat64, 0.0
		handleVol, handleClose := 0.0, math.MaxFloat64
		validHandle := true
		for _, x := range handle {
			handleFloor = math.Min(handleFloor, x.Low)
			handleHigh = math.Max(handleHigh, x.High)
			handleClose = math.Min(handleClose, x.Close)
			handleVol += float64(x.Volume)
			if x.Low <= 0 || x.High/x.Low > 1.15 || x.Close > breakout.Close*1.04 || x.Volume >= breakout.Volume {
				validHandle = false
			}
		}
		handleVol /= float64(handleWeeks)
		if !validHandle || handleFloor < breakout.Close*0.88 || handleVol > float64(breakout.Volume)*0.8 ||
			reversal.Close <= reversal.Open || reversal.Close <= handleHigh || reversal.Open > handleClose ||
			float64(reversal.Volume) < handleVol*1.15 || reversal.Close > breakout.Close*1.18 ||
			reversal.Amount < s.MinAmount*3 {
			continue
		}
		for cupWeeks := 8; cupWeeks <= 24 && cupWeeks <= breakoutIndex; cupWeeks++ {
			pre := w[breakoutIndex-cupWeeks : breakoutIndex]
			third := cupWeeks / 3
			rim, bottom := 0.0, math.MaxFloat64
			for _, x := range pre[:third] {
				rim = math.Max(rim, x.High)
			}
			for _, x := range pre[third : cupWeeks-third] {
				bottom = math.Min(bottom, x.Low)
			}
			if rim <= 0 || bottom <= 0 {
				continue
			}
			depth := 1 - bottom/rim
			if depth < 0.12 || depth > 0.45 || pre[cupWeeks-1].Close < rim*0.85 {
				continue
			}
			recovered := true
			for _, x := range pre[cupWeeks-third:] {
				if x.Low <= bottom*1.02 {
					recovered = false
				}
			}
			avgVol := 0.0
			for _, x := range pre[cupWeeks-4:] {
				avgVol += float64(x.Volume) / 4
			}
			volumeRatio := float64(breakout.Volume) / math.Max(avgVol, 1)
			if !recovered || breakout.Close <= rim || breakout.Close <= breakout.Open || volumeRatio < 1.3 {
				continue
			}
			score := volumeRatio + (reversal.Close/handleHigh-1)*10 + depth
			if score > best {
				best = score
				v.Entry = true
				v.Exit = reversal.Close < handleFloor
				v.Reason = fmt.Sprintf("cup=%dw depth=%.1f%%; breakout_vol=%.2fx; handle=%dw avg_vol=%.0f (%.0f%% of breakout), pullback=%.1f%%; engulf_close=%.2f > handle_high=%.2f",
					cupWeeks, depth*100, volumeRatio, handleWeeks, handleVol, handleVol/float64(breakout.Volume)*100,
					(1-handleFloor/breakout.Close)*100, reversal.Close, handleHigh)
			}
		}
	}
	if !v.Entry {
		v.Reason = "no 8-24w cup / 1-3w contracting handle / weekly engulfing combination"
	}
	return v
}
