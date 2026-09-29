package research

import (
	"context"
	"errors"
	"fmt"
)

var ErrHistoryVersionChanged = errors.New("dataset changed during paginated history query")
var ErrHistoryActionsUnchecked = errors.New("corporate actions unchecked; run update first")

// BarPage is one newest-first page returned in chronological order. NextBefore
// is an exclusive date cursor for fetching the next older page.
type BarPage struct {
	Symbol         string `json:"symbol"`
	Adjust         string `json:"adjust"`
	AdjustAsOf     string `json:"adjust_as_of"`
	DatasetVersion int64  `json:"dataset_version"`
	Bars           []Bar  `json:"bars"`
	HasMore        bool   `json:"has_more"`
	NextBefore     string `json:"next_before,omitempty"`
}

// QueryBarsPage reads completed daily bars from DuckDB. Raw pages are bounded
// in SQL; adjusted pages load the full symbol history so every page shares the
// same adjustment anchor. Pass the first page's version on later pages.
func (s *Store) QueryBarsPage(ctx context.Context, symbol, start, end, before, adjust string, limit int, version *int64) (BarPage, error) {
	page := BarPage{Symbol: symbol, Adjust: adjust, AdjustAsOf: end, Bars: []Bar{}}
	if !symbolRE.MatchString(symbol) {
		return page, fmt.Errorf("invalid symbol")
	}
	if _, err := parseDate(end); err != nil {
		return page, fmt.Errorf("invalid end date")
	}
	if start != "" {
		if _, err := parseDate(start); err != nil || start > end {
			return page, fmt.Errorf("invalid start date")
		}
	}
	if before != "" {
		if _, err := parseDate(before); err != nil {
			return page, fmt.Errorf("invalid before cursor")
		}
	}
	if adjust != "none" && adjust != "qfq" && adjust != "hfq" {
		return page, fmt.Errorf("adjust must be none, qfq or hfq")
	}
	if limit < 1 || limit > 1000 {
		return page, fmt.Errorf("limit must be 1..1000")
	}

	latest := make([]Bar, 0, limit+1)
	if adjust == "none" {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if err := s.db.QueryRowContext(ctx, "SELECT revision FROM research_meta WHERE id=1").Scan(&page.DatasetVersion); err != nil {
			return page, err
		}
		if version != nil && *version != page.DatasetVersion {
			return page, ErrHistoryVersionChanged
		}
		query := "SELECT symbol,CAST(date AS VARCHAR),open,high,low,close,volume,amount,source FROM bars_daily WHERE symbol=? AND date<=?"
		args := []any{symbol, end}
		if start != "" {
			query += " AND date>=?"
			args = append(args, start)
		}
		if before != "" {
			query += " AND date<?"
			args = append(args, before)
		}
		query += " ORDER BY date DESC LIMIT ?"
		args = append(args, limit+1)
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return page, err
		}
		defer rows.Close()
		for rows.Next() {
			var b Bar
			var o, h, l, c int64
			if err := rows.Scan(&b.Symbol, &b.Date, &o, &h, &l, &c, &b.Volume, &b.Amount, &b.Source); err != nil {
				return page, err
			}
			b.Open, b.High, b.Low, b.Close = float64(o)/1000, float64(h)/1000, float64(l)/1000, float64(c)/1000
			latest = append(latest, b)
		}
		if err := rows.Err(); err != nil {
			return page, err
		}
	} else {
		d, err := s.Snapshot(ctx, []string{symbol}, end)
		if err != nil {
			return page, err
		}
		page.DatasetVersion = d.Version
		if version != nil && *version != d.Version {
			return page, ErrHistoryVersionChanged
		}
		if !d.ActionsChecked[symbol] {
			return page, ErrHistoryActionsUnchecked
		}
		bars, err := Adjust(d.Bars[symbol], d.Actions[symbol], adjust, end)
		if err != nil {
			return page, err
		}
		for i := len(bars) - 1; i >= 0 && len(latest) <= limit; i-- {
			b := bars[i]
			if (start == "" || b.Date >= start) && (before == "" || b.Date < before) {
				latest = append(latest, b)
			}
		}
	}
	page.HasMore = len(latest) > limit
	if page.HasMore {
		latest = latest[:limit]
	}
	for i := len(latest) - 1; i >= 0; i-- {
		page.Bars = append(page.Bars, latest[i])
	}
	if page.HasMore {
		page.NextBefore = page.Bars[0].Date
	}
	return page, nil
}
