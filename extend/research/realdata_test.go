//go:build cgo

package research

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Optional read-only smoke check of user files. No upstream calls and no source writes.
func TestRealVipdocSample(t *testing.T) {
	root := os.Getenv("TDX_TEST_VIPDOC")
	if root == "" {
		t.Skip("set TDX_TEST_VIPDOC for optional real-file smoke test")
	}
	s := testStore(t)
	ctx := context.Background()
	for _, symbol := range []string{"sh600519", "sz000001", "sh000001", "sh510300"} {
		raw, err := os.ReadFile(filepath.Join(root, symbol[:2], "lday", symbol+".day"))
		if err != nil {
			t.Fatal(err)
		}
		bars, issues, err := ParseDayWithIssues(symbol, raw, 0)
		if err != nil {
			t.Fatalf("%s: %v", symbol, err)
		}
		if len(bars) == 0 {
			t.Fatal("empty real file")
		}
		if len(bars)+len(issues) != len(raw)/32 {
			t.Fatal("unaccounted records")
		}
		if err = s.UpsertWithIssues(ctx, symbol, bars, nil, issues); err != nil {
			t.Fatal(err)
		}
		if err = s.UpsertWithIssues(ctx, symbol, bars, nil, issues); err != nil {
			t.Fatal(err)
		}
		d, err := s.Snapshot(ctx, []string{symbol}, bars[len(bars)-1].Date)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Bars[symbol]) != len(bars) {
			t.Fatal("duplicate or lost real bars")
		}
		var count int
		if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM data_issues WHERE symbol=?", symbol).Scan(&count); err != nil || count != len(issues) {
			t.Fatal("issue deduplication", count, err)
		}
		t.Logf("%s valid=%d quarantined=%d first=%s last=%s last_close=%.3f", symbol, len(bars), len(issues), bars[0].Date, bars[len(bars)-1].Date, bars[len(bars)-1].Close)
	}
}
