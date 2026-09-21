package research

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/ios"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
)

// ParseDay parses the desktop .day layout without losing odd-lot stock volume.
// scale=0 uses the tdx2db instrument convention (stocks/indexes 100, funds/B
// shares 1000). An explicit 100/1000 override supports legacy source files.
// Never infer scale from price magnitude.
func ParseDay(symbol string, raw []byte, scale int) ([]Bar, error) {
	bars, issues, err := ParseDayWithIssues(symbol, raw, scale)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return nil, fmt.Errorf("record %d: %s", issues[0].Row, issues[0].Reason)
	}
	return bars, nil
}

// DataIssue preserves corrupt source records without fabricating a repaired bar.
type DataIssue struct {
	Symbol   string `json:"symbol"`
	Date     string `json:"date"`
	Row      int    `json:"row"`
	FileHash string `json:"file_sha256"`
	Reason   string `json:"reason"`
	RawHex   string `json:"raw_hex"`
}

// ParseDayWithIssues quarantines invalid records; structural corruption is fatal.
func ParseDayWithIssues(symbol string, raw []byte, scale int) ([]Bar, []DataIssue, error) {
	if scale == 0 {
		scale = 100
		if kind(symbol) == "etf" || strings.HasPrefix(symbol, "sh900") || strings.HasPrefix(symbol, "sz20") {
			scale = 1000
		}
	}
	if scale != 100 && scale != 1000 {
		return nil, nil, fmt.Errorf("price_scale must be 0 (instrument convention), 100 or 1000")
	}
	if !symbolRE.MatchString(symbol) || len(raw)%32 != 0 {
		return nil, nil, fmt.Errorf("invalid day file: %s (%d bytes)", symbol, len(raw))
	}
	result := make([]Bar, 0, len(raw)/32)
	prev := ""
	issues := []DataIssue{}
	hash := sha256.Sum256(raw)
	fileHash := hex.EncodeToString(hash[:])
	for offset := 0; offset < len(raw); offset += 32 {
		p := raw[offset : offset+32]
		u := func(i int) uint32 { return binary.LittleEndian.Uint32(p[i : i+4]) }
		date := fmt.Sprintf("%08d", u(0))
		date = date[:4] + "-" + date[4:6] + "-" + date[6:]
		volume := int64(u(24))
		reserved := u(28)
		// tdx2db's native merge format marks lots plus odd shares with 0xc36400xx.
		if kind(symbol) != "index" && reserved&0xffffff00 == 0xc3640000 {
			volume = volume*100 + int64(reserved&0xff)
		}
		b := Bar{Symbol: symbol, Date: date, Open: float64(u(4)) / float64(scale), High: float64(u(8)) / float64(scale), Low: float64(u(12)) / float64(scale), Close: float64(u(16)) / float64(scale), Volume: volume, Amount: float64(math.Float32frombits(u(20))), Source: "vipdoc"}
		if err := b.validate(); err != nil {
			issues = append(issues, DataIssue{symbol, date, offset / 32, fileHash, err.Error(), hex.EncodeToString(p)})
			continue
		}
		if date <= prev {
			issues = append(issues, DataIssue{symbol, date, offset / 32, fileHash, "non-increasing date", hex.EncodeToString(p)})
			continue
		}
		prev = date
		result = append(result, b)
	}
	return result, issues, nil
}

// ClosedThrough excludes today's still mutable daily bar until 16:30 Shanghai.
func ClosedThrough(now time.Time) string {
	t := now.In(Shanghai)
	if t.Hour()*60+t.Minute() < 16*60+30 {
		t = t.AddDate(0, 0, -1)
	}
	return day(t)
}

// Source separates network IO from deterministic import/update tests.
type Source interface {
	Symbols(context.Context) ([]string, error)
	Daily(context.Context, string, string) ([]Bar, error)
	Actions(context.Context, string) ([]*protocol.Gbbq, error)
}

type ProfileSource interface {
	Profile(context.Context, string) (InstrumentProfile, error)
}

// TDXSource uses an exclusively owned tdx connection; no HTTP loopback is used.
type TDXSource struct {
	Client     *tdx.Client
	mu         sync.Mutex
	names      map[string]string
	industries map[string]string
}

// DialSource bounds host discovery and closes the owned client on cancellation.
func DialSource(ctx context.Context) (Source, func(), error) {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c, err := tdx.DialWith(func(_ context.Context) (ios.ReadWriteCloser, string, error) {
		var last error
		for _, host := range tdx.Hosts {
			if !strings.Contains(host, ":") {
				host += ":7709"
			}
			dialer := net.Dialer{Timeout: 3 * time.Second}
			conn, e := dialer.DialContext(dialCtx, "tcp", host)
			if e == nil {
				return conn, host, nil
			}
			last = e
			if dialCtx.Err() != nil {
				return nil, "", dialCtx.Err()
			}
		}
		return nil, "", fmt.Errorf("no TDX host reachable: %w", last)
	})
	if err != nil {
		return nil, nil, err
	}
	c.SetTimeout(10 * time.Second)
	stop := context.AfterFunc(ctx, func() { c.Close() })
	return &TDXSource{Client: c, names: map[string]string{}, industries: map[string]string{}}, func() { stop(); c.Close() }, nil
}

func (s *TDXSource) Symbols(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	symbols, err := s.Client.GetStockCodeAll()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range []protocol.Exchange{protocol.ExchangeSZ, protocol.ExchangeSH, protocol.ExchangeBJ} {
		resp, e := s.Client.GetCodeAll(ex)
		if e != nil {
			continue
		}
		prefix := "sz"
		if ex == protocol.ExchangeSH {
			prefix = "sh"
		} else if ex == protocol.ExchangeBJ {
			prefix = "bj"
		}
		for _, item := range resp.List {
			s.names[prefix+item.Code] = strings.TrimSpace(item.Name)
		}
	}
	if rows, e := s.Client.GetTdxHy(); e == nil {
		for _, item := range rows {
			prefix := "sz"
			if item.Market == 1 {
				prefix = "sh"
			} else if item.Market == 2 {
				prefix = "bj"
			}
			s.industries[prefix+item.Code] = item.SwHy
			if s.industries[prefix+item.Code] == "" {
				s.industries[prefix+item.Code] = item.TdxHy
			}
		}
	}
	return symbols, nil
}
func (s *TDXSource) Actions(ctx context.Context, symbol string) ([]*protocol.Gbbq, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := s.Client.GetGbbq(symbol)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("empty action response")
	}
	out := append([]*protocol.Gbbq{}, r.List...)
	return out, nil
}
func (s *TDXSource) Daily(ctx context.Context, symbol, since string) ([]Bar, error) {
	result := []Bar{}
	seen := map[string]bool{}
	// An int cursor avoids the existing uint16 pagination wraparound.
	for offset := 0; offset <= 64800; offset += 800 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var r *protocol.KlineResp
		var err error
		if kind(symbol) == "index" {
			r, err = s.Client.GetIndex(protocol.TypeKlineDay, symbol, uint16(offset), 800)
		} else {
			r, err = s.Client.GetKlineDay(symbol, uint16(offset), 800)
		}
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, fmt.Errorf("empty daily response")
		}
		stop := false
		added := 0
		for _, k := range r.List {
			date := day(k.Time)
			if since != "" && date < since {
				stop = true
				continue
			}
			if seen[date] {
				continue
			}
			seen[date] = true
			added++
			volume := k.Volume
			if kind(symbol) != "index" {
				volume *= 100
			}
			result = append(result, Bar{Symbol: symbol, Date: date, Open: k.Open.Float64(), High: k.High.Float64(), Low: k.Low.Float64(), Close: k.Close.Float64(), Volume: volume, Amount: k.Amount.Float64(), Source: "tdx"})
		}
		if stop || len(r.List) < 800 {
			sort.Slice(result, func(i, j int) bool { return result[i].Date < result[j].Date })
			return result, nil
		}
		if added == 0 {
			return nil, fmt.Errorf("upstream repeated page for %s", symbol)
		}
	}
	return nil, fmt.Errorf("daily history exceeds protocol pagination limit")
}

func (s *TDXSource) Profile(ctx context.Context, symbol string) (InstrumentProfile, error) {
	if err := ctx.Err(); err != nil {
		return InstrumentProfile{}, err
	}
	s.mu.Lock()
	emptyReference := len(s.names) == 0 && len(s.industries) == 0
	s.mu.Unlock()
	if emptyReference {
		_, _ = s.Symbols(ctx)
	}
	ex := protocol.ExchangeSZ
	if strings.HasPrefix(symbol, "sh") {
		ex = protocol.ExchangeSH
	} else if strings.HasPrefix(symbol, "bj") {
		ex = protocol.ExchangeBJ
	}
	fi, err := s.Client.GetFinanceInfo(ex, symbol[2:])
	if err != nil {
		return InstrumentProfile{}, err
	}
	s.mu.Lock()
	name := s.names[symbol]
	industry := s.industries[symbol]
	s.mu.Unlock()
	upper := strings.ToUpper(strings.TrimSpace(name))
	return InstrumentProfile{
		Symbol: symbol, Name: name, Industry: industry,
		IPODate:     fmt.Sprintf("%08d", fi.IPODate),
		IsST:        strings.Contains(upper, "ST") || strings.Contains(name, "退"),
		FloatShares: fi.LiuTongGuBen, TotalShares: fi.ZongGuBen,
		FinanceDate: fmt.Sprintf("%08d", fi.UpdatedDate), Source: "tdx-finance-current", PointInTime: false,
	}, nil
}

// ImportDirectory imports each file atomically; failures are retained in the job.
func (s *Store) ImportDirectory(ctx context.Context, dir string, scale int, cutoff string, progress func(string, int, error)) error {
	files := []string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".day") {
			symbol := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
			if symbolRE.MatchString(symbol) {
				files = append(files, path)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no valid .day files in import directory")
	}
	sort.Strings(files)
	failures := 0
	for _, path := range files {
		if err = ctx.Err(); err != nil {
			return err
		}
		symbol := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		raw, e := os.ReadFile(path)
		var bars []Bar
		var issues []DataIssue
		if e == nil {
			bars, issues, e = ParseDayWithIssues(symbol, raw, scale)
		}
		if e == nil {
			kept := bars[:0]
			for _, b := range bars {
				if b.Date <= cutoff {
					kept = append(kept, b)
				}
			}
			bars = kept
			if len(bars) > 0 || len(issues) > 0 {
				e = s.UpsertWithIssues(ctx, symbol, bars, nil, issues)
			}
		}
		count := len(bars)
		if e != nil {
			count = 0
		}
		if e == nil && len(issues) > 0 {
			e = fmt.Errorf("%d invalid records quarantined; %d valid rows committed; inspect /v1/data-issues", len(issues), count)
		}
		if e != nil {
			failures++
		}
		progress(symbol, count, e)
	}
	if failures > 0 {
		return fmt.Errorf("%d files failed; successful files committed; rerun after repair", failures)
	}
	return nil
}

// Update performs a per-symbol overlap refresh. No global max-date watermark.
func (s *Store) Update(ctx context.Context, source Source, symbols []string, cutoff string, progress func(string, int, error)) error {
	if len(symbols) == 0 {
		online, err := source.Symbols(ctx)
		if err != nil {
			return err
		}
		set := map[string]bool{}
		for _, v := range online {
			if symbolRE.MatchString(v) {
				set[v] = true
			}
		}
		for v := range set {
			symbols = append(symbols, v)
		}
		sort.Strings(symbols)
	}
	if len(symbols) == 0 {
		return fmt.Errorf("no symbols to update")
	}
	failures := 0
	consecutiveFailures := 0
	for _, symbol := range symbols {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := 0
		err := func() error {
			latest, err := s.Latest(ctx, symbol)
			if err != nil {
				return err
			}
			since := ""
			if latest != "" {
				t, _ := parseDate(latest)
				since = day(t.AddDate(0, 0, -14))
			}
			bars, err := source.Daily(ctx, symbol, since)
			if err != nil {
				return err
			}
			kept := []Bar{}
			for _, b := range bars {
				if b.Date <= cutoff {
					kept = append(kept, b)
				}
			}
			if len(kept) == 0 {
				return fmt.Errorf("no completed bars returned; existing history preserved")
			}
			var actions []*protocol.Gbbq
			if kind(symbol) == "stock" || kind(symbol) == "etf" {
				actions, err = source.Actions(ctx, symbol)
				if err != nil {
					return err
				}
				if actions == nil {
					actions = []*protocol.Gbbq{}
				}
			} else {
				actions = []*protocol.Gbbq{}
			}
			if err = s.Upsert(ctx, symbol, kept, actions); err != nil {
				return err
			}
			count = len(kept)
			if ps, ok := source.(ProfileSource); ok && kind(symbol) == "stock" {
				profile, profileErr := ps.Profile(ctx, symbol)
				if profileErr != nil {
					return fmt.Errorf("bars committed but instrument profile unavailable: %w", profileErr)
				}
				if err = s.UpsertProfile(ctx, profile); err != nil {
					return fmt.Errorf("bars committed but instrument profile persist failed: %w", err)
				}
			}
			return nil
		}()
		if err != nil {
			failures++
			consecutiveFailures++
		} else {
			consecutiveFailures = 0
		}
		progress(symbol, count, err)
		if consecutiveFailures >= 5 {
			return fmt.Errorf("stopped after five consecutive failed symbols; %d failures, unprocessed symbols will be retried by the next job", failures)
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d symbols failed; inspect job errors", failures)
	}
	return nil
}
