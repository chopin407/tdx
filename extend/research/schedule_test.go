package research

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/tdx/protocol"
)

type sessionSource struct {
	date  string
	calls *int
	fail  bool
}

func (s sessionSource) Symbols(context.Context) ([]string, error)                 { return nil, nil }
func (s sessionSource) Actions(context.Context, string) ([]*protocol.Gbbq, error) { return nil, nil }
func (s sessionSource) Daily(_ context.Context, symbol, since string) ([]Bar, error) {
	*s.calls++
	if symbol != "sh000001" || since != "2026-10-16" {
		return nil, fmt.Errorf("unexpected probe %s %s", symbol, since)
	}
	if s.fail {
		return nil, fmt.Errorf("offline")
	}
	return []Bar{{Date: s.date, Close: 3000}}, nil
}

func TestSessionProbeUsesActualIndexDay(t *testing.T) {
	for _, test := range []struct {
		name, date string
		fail, want bool
	}{
		{"open", "2026-10-16", false, true},
		{"holiday or lagging upstream", "2026-10-15", false, false},
		{"offline", "2026-10-16", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			s := &Service{ctx: context.Background(), Factory: func(context.Context) (Source, func(), error) {
				return sessionSource{test.date, &calls, test.fail}, func() {}, nil
			}}
			now := time.Date(2026, 10, 16, 19, 0, 0, 0, Shanghai)
			if got := s.confirmSession(now); got != test.want {
				t.Fatalf("got %v", got)
			}
			s.confirmSession(now.Add(time.Minute))
			if calls != 1 {
				t.Fatal("probe not cached", calls)
			}
			s.confirmSession(now.Add(30 * time.Minute))
			if calls != 2 {
				t.Fatal("probe not refreshed", calls)
			}
		})
	}
}

func TestSchedulerSkipsWeekendAndBeforeSchedule(t *testing.T) {
	if DefaultDailySchedule != "17:00" {
		t.Fatal("daily update must precede dashboard scans")
	}
	s := &Service{Config: Config{Schedule: DefaultDailySchedule}}
	// Store intentionally nil: neither path may query DB or open a connection.
	s.scheduleTick(time.Date(2026, 10, 17, 20, 0, 0, 0, Shanghai))
	s.scheduleTick(time.Date(2026, 10, 18, 20, 0, 0, 0, Shanghai))
	s.scheduleTick(time.Date(2026, 10, 16, 16, 59, 0, 0, Shanghai))
}

func TestScheduleRetryOnlyOutstandingAndFinishCooldown(t *testing.T) {
	now := time.Date(2026, 10, 16, 21, 0, 0, 0, Shanghai)
	j := Job{ID: "first", ScheduledDate: day(now), State: "partial", Started: now.Add(-2 * time.Hour).Format(time.RFC3339), Finished: now.Add(-31 * time.Minute).Format(time.RFC3339), Processed: 6, Total: 10, Failures: 2, RetrySymbols: []string{"sz000002", "sz000003", "sz000007", "sz000008", "sz000009", "sz000010"}}
	got, parent, ready := scheduleRetry(now, []Job{j})
	if !ready || parent != j.ID || !reflect.DeepEqual(got, j.RetrySymbols) {
		t.Fatal(got, parent, ready)
	}
	j.Finished = now.Add(-29 * time.Minute).Format(time.RFC3339)
	if _, _, ready = scheduleRetry(now, []Job{j}); ready {
		t.Fatal("retried before finish cooldown")
	}
}

func TestScheduleRetryLimitsAndLegacy(t *testing.T) {
	now := time.Date(2026, 10, 16, 23, 0, 0, 0, Shanghai)
	base := Job{ID: "first", ScheduledDate: day(now), State: "failed", Started: now.Add(-3 * time.Hour).Format(time.RFC3339), Processed: 10, Failures: 1, Error: "1 symbols failed; inspect job errors", Errors: map[string]string{"sz000002": "offline"}}
	got, _, ready := scheduleRetry(now, []Job{base})
	if !ready || !reflect.DeepEqual(got, []string{"sz000002"}) {
		t.Fatal(got, ready)
	}
	for _, state := range []string{"succeeded", "running"} {
		j := base
		j.State = state
		if _, _, ok := scheduleRetry(now, []Job{j}); ok {
			t.Fatal(state)
		}
	}
	j := base
	j.Failures = 2
	if _, _, ok := scheduleRetry(now, []Job{j}); ok {
		t.Fatal("incomplete errors retried")
	}
	j = base
	j.Total = 11
	if _, _, ok := scheduleRetry(now, []Job{j}); ok {
		t.Fatal("unknown unprocessed symbols retried")
	}
	j = base
	j.State = "interrupted"
	if _, _, ok := scheduleRetry(now, []Job{j}); ok {
		t.Fatal("legacy interrupted job lost unprocessed universe")
	}
	j = base
	j.Error = "stopped after five consecutive failed symbols"
	if _, _, ok := scheduleRetry(now, []Job{j}); ok {
		t.Fatal("legacy circuit breaker lost unprocessed universe")
	}
	if _, _, ok := scheduleRetry(now, []Job{base, base, base}); ok {
		t.Fatal("fourth attempt")
	}
	// A newer retry supersedes the earlier (larger) outstanding list.
	j = base
	j.ID = "retry"
	j.Started = now.Add(-time.Hour).Format(time.RFC3339)
	j.RetrySymbols = []string{"sz000003"}
	got, parent, ok := scheduleRetry(now, []Job{base, j})
	if !ok || parent != "retry" || !reflect.DeepEqual(got, j.RetrySymbols) {
		t.Fatal(got, parent, ok)
	}
}
