package research

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/tdx/protocol"
)

// Store owns one DuckDB file. Writers serialize; snapshots hold a read lock.
// The executable must register the official duckdb database/sql driver.
type Store struct {
	db *sql.DB
	mu sync.RWMutex
}

const schema = `
CREATE TABLE IF NOT EXISTS research_meta (id INTEGER PRIMARY KEY, schema_version INTEGER NOT NULL, revision BIGINT NOT NULL);
INSERT INTO research_meta VALUES (1,1,0) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS instruments (symbol VARCHAR PRIMARY KEY, kind VARCHAR NOT NULL, actions_checked BOOLEAN NOT NULL DEFAULT false, updated_at VARCHAR NOT NULL);
CREATE TABLE IF NOT EXISTS instrument_profiles (symbol VARCHAR PRIMARY KEY, name VARCHAR NOT NULL DEFAULT '', industry VARCHAR NOT NULL DEFAULT '', ipo_date VARCHAR NOT NULL DEFAULT '', is_st BOOLEAN NOT NULL DEFAULT false, float_shares DOUBLE NOT NULL DEFAULT 0, total_shares DOUBLE NOT NULL DEFAULT 0, finance_date VARCHAR NOT NULL DEFAULT '', source VARCHAR NOT NULL DEFAULT '', point_in_time BOOLEAN NOT NULL DEFAULT false, updated_at VARCHAR NOT NULL);
CREATE TABLE IF NOT EXISTS bars_daily (symbol VARCHAR NOT NULL, date DATE NOT NULL, open BIGINT NOT NULL, high BIGINT NOT NULL, low BIGINT NOT NULL, close BIGINT NOT NULL, volume BIGINT NOT NULL, amount DOUBLE NOT NULL, source VARCHAR NOT NULL, PRIMARY KEY(symbol,date));
CREATE TABLE IF NOT EXISTS corporate_actions (symbol VARCHAR NOT NULL, date DATE NOT NULL, category INTEGER NOT NULL, c1 DOUBLE, c2 DOUBLE, c3 DOUBLE, c4 DOUBLE, PRIMARY KEY(symbol,date,category));
CREATE TABLE IF NOT EXISTS strategies (id VARCHAR PRIMARY KEY, body VARCHAR NOT NULL);
CREATE TABLE IF NOT EXISTS artifacts (id VARCHAR PRIMARY KEY, kind VARCHAR NOT NULL, created_at VARCHAR NOT NULL, body VARCHAR NOT NULL);
CREATE TABLE IF NOT EXISTS jobs (id VARCHAR PRIMARY KEY, body VARCHAR NOT NULL);
CREATE TABLE IF NOT EXISTS data_issues (symbol VARCHAR, date_text VARCHAR, row_no INTEGER, file_hash VARCHAR, reason VARCHAR, raw_hex VARCHAR, PRIMARY KEY(symbol,file_hash,row_no));
`

// Open opens or creates a research database. Only this service may write it.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err = db.QueryRow("SELECT schema_version FROM research_meta WHERE id=1").Scan(&version); err != nil || version != 1 {
		db.Close()
		return nil, fmt.Errorf("unsupported research schema %d: %v", version, err)
	}
	s := &Store{db: db}
	jobs, err := s.Jobs(context.Background())
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, j := range jobs {
		if j.State == "running" || j.State == "queued" {
			j.State = "interrupted"
			j.Error = "service stopped before job completion; rerun is safe"
			j.Finished = time.Now().UTC().Format(time.RFC3339)
			if err = s.SaveJob(context.Background(), j); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }

// Upsert commits one symbol atomically, including its complete action response.
// nil actions means unchecked, a non-nil empty slice means successfully checked.
func (s *Store) Upsert(ctx context.Context, symbol string, bars []Bar, actions []*protocol.Gbbq) error {
	return s.UpsertWithIssues(ctx, symbol, bars, actions, nil)
}

// UpsertWithIssues commits valid data and its quarantined source records together.
func (s *Store) UpsertWithIssues(ctx context.Context, symbol string, bars []Bar, actions []*protocol.Gbbq, issues []DataIssue) error {
	if !symbolRE.MatchString(symbol) {
		return fmt.Errorf("invalid symbol")
	}
	for _, b := range bars {
		if b.Symbol != symbol {
			return fmt.Errorf("mixed symbols")
		}
		if err := b.validate(); err != nil {
			return err
		}
	}
	for _, a := range actions {
		if a == nil || a.Code != symbol || a.Time.IsZero() {
			return fmt.Errorf("invalid corporate action")
		}
		for _, v := range []float64{a.C1, a.C2, a.C3, a.C4} {
			if !finite(v) {
				return fmt.Errorf("invalid corporate action value")
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO instruments VALUES (?,?,?,?) ON CONFLICT(symbol) DO UPDATE SET actions_checked=excluded.actions_checked, updated_at=excluded.updated_at`, symbol, kind(symbol), actions != nil, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	// DuckDB handles wide prepared inserts efficiently. A 1000-row batch keeps
	// parameter counts bounded while avoiding dozens of round trips per .day file.
	for start := 0; start < len(bars); start += 1000 {
		end := start + 1000
		if end > len(bars) {
			end = len(bars)
		}
		values := make([]string, 0, end-start)
		args := []any{}
		for _, b := range bars[start:end] {
			values = append(values, "(?,?,?,?,?,?,?,?,?)")
			args = append(args, b.Symbol, b.Date, milli(b.Open), milli(b.High), milli(b.Low), milli(b.Close), b.Volume, b.Amount, b.Source)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO bars_daily VALUES `+strings.Join(values, ",")+` ON CONFLICT(symbol,date) DO UPDATE SET open=excluded.open,high=excluded.high,low=excluded.low,close=excluded.close,volume=excluded.volume,amount=excluded.amount,source=excluded.source`, args...)
		if err != nil {
			return err
		}
	}
	if actions != nil {
		if _, err = tx.ExecContext(ctx, "DELETE FROM corporate_actions WHERE symbol=?", symbol); err != nil {
			return err
		}
		for _, a := range actions {
			if _, err = tx.ExecContext(ctx, "INSERT INTO corporate_actions VALUES (?,?,?,?,?,?,?) ON CONFLICT DO UPDATE SET c1=excluded.c1,c2=excluded.c2,c3=excluded.c3,c4=excluded.c4", symbol, day(a.Time), a.Category, a.C1, a.C2, a.C3, a.C4); err != nil {
				return err
			}
		}
	}
	for _, issue := range issues {
		if issue.Symbol != symbol {
			return fmt.Errorf("mixed issue symbols")
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO data_issues VALUES (?,?,?,?,?,?) ON CONFLICT DO NOTHING", issue.Symbol, issue.Date, issue.Row, issue.FileHash, issue.Reason, issue.RawHex); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE research_meta SET revision=revision+1 WHERE id=1"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Symbols(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT symbol FROM instruments ORDER BY symbol")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Latest(ctx context.Context, symbol string) (string, error) {
	var d string
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(CAST(max(date) AS VARCHAR),'') FROM bars_daily WHERE symbol=?", symbol).Scan(&d)
	return d, err
}

func (s *Store) UpsertProfile(ctx context.Context, p InstrumentProfile) error {
	if !symbolRE.MatchString(p.Symbol) {
		return fmt.Errorf("invalid profile symbol")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO instrument_profiles VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol) DO UPDATE SET name=excluded.name,industry=excluded.industry,ipo_date=excluded.ipo_date,is_st=excluded.is_st,float_shares=excluded.float_shares,total_shares=excluded.total_shares,finance_date=excluded.finance_date,source=excluded.source,point_in_time=excluded.point_in_time,updated_at=excluded.updated_at`,
		p.Symbol, p.Name, p.Industry, p.IPODate, p.IsST, p.FloatShares, p.TotalShares, p.FinanceDate, p.Source, p.PointInTime, time.Now().UTC().Format(time.RFC3339))
	return err
}

// Snapshot loads one consistent revision. Empty symbols means the local universe.
func (s *Store) Snapshot(ctx context.Context, symbols []string, end string) (*Dataset, error) {
	return s.SnapshotWindow(ctx, symbols, end, 0)
}

// SnapshotWindow bounds cross-sectional scans to the latest n bars per symbol.
// Zero requests full history, used for individual backtests and exports.
func (s *Store) SnapshotWindow(ctx context.Context, symbols []string, end string, n int) (*Dataset, error) {
	if _, err := parseDate(end); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := &Dataset{Bars: map[string][]Bar{}, Actions: map[string][]*protocol.Gbbq{}, ActionsChecked: map[string]bool{}, Profiles: map[string]InstrumentProfile{}}
	if err := s.db.QueryRowContext(ctx, "SELECT revision FROM research_meta WHERE id=1").Scan(&d.Version); err != nil {
		return nil, err
	}
	where := ""
	args := []any{end}
	if len(symbols) > 0 {
		marks := []string{}
		for _, v := range symbols {
			if !symbolRE.MatchString(v) {
				return nil, fmt.Errorf("invalid symbol %q", v)
			}
			marks = append(marks, "?")
			args = append(args, v)
		}
		where = " AND symbol IN (" + strings.Join(marks, ",") + ")"
	}
	qualify := ""
	if n > 0 {
		qualify = fmt.Sprintf(" QUALIFY row_number() OVER (PARTITION BY symbol ORDER BY date DESC) <= %d", n)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT symbol,CAST(date AS VARCHAR),open,high,low,close,volume,amount,source FROM bars_daily WHERE date<=?"+where+qualify+" ORDER BY symbol,date", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b Bar
		var o, h, l, c int64
		if err = rows.Scan(&b.Symbol, &b.Date, &o, &h, &l, &c, &b.Volume, &b.Amount, &b.Source); err != nil {
			rows.Close()
			return nil, err
		}
		b.Open = float64(o) / 1000
		b.High = float64(h) / 1000
		b.Low = float64(l) / 1000
		b.Close = float64(c) / 1000
		d.Bars[b.Symbol] = append(d.Bars[b.Symbol], b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, "SELECT symbol,CAST(date AS VARCHAR),category,c1,c2,c3,c4 FROM corporate_actions WHERE date<=?"+where+" ORDER BY symbol,date,category", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		a := new(protocol.Gbbq)
		var date string
		if err = rows.Scan(&a.Code, &date, &a.Category, &a.C1, &a.C2, &a.C3, &a.C4); err != nil {
			rows.Close()
			return nil, err
		}
		a.Time, _ = parseDate(date)
		a.Time = a.Time.Add(15 * time.Hour)
		d.Actions[a.Code] = append(d.Actions[a.Code], a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, "SELECT symbol,actions_checked FROM instruments")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var symbol string
		var checked bool
		if err = rows.Scan(&symbol, &checked); err != nil {
			return nil, err
		}
		if _, ok := d.Bars[symbol]; ok {
			d.ActionsChecked[symbol] = checked
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	profileRows, err := s.db.QueryContext(ctx, "SELECT symbol,name,industry,ipo_date,is_st,float_shares,total_shares,finance_date,source,point_in_time FROM instrument_profiles")
	if err != nil {
		return nil, err
	}
	defer profileRows.Close()
	for profileRows.Next() {
		var p InstrumentProfile
		if err = profileRows.Scan(&p.Symbol, &p.Name, &p.Industry, &p.IPODate, &p.IsST, &p.FloatShares, &p.TotalShares, &p.FinanceDate, &p.Source, &p.PointInTime); err != nil {
			return nil, err
		}
		if _, ok := d.Bars[p.Symbol]; ok {
			d.Profiles[p.Symbol] = p
		}
	}
	return d, profileRows.Err()
}

func (s *Store) SaveStrategy(ctx context.Context, v Strategy) error {
	if err := v.Validate(); err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, "INSERT INTO strategies VALUES (?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", v.ID, string(b))
	return err
}
func (s *Store) Strategy(ctx context.Context, id string) (Strategy, error) {
	var raw string
	v := Strategy{}
	err := s.db.QueryRowContext(ctx, "SELECT body FROM strategies WHERE id=?", id).Scan(&raw)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}
func (s *Store) Strategies(ctx context.Context) ([]Strategy, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM strategies ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Strategy{}
	for rows.Next() {
		var raw string
		var v Strategy
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Artifact(ctx context.Context, id, kind string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx, "INSERT INTO artifacts VALUES (?,?,?,?)", id, kind, time.Now().UTC().Format(time.RFC3339), string(b))
	return err
}
func (s *Store) GetArtifact(ctx context.Context, id string) (json.RawMessage, error) {
	var b string
	err := s.db.QueryRowContext(ctx, "SELECT body FROM artifacts WHERE id=?", id).Scan(&b)
	return json.RawMessage(b), err
}
func (s *Store) SaveJob(ctx context.Context, j Job) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx, "INSERT INTO jobs VALUES (?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", j.ID, string(b))
	return err
}
func (s *Store) Jobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM jobs ORDER BY json_extract_string(body, '$.started') DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var raw string
		var j Job
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
