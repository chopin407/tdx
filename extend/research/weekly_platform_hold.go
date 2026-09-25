package research

import (
	"fmt"
	"math"
)

// evaluateWeeklyPlatformHold requires a bounded base, a volume-backed breakout,
// and 1-3 completed contracting weeks that continue to hold the platform top.
// The signal is a research watchlist observation, not an immediate buy order.
func evaluateWeeklyPlatformHold(s Strategy, bars []Bar) Signal {
	v := Signal{}
	if len(bars) == 0 {
		return v
	}
	last := bars[len(bars)-1]
	v.Symbol, v.Date = last.Symbol, last.Date
	weeks := completedWeeks(bars)
	if len(weeks) < 8 || weeks[len(weeks)-1].Date != last.Date {
		v.Reason = "need at least 8 completed Friday weeks ending on signal date"
		return v
	}
	v.Ready = true
	best := -1.0
	for holdWeeks := 1; holdWeeks <= 3; holdWeeks++ {
		breakoutIndex := len(weeks) - holdWeeks - 1
		if breakoutIndex < 6 {
			continue
		}
		breakout := weeks[breakoutIndex]
		hold := weeks[breakoutIndex+1:]
		for baseWeeks := 6; baseWeeks <= 20 && baseWeeks <= breakoutIndex; baseWeeks++ {
			base := weeks[breakoutIndex-baseWeeks : breakoutIndex]
			floor, ceiling := math.MaxFloat64, 0.0
			for _, x := range base {
				floor = math.Min(floor, x.Low)
				ceiling = math.Max(ceiling, x.High)
			}
			if floor <= 0 || ceiling/floor > 1.25 {
				continue
			}
			firstTouch := -1
			secondTouch := false
			for i, x := range base {
				if x.Low <= floor*1.07 {
					if firstTouch < 0 {
						firstTouch = i
					} else if i-firstTouch >= 2 {
						secondTouch = true
					}
				}
			}
			if !secondTouch {
				continue // A single sharp V is not a platform bottom.
			}
			avgBaseVolume := 0.0
			for _, x := range base[baseWeeks-4:] {
				avgBaseVolume += float64(x.Volume) / 4
			}
			breakoutRatio := float64(breakout.Volume) / math.Max(avgBaseVolume, 1)
			if breakout.High <= breakout.Low || breakout.Close <= breakout.Open ||
				(breakout.Close-breakout.Low)/(breakout.High-breakout.Low) < 0.7 ||
				breakout.Close <= ceiling*1.01 || breakout.Close > ceiling*1.12 || breakoutRatio < 1.5 {
				continue
			}
			holdLow, holdVolume := math.MaxFloat64, 0.0
			valid := true
			previousClose := breakout.Close
			for _, x := range hold {
				holdLow = math.Min(holdLow, x.Low)
				holdVolume += float64(x.Volume)
				if x.Low < ceiling*0.98 || x.Close < ceiling || x.Close < previousClose*0.97 ||
					x.Close > breakout.Close*1.04 || x.Low <= 0 || x.High/x.Low > 1.15 ||
					x.Volume >= breakout.Volume {
					valid = false
				}
				previousClose = x.Close
			}
			holdVolume /= float64(holdWeeks)
			if !valid || holdVolume > float64(breakout.Volume)*0.75 ||
				hold[len(hold)-1].Amount < s.MinAmount*3 {
				continue
			}
			score := breakoutRatio + (holdLow/ceiling-1)*5 - ceiling/floor + 1
			if score > best {
				best = score
				v.Entry = true
				v.Exit = hold[len(hold)-1].Close < ceiling
				v.Reason = fmt.Sprintf("platform=%dw range=%.1f%% top=%.2f; breakout=%s volume=%.2fx; hold=%dw avg_volume=%.0f (%.0f%% of breakout), low=%.2f, close=%.2f",
					baseWeeks, (ceiling/floor-1)*100, ceiling, breakout.Date, breakoutRatio,
					holdWeeks, holdVolume, holdVolume/float64(breakout.Volume)*100, holdLow, hold[len(hold)-1].Close)
			}
		}
	}
	if !v.Entry {
		v.Reason = "no 6-20w base / volume breakout / 1-3w contracting hold combination"
	}
	return v
}
