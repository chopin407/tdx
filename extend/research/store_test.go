//go:build cgo

package research

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/injoyai/tdx/protocol"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestDuckDBUpsertSnapshotAndRollback(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	bars := fixtureBars(10)
	if err := s.Upsert(ctx, "sz000001", bars, []*protocol.Gbbq{}); err != nil {
		t.Fatal(err)
	}
	bars[9].Close += 0.01
	if err := s.Upsert(ctx, "sz000001", bars, []*protocol.Gbbq{}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Snapshot(ctx, nil, "2025-01-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Bars["sz000001"]) != 10 || d.Bars["sz000001"][9].Close != 11.01 {
		t.Fatal(d)
	}
	bars[0].Close = -1
	if err = s.Upsert(ctx, "sz000001", bars, nil); err == nil {
		t.Fatal("invalid transaction accepted")
	}
	d2, err := s.SnapshotWindow(ctx, nil, "2025-01-10", 3)
	if err != nil || len(d2.Bars["sz000001"]) != 3 || d2.Version != d.Version {
		t.Fatal(d2, err)
	}
}

type fakeSource struct {
	bars []Bar
	fail bool
}

func (f fakeSource) Symbols(context.Context) ([]string, error)            { return []string{"sz000001"}, nil }
func (f fakeSource) Daily(context.Context, string, string) ([]Bar, error) { return f.bars, nil }
func (f fakeSource) Actions(context.Context, string) ([]*protocol.Gbbq, error) {
	if f.fail {
		return nil, fmt.Errorf("action source down")
	}
	return []*protocol.Gbbq{}, nil
}
func TestUpdateFailureDoesNotPublishHalfSymbol(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	bars := fixtureBars(10)
	progress := func(string, int, error) {}
	err := s.Update(ctx, fakeSource{bars, true}, nil, "2025-01-10", progress)
	if err == nil {
		t.Fatal("failure lost")
	}
	d, err := s.Snapshot(ctx, nil, "2025-01-10")
	if err != nil || len(d.Bars) != 0 {
		t.Fatal(d, err)
	}
	if err = s.Update(ctx, fakeSource{bars, false}, nil, "2025-01-09", progress); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Snapshot(ctx, nil, "2025-01-10")
	if len(d.Bars["sz000001"]) != 9 {
		t.Fatal("unfinished bar published")
	}
}

func TestImportRerunAndHTTPWorkflow(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	root, err := filepath.Abs("../../output/testdata")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "research-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err = os.WriteFile(filepath.Join(dir, "sz000001.day"), dayBytes(), 0644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.ImportDirectory(ctx, dir, 100, "2025-01-10", func(string, int, error) {}); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := s.Snapshot(ctx, nil, "2025-01-10")
	if len(d.Bars["sz000001"]) != 1 {
		t.Fatal("import not idempotent")
	}
	if err = s.Upsert(ctx, "sz000001", fixtureBars(10), []*protocol.Gbbq{}); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ctx, s, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	h := Authenticate("test-token-for-local", service.Handler())
	call := func(method, path, body string, authorized bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if authorized {
			r.Header.Set("Authorization", "Bearer test-token-for-local")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/v1/status", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := call("PUT", "/v1/strategies/test", `{"kind":"ma_trend","fast":2,"slow":3,"lookback":3}`, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/v1/status", "/v1/bars?symbol=sz000001&end=2025-01-10&adjust=qfq", "/v1/dataset?symbol=sz000001&end=2025-01-10"} {
		if w = call("GET", path, "", true); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	w = call("POST", "/v1/screens", `{"strategy_id":"test","date":"2025-01-10"}`, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	request, _ := json.Marshal(map[string]any{"strategy_id": "test", "config": fixtureConfig()})
	w = call("POST", "/v1/backtests", string(request), true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var response struct {
		Data struct {
			ID        string `json:"id"`
			DatasetID string `json:"dataset_id"`
		} `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.DatasetID == "" {
		t.Fatal("missing reproducibility snapshot")
	}
	if w = call("GET", "/v1/artifacts/"+response.Data.DatasetID, "", true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = call("POST", "/v1/reviews", `{"date":"2025-01-10"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = call("PUT", "/v1/strategies/test", `{"kind":"ma_trend","fast":0,"slow":3,"lookback":3}`, true); w.Code != http.StatusBadRequest {
		t.Fatal(w.Code)
	}
}
func TestDailyJobPersistsReview(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.SaveStrategy(ctx, fixtureStrategy()); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ctx, s, Config{}, func(context.Context) (Source, func(), error) {
		return fakeSource{fixtureBars(10), false}, func() {}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	j, err := service.Start("daily", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		jobs, e := s.Jobs(ctx)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range jobs {
			if v.ID == j.ID && v.State != "running" {
				if v.State != "succeeded" || v.ArtifactID == "" {
					t.Fatal(v)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}

func TestOfflineImportInvalidatesActions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	bars := fixtureBars(5)
	if err := s.Upsert(ctx, "sz000001", bars, []*protocol.Gbbq{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(ctx, "sz000001", bars, nil); err != nil {
		t.Fatal(err)
	}
	d, err := s.Snapshot(ctx, nil, "2025-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if d.ActionsChecked["sz000001"] {
		t.Fatal("offline import reused potentially stale actions")
	}
}

func TestSchedulerDoesNotRepeatSuccessfulDay(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2025, 1, 10, 20, 0, 0, 0, Shanghai)
	if err := s.SaveJob(ctx, Job{ID: "completed", Kind: "daily", State: "succeeded", Started: now.Format(time.RFC3339), ScheduledDate: day(now)}); err != nil {
		t.Fatal(err)
	}
	called := false
	service, err := NewService(ctx, s, Config{Schedule: "19:00"}, func(context.Context) (Source, func(), error) { called = true; return fakeSource{}, func() {}, nil })
	if err != nil {
		t.Fatal(err)
	}
	service.scheduleTick(now)
	service.Close()
	if called {
		t.Fatal("repeated completed schedule")
	}
}

func TestReopenMarksInterruptedJob(t *testing.T) {
	root, err := filepath.Abs("../../output/testdata")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "recovery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "market.duckdb")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveJob(context.Background(), Job{ID: "unfinished", State: "running", Started: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jobs, err := s.Jobs(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].State != "interrupted" {
		t.Fatal(jobs, err)
	}
}
