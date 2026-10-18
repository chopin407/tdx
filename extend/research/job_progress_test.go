//go:build cgo

package research

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/tdx/protocol"
)

type progressSource struct {
	fail map[string]bool
	wait bool
}

func (s progressSource) Symbols(context.Context) ([]string, error) {
	return []string{"sz000001", "sz000002", "sz000003"}, nil
}
func (s progressSource) Actions(context.Context, string) ([]*protocol.Gbbq, error) {
	return []*protocol.Gbbq{}, nil
}
func (s progressSource) Daily(ctx context.Context, symbol, since string) ([]Bar, error) {
	if s.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.fail[symbol] {
		return nil, fmt.Errorf("upstream failed")
	}
	bars := fixtureBars(10)
	for i := range bars {
		bars[i].Symbol = symbol
	}
	return bars, nil
}

func TestJobPartialProgressAndFailedOnlyRetryPlan(t *testing.T) {
	s := testStore(t)
	service, err := NewService(context.Background(), s, Config{}, func(context.Context) (Source, func(), error) {
		return progressSource{fail: map[string]bool{"sz000002": true}}, func() {}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.run(Job{ID: "partial", Kind: "daily", State: "running", Errors: map[string]string{}}, nil)
	jobs, err := s.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	j := jobs[0]
	if j.State != "partial" || j.Processed != 3 || j.Total != 3 || j.Failures != 1 || j.Rows == 0 || j.Updated == "" || j.Finished == "" || j.LastSymbol != "sz000003" {
		t.Fatal(j)
	}
	if !reflect.DeepEqual(j.RetrySymbols, []string{"sz000002"}) || len(j.RemainingSymbols) != 0 {
		t.Fatal(j)
	}
}

func TestJobCircuitBreakerIncludesUnprocessedSymbols(t *testing.T) {
	s := testStore(t)
	symbols := []string{"sz000001", "sz000002", "sz000003", "sz000004", "sz000005", "sz000006", "sz000007"}
	fail := map[string]bool{}
	for _, symbol := range symbols {
		fail[symbol] = true
	}
	service, err := NewService(context.Background(), s, Config{}, func(context.Context) (Source, func(), error) { return progressSource{fail: fail}, func() {}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.run(Job{ID: "circuit", Kind: "daily", Errors: map[string]string{}}, symbols)
	jobs, err := s.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	j := jobs[0]
	if j.State != "failed" || j.Total != 7 || j.Processed != 5 || !reflect.DeepEqual(j.RetrySymbols, symbols) {
		t.Fatal(j)
	}
}

func TestJobDeadlinePersistsFinalProgressAndOutstanding(t *testing.T) {
	s := testStore(t)
	service, err := NewService(context.Background(), s, Config{JobTimeout: 20 * time.Millisecond}, func(context.Context) (Source, func(), error) { return progressSource{wait: true}, func() {}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.run(Job{ID: "timeout", Kind: "daily", Errors: map[string]string{}}, []string{"sz000001", "sz000002"})
	jobs, err := s.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	j := jobs[0]
	if j.State != "failed" || j.Finished == "" || j.Total != 2 || len(j.RetrySymbols) != 2 {
		t.Fatal(j)
	}
}
