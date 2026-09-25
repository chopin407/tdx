package research

import "testing"

func doubleBottomFixture() []Bar {
	prices := []float64{10, 9.5, 9.0, 8.0, 8.6, 9.2, 9.5, 8.8, 8.2, 8.5, 8.9, 9.3, 9.8}
	start, _ := parseDate("2025-01-03")
	out := make([]Bar, 0, len(prices))
	for i, price := range prices {
		out = append(out, Bar{Symbol: "sz000001", Date: day(start.AddDate(0, 0, i*7)),
			Open: price - 0.1, High: price + 0.2, Low: price - 0.2,
			Close: price, Volume: 100, Amount: 1e9})
	}
	out[3].Volume = 120
	out[8].Volume = 80
	out[12].Open, out[12].Low, out[12].High, out[12].Volume = 9.4, 9.3, 9.9, 250
	return out
}

func TestWeeklyDoubleBottomRequiresNecklineAndVolume(t *testing.T) {
	s := Strategy{ID: "w", Kind: "weekly_double_bottom", Fast: 5, Slow: 20, Lookback: 20, MinAmount: 5e7}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	bars := doubleBottomFixture()
	if signal := evaluateWeeklyDoubleBottom(s, bars); !signal.Ready || !signal.Entry {
		t.Fatalf("expected W breakout: %+v", signal)
	}
	bars = doubleBottomFixture()
	bars[8].Low = 7.0
	if signal := evaluateWeeklyDoubleBottom(s, bars); signal.Entry {
		t.Fatal("broken second bottom qualified")
	}
	bars = doubleBottomFixture()
	bars[12].Volume = 110
	if signal := evaluateWeeklyDoubleBottom(s, bars); signal.Entry {
		t.Fatal("breakout without volume qualified")
	}
	bars = doubleBottomFixture()
	bars[12].Date = "2025-03-27"
	if signal := evaluateWeeklyDoubleBottom(s, bars); signal.Ready {
		t.Fatal("incomplete week treated as closed")
	}
}
