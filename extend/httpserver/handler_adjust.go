package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
)

type dailySource interface {
	GetKlineDay(string, uint16, uint16) (*protocol.KlineResp, error)
	GetGbbq(string) (*protocol.GbbqResp, error)
}

// loadAdjustedDaily uses a bounded cursor and calculates factors before slicing.
func loadAdjustedDaily(ctx context.Context, c dailySource, code string) (protocol.Klines, []*protocol.Factor, error) {
	ks := make(protocol.Klines, 0)
	for start := 0; ; start += 800 {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if start > 64800 {
			return nil, nil, fmt.Errorf("日线历史超过协议分页范围")
		}
		page, err := c.GetKlineDay(code, uint16(start), 800)
		if err != nil {
			return nil, nil, err
		}
		if page == nil {
			return nil, nil, fmt.Errorf("日线响应为空")
		}
		for i, k := range page.List {
			if k == nil || (i > 0 && !k.Time.After(page.List[i-1].Time)) {
				return nil, nil, fmt.Errorf("日线顺序异常")
			}
		}
		if len(ks) > 0 && len(page.List) > 0 && !page.List[len(page.List)-1].Time.Before(ks[0].Time) {
			return nil, nil, fmt.Errorf("日线分页重复或重叠")
		}
		if len(ks) > 0 && len(page.List) > 0 {
			ks[0].Last = page.List[len(page.List)-1].Close
		}
		if len(page.List) > 800 || len(ks)+len(page.List) > 65535 {
			return nil, nil, fmt.Errorf("日线数量超过协议范围")
		}
		ks = append(protocol.Klines(page.List), ks...)
		if len(page.List) < 800 {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	actions, err := c.GetGbbq(code)
	if err != nil {
		return nil, nil, err
	}
	if actions == nil {
		return nil, nil, fmt.Errorf("除权除息响应为空")
	}
	events := make(protocol.XRXDs, 0)
	for _, g := range actions.List {
		if g == nil {
			return nil, nil, fmt.Errorf("除权除息记录为空")
		}
		if len(ks) > 0 && !g.Time.After(ks[len(ks)-1].Time) && (g.Category == 11 || g.Category == 12) {
			return nil, nil, fmt.Errorf("暂不支持扩缩股复权")
		}
		if g.IsXRXD() {
			events = append(events, g.XRXD())
		}
	}
	return ks, events.Pre(ks).Factors(), nil
}

// adjustedDaily exposes the existing affine daily adjustment without a second cache.
func (s *Server) adjustedDaily(mode string, all bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code, err := queryStr(r, "code")
		if err == nil {
			_, _, err = protocol.DecodeCode(code)
		}
		if err != nil {
			respondErr(w, 400, err.Error())
			return
		}
		start, count := uint16(0), uint16(0)
		if !all {
			start, err = queryUint16(r, "start")
			if err == nil {
				count, err = queryUint16(r, "count")
			}
			if err == nil && (count == 0 || count > 800) {
				err = fmt.Errorf("count 必须在 1..800")
			}
			if err != nil {
				respondErr(w, 400, err.Error())
				return
			}
		}
		var ks protocol.Klines
		var fs []*protocol.Factor
		err = s.pool.Do(func(c *tdx.Client) error { var e error; ks, fs, e = loadAdjustedDaily(r.Context(), c, code); return e })
		if err != nil {
			respondErr(w, 502, err.Error())
			return
		}
		if mode == "factors" {
			respondOK(w, fs)
			return
		}
		if mode == "qfq" {
			ks = protocol.ApplyQFQ(ks, fs)
		} else {
			ks = protocol.ApplyHFQ(ks, fs)
		}
		if !all {
			ks = dailyPage(ks, int(start), int(count))
		}
		respondOK(w, &protocol.KlineResp{Count: uint16(len(ks)), List: ks})
	}
}

func dailyPage(ks protocol.Klines, start, count int) protocol.Klines {
	end := len(ks) - start
	if end <= 0 {
		return protocol.Klines{}
	}
	begin := end - count
	if begin < 0 {
		begin = 0
	}
	return ks[begin:end]
}

// dailyAdjustment returns true if it handled an explicit adjustment request.
func (s *Server) dailyAdjustment(w http.ResponseWriter, r *http.Request, all bool) bool {
	mode := r.URL.Query().Get("adjust")
	if mode == "" || mode == "none" {
		return false
	}
	if mode != "qfq" && mode != "hfq" {
		respondErr(w, 400, "adjust 可选 none、qfq、hfq")
		return true
	}
	s.adjustedDaily(mode, all)(w, r)
	return true
}

func (s *Server) registerAdditionalRoutes(mux *http.ServeMux) {
	for _, mode := range []string{"qfq", "hfq"} {
		mux.HandleFunc("GET /kline/day/"+mode, s.adjustedDaily(mode, false))
		mux.HandleFunc("GET /kline/day/"+mode+"/all", s.adjustedDaily(mode, true))
	}
	mux.HandleFunc("GET /kline/day/factors", s.adjustedDaily("factors", true))
	// Route aliases delegate to existing validated generic index handlers.
	periods := map[string]string{"minute": "7", "5minute": "0", "15minute": "1", "30minute": "2", "60minute": "3", "week": "5", "month": "6", "quarter": "10", "year": "11"}
	names := make([]string, 0, len(periods))
	for name := range periods {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		typ := periods[name]
		all := strings.Contains(name, "minute")
		path := "GET /index/" + name
		if all {
			path += "/all"
		}
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			rr := r.Clone(r.Context())
			u := *r.URL
			rr.URL = &u
			q := u.Query()
			q.Set("type", typ)
			u.RawQuery = q.Encode()
			if all {
				s.handleIndexAll(w, rr)
			} else {
				s.handleIndex(w, rr)
			}
		})
	}
}
