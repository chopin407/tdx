//go:build cgo

package research

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/injoyai/tdx/protocol"
)

func TestHistoryBarsPageFromDuckDB(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Upsert(ctx, "sz000001", fixtureBars(10), []*protocol.Gbbq{}); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ctx, s, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	handler := Authenticate("test-token-for-local", service.Handler())
	call := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer test-token-for-local")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	var response struct {
		Data BarPage `json:"data"`
	}
	first := call("/v1/history/bars?symbol=sz000001&end=2025-01-10&limit=3")
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Bars) != 3 || response.Data.Bars[0].Date != "2025-01-08" || response.Data.Bars[2].Date != "2025-01-10" ||
		!response.Data.HasMore || response.Data.NextBefore != "2025-01-08" {
		t.Fatal(response.Data)
	}
	version := response.Data.DatasetVersion
	second := call(fmt.Sprintf("/v1/history/bars?symbol=sz000001&end=2025-01-10&before=%s&limit=3&version=%d&adjust=qfq", response.Data.NextBefore, version))
	if second.Code != 200 {
		t.Fatal(second.Body.String())
	}
	if err := json.Unmarshal(second.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Bars) != 3 || response.Data.Bars[0].Date != "2025-01-05" || response.Data.Bars[2].Date != "2025-01-07" {
		t.Fatal(response.Data)
	}
	if w := call("/v1/history/bars?symbol=sz000001&end=2025-01-10&start=2025-01-09&limit=3"); w.Code != 200 ||
		!strings.Contains(w.Body.String(), "\"has_more\":false") {
		t.Fatal(w.Body.String())
	}
	if w := call("/v1/history/bars?symbol=sz000001&end=2025-01-10&limit=1001"); w.Code != 400 {
		t.Fatal(w.Body.String())
	}
	if err := s.Upsert(ctx, "sz000001", fixtureBars(10), []*protocol.Gbbq{}); err != nil {
		t.Fatal(err)
	}
	if w := call(fmt.Sprintf("/v1/history/bars?symbol=sz000001&end=2025-01-10&version=%d", version)); w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	if err := s.Upsert(ctx, "sz000001", fixtureBars(10), nil); err != nil {
		t.Fatal(err)
	}
	if w := call("/v1/history/bars?symbol=sz000001&end=2025-01-10&adjust=qfq"); w.Code != 409 {
		t.Fatal(w.Body.String())
	}
}

func TestHistoryAdjustedPagesKeepEndDateAnchor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	eventDate, _ := parseDate("2025-01-06")
	actions := []*protocol.Gbbq{{Code: "sz000001", Time: eventDate.Add(15 * time.Hour), Category: 1, C1: 10}}
	if err := s.Upsert(ctx, "sz000001", fixtureBars(10), actions); err != nil {
		t.Fatal(err)
	}
	first, err := s.QueryBarsPage(ctx, "sz000001", "", "2025-01-10", "", "qfq", 3, nil)
	if err != nil || first.NextBefore != "2025-01-08" {
		t.Fatal(first, err)
	}
	second, err := s.QueryBarsPage(ctx, "sz000001", "", "2025-01-10", first.NextBefore, "qfq", 3, &first.DatasetVersion)
	if err != nil || len(second.Bars) != 3 || second.Bars[0].Date != "2025-01-05" {
		t.Fatal(second, err)
	}
	want := fixtureBars(10)[4].Close - 1
	if second.Bars[0].Close != want {
		t.Fatalf("adjusted close %.3f, want %.3f", second.Bars[0].Close, want)
	}
}

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
	var w *httptest.ResponseRecorder
	for _, path := range []string{"/v1/status", "/v1/data-coverage", "/v1/bars?symbol=sz000001&end=2025-01-10&adjust=qfq", "/v1/dataset?symbol=sz000001&end=2025-01-10"} {
		if w = call("GET", path, "", true); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	for _, path := range []string{"/v1/strategies", "/v1/mtfa/latest", "/v1/artifacts/old"} {
		if w = call("GET", path, "", true); w.Code != 404 {
			t.Fatalf("removed route %s returned %d", path, w.Code)
		}
	}
}
func TestDailyJobUpdatesData(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
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
				if v.State != "succeeded" || v.Rows == 0 {
					t.Fatal(v)
				}
				d, e := s.Snapshot(ctx, []string{"sz000001"}, "2025-01-10")
				if e != nil || len(d.Bars["sz000001"]) == 0 {
					t.Fatal(d, e)
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
