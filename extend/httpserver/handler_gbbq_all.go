package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
)

// parseGbbqCodes validates an optional bounded batch before accessing the pool.
func parseGbbqCodes(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 100 {
		return nil, fmt.Errorf("codes 最多 100 个代码")
	}
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		ex, number, err := protocol.DecodeCode(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		if (ex != protocol.ExchangeSH && ex != protocol.ExchangeSZ && ex != protocol.ExchangeBJ) || len(number) != 6 {
			return nil, fmt.Errorf("仅支持沪深北六位证券代码")
		}
		for _, v := range number {
			if v < '0' || v > '9' {
				return nil, fmt.Errorf("证券代码必须为数字")
			}
		}
		code := ex.String() + number
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	return out, nil
}

func collectGbbq(ctx context.Context, codes []string, fetch func(string) (*protocol.GbbqResp, error)) (map[string][]*protocol.Gbbq, error) {
	out := make(map[string][]*protocol.Gbbq, len(codes))
	for _, code := range codes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := fetch(code)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", code, err)
		}
		if resp == nil {
			return nil, fmt.Errorf("%s: 股本响应为空", code)
		}
		rows := resp.List
		if rows == nil {
			rows = []*protocol.Gbbq{}
		}
		out[code] = rows
	}
	return out, nil
}

func (s *Server) handleGbbqAll(w http.ResponseWriter, r *http.Request) {
	codes, err := parseGbbqCodes(r.URL.Query().Get("codes"))
	if err != nil {
		respondErr(w, 400, err.Error())
		return
	}
	if codes == nil {
		err = s.pool.Do(func(c *tdx.Client) error {
			if e := r.Context().Err(); e != nil {
				return e
			}
			var e error
			codes, e = c.GetStockCodeAll()
			return e
		})
		if err != nil {
			respondErr(w, 502, err.Error())
			return
		}
	}
	// Release the pool between securities so normal quotes can interleave.
	result, err := collectGbbq(r.Context(), codes, func(code string) (*protocol.GbbqResp, error) {
		var resp *protocol.GbbqResp
		e := s.pool.Do(func(c *tdx.Client) error {
			if e := r.Context().Err(); e != nil {
				return e
			}
			var e error
			resp, e = c.GetGbbq(code)
			return e
		})
		return resp, e
	})
	if err != nil {
		respondErr(w, 502, err.Error())
		return
	}
	respondOK(w, result)
}
