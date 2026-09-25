package research

import "testing"

func platformFixture() []Bar {
	prices := []float64{9.4, 9.2, 9.3, 9.5, 9.4, 9.2, 9.5, 9.7, 10.5, 10.2, 10.1}
	start, _ := parseDate("2025-01-03")
	out := make([]Bar, 0, len(prices))
	for i, price := range prices {
		out = append(out, Bar{Symbol: "sz000001", Date: day(start.AddDate(0, 0, i*7)),
			Open: price - 0.1, High: price + 0.2, Low: price - 0.2,
			Close: price, Volume: 100, Amount: 1e9})
	}
	out[2].Low, out[5].Low = 9.0, 9.0
	out[7].High = 10.0
	out[8].Open, out[8].High, out[8].Low, out[8].Volume = 9.8, 10.7, 9.7, 250
	out[9].Open, out[9].High, out[9].Low, out[9].Volume = 10.4, 10.6, 10.0, 140
	out[10].Open, out[10].High, out[10].Low, out[10].Volume = 10.15, 10.4, 9.95, 110
	return out
}

func TestWeeklyPlatformHoldRequiresVolumeAndUnbrokenTop(t *testing.T) {
	s := Strategy{ID: "platform", Kind: "weekly_platform_hold", Fast: 5, Slow: 20, Lookback: 20, MinAmount: 5e7}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	bars := platformFixture()
	if signal := evaluateWeeklyPlatformHold(s, bars); !signal.Ready || !signal.Entry {
		t.Fatalf("expected platform hold: %+v", signal)
	}
	bars = platformFixture()
	bars[10].Close = 9.8
	if signal := evaluateWeeklyPlatformHold(s, bars); signal.Entry {
		t.Fatal("close back inside platform qualified")
	}
	bars = platformFixture()
	bars[8].Volume = 110
	if signal := evaluateWeeklyPlatformHold(s, bars); signal.Entry {
		t.Fatal("breakout without volume qualified")
	}
	bars = platformFixture()
	bars[10].Date = "2025-03-13"
	if signal := evaluateWeeklyPlatformHold(s, bars); signal.Ready {
		t.Fatal("incomplete week treated as closed")
	}
}
