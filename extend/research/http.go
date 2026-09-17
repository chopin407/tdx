package research

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func timeNow() time.Time { return time.Now() }

func reply(w http.ResponseWriter, status int, v any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	body := map[string]any{"code": 0, "msg": "ok", "data": v}
	if err != nil {
		body["code"] = 1
		body["msg"] = err.Error()
		body["data"] = nil
	}
	raw, e := json.Marshal(body)
	if e != nil {
		status = 500
		raw = []byte(`{"code":1,"msg":"response encoding failed","data":null}`)
	}
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}

// Authenticate protects both live and research routes when wrapped by main.
func Authenticate(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			reply(w, 401, nil, fmt.Errorf("bearer token required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Handler exposes research routes separately from existing live行情 routes.
func (s *Service) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/data-issues", func(w http.ResponseWriter, r *http.Request) {
		symbol := r.URL.Query().Get("symbol")
		if !symbolRE.MatchString(symbol) {
			reply(w, 400, nil, fmt.Errorf("valid symbol required"))
			return
		}
		rows, err := s.Store.db.QueryContext(r.Context(), "SELECT symbol,date_text,row_no,file_hash,reason,raw_hex FROM data_issues WHERE symbol=? ORDER BY date_text,row_no LIMIT 1000", symbol)
		if err != nil {
			reply(w, 500, nil, err)
			return
		}
		defer rows.Close()
		issues := []DataIssue{}
		for rows.Next() {
			var v DataIssue
			if err = rows.Scan(&v.Symbol, &v.Date, &v.Row, &v.FileHash, &v.Reason, &v.RawHex); err != nil {
				reply(w, 500, nil, err)
				return
			}
			issues = append(issues, v)
		}
		if err = rows.Err(); err != nil {
			reply(w, 500, nil, err)
			return
		}
		reply(w, 200, map[string]any{"issues": issues, "limit": 1000}, nil)
	})
	m.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		err := s.Store.db.PingContext(r.Context())
		if err != nil {
			reply(w, 503, nil, err)
			return
		}
		reply(w, 200, map[string]any{"database": "duckdb", "schema_version": 1}, nil)
	})
	m.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.db.QueryContext(r.Context(), `SELECT b.symbol, min(b.date)::VARCHAR, max(b.date)::VARCHAR, count(*), i.actions_checked, i.kind FROM bars_daily b JOIN instruments i USING(symbol) GROUP BY b.symbol,i.actions_checked,i.kind ORDER BY b.symbol`)
		if err != nil {
			reply(w, 500, nil, err)
			return
		}
		defer rows.Close()
		items := []any{}
		for rows.Next() {
			var symbol, start, end, k string
			var count int64
			var checked bool
			if err = rows.Scan(&symbol, &start, &end, &count, &checked, &k); err != nil {
				reply(w, 500, nil, err)
				return
			}
			items = append(items, map[string]any{"symbol": symbol, "start": start, "end": end, "rows": count, "kind": k, "actions_checked": checked})
		}
		if err = rows.Err(); err != nil {
			reply(w, 500, nil, err)
			return
		}
		reply(w, 200, map[string]any{"instruments": items, "closed_through": ClosedThrough(timeNow()), "price_unit": "CNY", "stock_volume_unit": "shares", "index_volume_unit": "lots", "schedule": s.Config.Schedule}, nil)
	})
	m.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Store.Jobs(r.Context())
		if err != nil {
			reply(w, 500, nil, err)
			return
		}
		reply(w, 200, v, nil)
	})
	m.HandleFunc("POST /v1/jobs/{kind}", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Symbols []string `json:"symbols"`
		}
		if err := decode(w, r, &req); err != nil {
			reply(w, 400, nil, err)
			return
		}
		v, err := s.Start(r.PathValue("kind"), req.Symbols, "")
		if err != nil {
			code := 400
			if errors.Is(err, ErrBusy) {
				code = 409
			}
			reply(w, code, nil, err)
			return
		}
		reply(w, 202, v, nil)
	})
	m.HandleFunc("GET /v1/strategies", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Store.Strategies(r.Context())
		if err != nil {
			reply(w, 500, nil, err)
			return
		}
		reply(w, 200, v, nil)
	})
	m.HandleFunc("PUT /v1/strategies/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v Strategy
		if err := decode(w, r, &v); err != nil {
			reply(w, 400, nil, err)
			return
		}
		if v.ID != "" && v.ID != r.PathValue("id") {
			reply(w, 400, nil, fmt.Errorf("strategy ID mismatch"))
			return
		}
		v.ID = r.PathValue("id")
		if err := s.Store.SaveStrategy(r.Context(), v); err != nil {
			reply(w, 400, nil, err)
			return
		}
		reply(w, 200, v, nil)
	})
	m.HandleFunc("GET /v1/bars", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		symbol := q.Get("symbol")
		end := q.Get("end")
		start := q.Get("start")
		mode := q.Get("adjust")
		if mode == "" {
			mode = "none"
		}
		if !symbolRE.MatchString(symbol) {
			reply(w, 400, nil, fmt.Errorf("valid symbol required"))
			return
		}
		if start != "" {
			if _, err := parseDate(start); err != nil || start > end {
				reply(w, 400, nil, fmt.Errorf("invalid start"))
				return
			}
		}
		d, err := s.Store.Snapshot(r.Context(), []string{symbol}, end)
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		if mode != "none" && !d.ActionsChecked[symbol] {
			reply(w, 409, nil, fmt.Errorf("corporate actions unchecked; run update first"))
			return
		}
		bars, err := Adjust(d.Bars[symbol], d.Actions[symbol], mode, end)
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		out := []Bar{}
		for _, b := range bars {
			if b.Date >= start {
				out = append(out, b)
			}
		}
		reply(w, 200, map[string]any{"dataset_version": d.Version, "adjust": mode, "adjust_as_of": end, "bars": out}, nil)
	})
	m.HandleFunc("POST /v1/screens", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			StrategyID string   `json:"strategy_id"`
			Date       string   `json:"date"`
			Symbols    []string `json:"symbols"`
		}
		if err := decode(w, r, &req); err != nil {
			reply(w, 400, nil, err)
			return
		}
		strategy, err := s.Store.Strategy(r.Context(), req.StrategyID)
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		d, err := s.Store.SnapshotWindow(r.Context(), req.Symbols, req.Date, strategy.Warmup())
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		v, err := Screen(d, strategy, req.Date)
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		id := uuid.NewString()
		if err = s.Store.Artifact(r.Context(), id, "screen", v); err != nil {
			reply(w, 500, nil, err)
			return
		}
		reply(w, 200, map[string]any{"id": id, "result": v}, nil)
	})
	m.HandleFunc("POST /v1/backtests", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			StrategyID string         `json:"strategy_id"`
			Config     BacktestConfig `json:"config"`
		}
		if err := decode(w, r, &req); err != nil {
			reply(w, 400, nil, err)
			return
		}
		strategy, err := s.Store.Strategy(r.Context(), req.StrategyID)
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		d, err := s.Store.Snapshot(r.Context(), []string{req.Config.Symbol}, req.Config.End)
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		v, err := Backtest(d, strategy, req.Config)
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		id := uuid.NewString()
		snapshot := id + "-dataset"
		if err = s.Store.Artifact(r.Context(), snapshot, "dataset", d); err != nil {
			reply(w, 500, nil, err)
			return
		}
		result := map[string]any{"id": id, "dataset_id": snapshot, "result": v}
		if err = s.Store.Artifact(r.Context(), id, "backtest", result); err != nil {
			reply(w, 500, nil, err)
			return
		}
		reply(w, 200, result, nil)
	})
	m.HandleFunc("POST /v1/reviews", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Date string `json:"date"`
		}
		if err := decode(w, r, &req); err != nil {
			reply(w, 400, nil, err)
			return
		}
		d, err := s.Store.SnapshotWindow(r.Context(), nil, req.Date, 501)
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		strategies, err := s.Store.Strategies(r.Context())
		if err != nil {
			reply(w, 500, nil, err)
			return
		}
		v, err := Review(d, req.Date, strategies)
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		id := uuid.NewString()
		if err = s.Store.Artifact(r.Context(), id, "review", v); err != nil {
			reply(w, 500, nil, err)
			return
		}
		reply(w, 200, map[string]any{"id": id, "result": v}, nil)
	})
	m.HandleFunc("GET /v1/artifacts/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Store.GetArtifact(r.Context(), r.PathValue("id"))
		if err != nil {
			code := 500
			if errors.Is(err, sql.ErrNoRows) {
				code = 404
			}
			reply(w, code, nil, err)
			return
		}
		reply(w, 200, v, nil)
	})
	m.HandleFunc("GET /v1/dataset", func(w http.ResponseWriter, r *http.Request) {
		symbol := r.URL.Query().Get("symbol")
		if !symbolRE.MatchString(symbol) {
			reply(w, 400, nil, fmt.Errorf("valid symbol required"))
			return
		}
		v, err := s.Store.Snapshot(r.Context(), []string{symbol}, r.URL.Query().Get("end"))
		if err != nil {
			reply(w, 400, nil, err)
			return
		}
		reply(w, 200, v, nil)
	})
	return m
}
