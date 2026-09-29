package research

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
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

// Authenticate protects both live and historical-data routes.
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

// Handler exposes data import, coverage and historical bars without trading routes.
func (s *Service) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.db.PingContext(r.Context()); err != nil {
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
	m.HandleFunc("GET /v1/data-coverage", func(w http.ResponseWriter, r *http.Request) {
		var bars, profiles, checked, issues int64
		var first, last string
		err := s.Store.db.QueryRowContext(r.Context(), "SELECT count(DISTINCT symbol),COALESCE(CAST(min(date) AS VARCHAR),''),COALESCE(CAST(max(date) AS VARCHAR),'') FROM bars_daily").Scan(&bars, &first, &last)
		if err == nil {
			err = s.Store.db.QueryRowContext(r.Context(), "SELECT count(*) FROM instrument_profiles").Scan(&profiles)
		}
		if err == nil {
			err = s.Store.db.QueryRowContext(r.Context(), "SELECT count(*) FROM instruments WHERE actions_checked").Scan(&checked)
		}
		if err == nil {
			err = s.Store.db.QueryRowContext(r.Context(), "SELECT count(*) FROM data_issues").Scan(&issues)
		}
		if err != nil {
			reply(w, 500, nil, err)
			return
		}
		reply(w, 200, map[string]any{"symbols_with_bars": bars, "profiles": profiles, "actions_checked": checked, "data_issues": issues, "first_date": first, "last_date": last}, nil)
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
	m.HandleFunc("GET /v1/bars", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		symbol, end, start, mode := q.Get("symbol"), q.Get("end"), q.Get("start"), q.Get("adjust")
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
	m.HandleFunc("GET /v1/history/bars", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		end := q.Get("end")
		if end == "" {
			end = ClosedThrough(timeNow())
		}
		adjust := q.Get("adjust")
		if adjust == "" {
			adjust = "none"
		}
		limit := 200
		if q.Has("limit") {
			parsed, err := strconv.Atoi(q.Get("limit"))
			if err != nil {
				reply(w, 400, nil, fmt.Errorf("invalid limit"))
				return
			}
			limit = parsed
		}
		var version *int64
		if q.Has("version") {
			parsed, err := strconv.ParseInt(q.Get("version"), 10, 64)
			if err != nil || parsed < 0 {
				reply(w, 400, nil, fmt.Errorf("invalid version"))
				return
			}
			version = &parsed
		}
		page, err := s.Store.QueryBarsPage(r.Context(), q.Get("symbol"), q.Get("start"), end, q.Get("before"), adjust, limit, version)
		if err != nil {
			status := 400
			if errors.Is(err, ErrHistoryVersionChanged) || errors.Is(err, ErrHistoryActionsUnchecked) {
				status = 409
			}
			reply(w, status, nil, err)
			return
		}
		reply(w, 200, page, nil)
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
