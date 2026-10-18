package research

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrBusy = errors.New("another data job is running")

// Job retains partial progress; failed symbols can be replayed idempotently.
type Job struct {
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	State            string            `json:"state"`
	Started          string            `json:"started"`
	Finished         string            `json:"finished,omitempty"`
	Processed        int               `json:"processed"`
	Rows             int               `json:"rows"`
	Failures         int               `json:"failures"`
	Errors           map[string]string `json:"errors,omitempty"`
	Error            string            `json:"error,omitempty"`
	ScheduledDate    string            `json:"scheduled_date,omitempty"`
	Total            int               `json:"total,omitempty"`
	Updated          string            `json:"updated,omitempty"`
	LastSymbol       string            `json:"last_symbol,omitempty"`
	RetrySymbols     []string          `json:"retry_symbols,omitempty"`
	RetryOf          string            `json:"retry_of,omitempty"`
	RemainingSymbols []string          `json:"remaining_symbols,omitempty"`
}
type Config struct {
	ImportDir  string
	PriceScale int
	Schedule   string
	Symbols    []string
	JobTimeout time.Duration
}
type SourceFactory func(context.Context) (Source, func(), error)

// Service coordinates one writer queue, schedules and a persistent job ledger.
type Service struct {
	Store     *Store
	Config    Config
	Factory   SourceFactory
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	busy      bool
	closed    bool
	wg        sync.WaitGroup
	probeDate string
	probeAt   time.Time
	probeOpen bool
}

func NewService(parent context.Context, store *Store, c Config, f SourceFactory) (*Service, error) {
	if c.JobTimeout == 0 {
		c.JobTimeout = 2 * time.Hour
	}
	if c.JobTimeout < 0 {
		return nil, fmt.Errorf("job timeout must be positive")
	}
	if c.PriceScale != 0 && c.PriceScale != 100 && c.PriceScale != 1000 {
		return nil, fmt.Errorf("price scale must be 0, 100 or 1000")
	}
	if c.Schedule != "" {
		if _, err := time.Parse("15:04", c.Schedule); err != nil {
			return nil, fmt.Errorf("schedule must be HH:MM Shanghai")
		}
		if c.Schedule < "16:30" {
			return nil, fmt.Errorf("daily schedule must be at or after 16:30 Shanghai")
		}
	}
	for _, symbol := range c.Symbols {
		if !symbolRE.MatchString(symbol) {
			return nil, fmt.Errorf("invalid configured symbol %q", symbol)
		}
	}
	ctx, cancel := context.WithCancel(parent)
	return &Service{Store: store, Config: c, Factory: f, ctx: ctx, cancel: cancel}, nil
}
func (s *Service) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock(); s.wg.Wait() }

// Start runs a managed job; client disconnect does not cancel an accepted import.
func (s *Service) Start(kind string, symbols []string, scheduledDate string) (Job, error) {
	return s.start(kind, symbols, scheduledDate, "")
}

func (s *Service) start(kind string, symbols []string, scheduledDate, retryOf string) (Job, error) {
	if kind != "import" && kind != "update" && kind != "daily" {
		return Job{}, fmt.Errorf("unknown job kind")
	}
	for _, symbol := range symbols {
		if !symbolRE.MatchString(symbol) {
			return Job{}, fmt.Errorf("invalid symbol %q", symbol)
		}
	}
	if kind == "import" && s.Config.ImportDir == "" {
		return Job{}, fmt.Errorf("import directory not configured")
	}
	if kind != "import" && s.Factory == nil {
		return Job{}, fmt.Errorf("online source disabled")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, fmt.Errorf("service is closing")
	}
	if s.busy {
		return Job{}, ErrBusy
	}
	j := Job{ID: uuid.NewString(), Kind: kind, State: "running", Started: time.Now().UTC().Format(time.RFC3339), Errors: map[string]string{}, ScheduledDate: scheduledDate}
	j.Updated = j.Started
	j.RetryOf = retryOf
	if err := s.Store.SaveJob(s.ctx, j); err != nil {
		return Job{}, err
	}
	s.busy = true
	s.wg.Add(1)
	// Own the error map: the HTTP response may serialize while the worker updates.
	worker := j
	worker.Errors = map[string]string{}
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()
		s.run(worker, symbols)
	}()
	return j, nil
}
func (s *Service) run(j Job, symbols []string) {
	ctx, cancelJob := context.WithTimeout(s.ctx, s.Config.JobTimeout)
	defer cancelJob()
	progress := func(symbol string, n int, err error) {
		j.Processed++
		j.Rows += n
		j.Updated = time.Now().UTC().Format(time.RFC3339)
		j.LastSymbol = symbol
		if len(j.RemainingSymbols) > 0 {
			j.RemainingSymbols = j.RemainingSymbols[1:]
		}
		if err != nil {
			j.Failures++
			j.RetrySymbols = append(j.RetrySymbols, symbol)
			if len(j.Errors) < 500 {
				j.Errors[symbol] = err.Error()
			}
		}
		if e := s.Store.SaveJob(ctx, j); e != nil {
			log.Printf("persist job progress: %v", e)
		}
	}
	cutoff := ClosedThrough(time.Now())
	var err error
	if j.Kind == "import" {
		err = s.Store.ImportDirectory(ctx, s.Config.ImportDir, s.Config.PriceScale, cutoff, progress)
	} else {
		var source Source
		var closeFn func()
		source, closeFn, err = s.Factory(ctx)
		if err == nil {
			if closeFn != nil {
				defer closeFn()
			}
			if len(symbols) == 0 {
				symbols = s.Config.Symbols
			}
			symbols, err = updateSymbols(ctx, source, symbols)
			if err == nil {
				j.Total = len(symbols)
				j.RemainingSymbols = append([]string(nil), symbols...)
				if err = s.Store.SaveJob(ctx, j); err == nil {
					err = s.Store.updateSymbols(ctx, source, symbols, cutoff, progress)
				}
				j.RetrySymbols = append(j.RetrySymbols, j.RemainingSymbols...)
				j.RemainingSymbols = nil
			}
		}
	}
	j.State = "succeeded"
	if err != nil {
		j.State = "failed"
		if j.Processed > j.Failures || j.Rows > 0 {
			j.State = "partial"
		}
		j.Error = err.Error()
	}
	j.Finished = time.Now().UTC().Format(time.RFC3339)
	j.Updated = j.Finished
	finalCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := s.Store.SaveJob(finalCtx, j); e != nil {
		log.Printf("persist final job: %v", e)
	}
}

// StartScheduler catches up after restart and updates completed daily data.
// At most three attempts are made per calendar day.
func (s *Service) StartScheduler() {
	if s.Config.Schedule == "" || s.Factory == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			s.scheduleTick(time.Now())
			select {
			case <-s.ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}
func (s *Service) scheduleTick(now time.Time) {
	now = now.In(Shanghai)
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday || now.Format("15:04") < s.Config.Schedule {
		return
	}
	date := day(now)
	jobs, err := s.Store.Jobs(s.ctx)
	if err != nil {
		log.Printf("scheduler job lookup: %v", err)
		return
	}
	symbols, retryOf, ready := scheduleRetry(now, jobs)
	if !ready {
		return
	}
	if retryOf == "" && !s.confirmSession(now) {
		return
	}
	if _, err = s.start("daily", symbols, date, retryOf); err != nil && !errors.Is(err, ErrBusy) {
		log.Printf("scheduler: %v", err)
	}
}

// scheduleRetry never turns an incomplete legacy error list into a full rescan.
func scheduleRetry(now time.Time, jobs []Job) ([]string, string, bool) {
	date := day(now.In(Shanghai))
	attempts := 0
	var latest *Job
	for _, j := range jobs {
		if j.ScheduledDate != date {
			continue
		}
		attempts++
		if latest == nil || j.Started > latest.Started {
			copy := j
			latest = &copy
		}
		if j.State == "succeeded" || j.State == "running" {
			return nil, "", false
		}
		finished, _ := time.Parse(time.RFC3339, j.Finished)
		if finished.IsZero() {
			finished, _ = time.Parse(time.RFC3339, j.Started)
		}
		if now.Sub(finished) < 30*time.Minute {
			return nil, "", false
		}
	}
	if attempts >= 3 {
		return nil, "", false
	}
	var symbols []string
	retryOf := ""
	if latest != nil {
		retryOf = latest.ID
		symbols = latest.RetrySymbols
		// Old completed jobs have no retry plan. Recover only a complete error list;
		// never silently repeat the full universe after a partial legacy job.
		if len(symbols) == 0 && latest.Processed > 0 {
			if latest.Failures == 0 || len(latest.Errors) != latest.Failures || latest.Total > latest.Processed {
				return nil, "", false
			}
			if latest.Total == 0 && (latest.State == "interrupted" || latest.Error != fmt.Sprintf("%d symbols failed; inspect job errors", latest.Failures)) {
				// Only this legacy terminal error proves the whole universe was
				// traversed. A circuit-breaker/interruption also leaves unknown work.
				return nil, "", false
			}
			for symbol := range latest.Errors {
				symbols = append(symbols, symbol)
			}
			sort.Strings(symbols)
		}
	}
	return symbols, retryOf, true
}

// Follow Workday's existing index-bar approach: no invented holiday calendar.
// Probe failures and absent bars defer scheduling; retry after 30 minutes.
func (s *Service) confirmSession(now time.Time) bool {
	date := day(now)
	if s.probeDate == date && now.Sub(s.probeAt) < 30*time.Minute {
		return s.probeOpen
	}
	s.probeDate, s.probeAt, s.probeOpen = date, now, false
	ctx, cancel := context.WithTimeout(s.ctx, 40*time.Second)
	defer cancel()
	source, closeFn, err := s.Factory(ctx)
	if err != nil {
		log.Printf("scheduler session confirmation: %v", err)
		return false
	}
	if closeFn != nil {
		defer closeFn()
	}
	bars, err := source.Daily(ctx, "sh000001", date)
	if err != nil {
		log.Printf("scheduler session confirmation: %v", err)
		return false
	}
	for _, bar := range bars {
		if bar.Date == date && bar.Close > 0 {
			s.probeOpen = true
			break
		}
	}
	return s.probeOpen
}
