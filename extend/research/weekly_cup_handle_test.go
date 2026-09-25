package research

import "testing"

func cupFixture() []Bar {
	prices := []float64{9.8, 9.5, 9.2, 8.9, 8.2, 7.8, 7.6, 7.8, 8.1, 8.7, 9.1, 9.4, 10.6, 10.2, 10.0, 10.9}
	start, _ := parseDate("2025-01-03")
	out := make([]Bar, 0, len(prices))
	for i, p := range prices {
		out = append(out, Bar{Symbol: "sz000001", Date: day(start.AddDate(0, 0, i*7)), Open: p - 0.1,
			High: p + 0.2, Low: p - 0.2, Close: p, Volume: 100, Amount: 1e9})
	}
	out[0].High = 10
	out[6].Low = 7.5
	out[12].Open, out[12].Volume = 9.8, 250
	out[13].Open, out[13].Volume = 10.4, 170
	out[14].Open, out[14].Volume = 10.2, 120
	out[15].Open, out[15].Volume = 9.9, 220
	return out
}

func TestWeeklyCupHandleRequiresEngulfAndContractingVolume(t *testing.T) {
	s := Strategy{ID: "cup", Kind: "weekly_cup_handle", Fast: 5, Slow: 20, Lookback: 20, MinAmount: 5e7}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	bars := cupFixture()
	if signal := evaluateWeeklyCupHandle(s, bars); !signal.Ready || !signal.Entry {
		t.Fatalf("expected cup signal: %+v", signal)
	}
	bars[14].Volume = 240
	if signal := evaluateWeeklyCupHandle(s, bars); signal.Entry {
		t.Fatal("handle average volume did not contract")
	}
	bars = cupFixture()
	bars = append(bars[:14], bars[15:]...)
	for i := range bars {
		start, _ := parseDate("2025-01-03")
		bars[i].Date = day(start.AddDate(0, 0, i*7))
	}
	if signal := evaluateWeeklyCupHandle(s, bars); !signal.Entry {
		t.Fatalf("one-week handle should qualify: %+v", signal)
	}
	bars = cupFixture()
	extra := bars[14]
	extra.Volume = 110
	bars = append(bars[:15], append([]Bar{extra}, bars[15:]...)...)
	for i := range bars {
		start, _ := parseDate("2025-01-03")
		bars[i].Date = day(start.AddDate(0, 0, i*7))
	}
	if signal := evaluateWeeklyCupHandle(s, bars); !signal.Entry {
		t.Fatalf("three-week handle should qualify: %+v", signal)
	}
	bars = cupFixture()
	bars[15].Close = 10.1
	if signal := evaluateWeeklyCupHandle(s, bars); signal.Entry {
		t.Fatal("no engulfing close")
	}
	bars = cupFixture()
	bars[15].Date = "2025-04-17"
	if signal := evaluateWeeklyCupHandle(s, bars); signal.Ready {
		t.Fatal("incomplete week treated as closed")
	}
}
