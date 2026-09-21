package research

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrBusy = errors.New("another data job is running")

// Job retains partial progress; failed symbols can be replayed idempotently.
type Job struct {
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	State         string            `json:"state"`
	Started       string            `json:"started"`
	Finished      string            `json:"finished,omitempty"`
	Processed     int               `json:"processed"`
	Rows          int               `json:"rows"`
	Failures      int               `json:"failures"`
	Errors        map[string]string `json:"errors,omitempty"`
	Error         string            `json:"error,omitempty"`
	ArtifactID    string            `json:"artifact_id,omitempty"`
	ScheduledDate string            `json:"scheduled_date,omitempty"`
}
type Config struct {
	ImportDir  string
	PriceScale int
	Schedule   string
	Symbols    []string
}
type SourceFactory func(context.Context) (Source, func(), error)

// Service coordinates one writer queue, schedules and a persistent job ledger.
type Service struct {
	Store   *Store
	Config  Config
	Factory SourceFactory
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	busy    bool
	closed  bool
	wg      sync.WaitGroup
}

func NewService(parent context.Context, store *Store, c Config, f SourceFactory) (*Service, error) {
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
	progress := func(symbol string, n int, err error) {
		j.Processed++
		j.Rows += n
		if err != nil {
			j.Failures++
			if len(j.Errors) < 500 {
				j.Errors[symbol] = err.Error()
			}
		}
		if e := s.Store.SaveJob(s.ctx, j); e != nil {
			log.Printf("persist job progress: %v", e)
		}
	}
	cutoff := ClosedThrough(time.Now())
	var err error
	if j.Kind == "import" {
		err = s.Store.ImportDirectory(s.ctx, s.Config.ImportDir, s.Config.PriceScale, cutoff, progress)
	} else {
		var source Source
		var closeFn func()
		source, closeFn, err = s.Factory(s.ctx)
		if err == nil {
			if closeFn != nil {
				defer closeFn()
			}
			if len(symbols) == 0 {
				symbols = s.Config.Symbols
			}
			err = s.Store.Update(s.ctx, source, symbols, cutoff, progress)
		}
	}
	if err == nil && j.Kind == "daily" {
		var d *Dataset
		d, err = s.Store.SnapshotWindow(s.ctx, nil, cutoff, 501)
		if err == nil {
			date := ""
			for symbol, bars := range d.Bars {
				if kind(symbol) == "stock" && len(bars) > 0 && bars[len(bars)-1].Date > date {
					date = bars[len(bars)-1].Date
				}
			}
			if date == "" {
				err = fmt.Errorf("no completed A-share bars for review")
			} else {
				var strategies []Strategy
				strategies, err = s.Store.Strategies(s.ctx)
				if err == nil {
					var review ReviewResult
					review, err = Review(d, date, strategies)
					if err == nil {
						var mtfa MTFAScanResult
						mtfa, err = ScanMTFA(d, date, DefaultMTFAConfig())
						if err == nil {
							j.ArtifactID = uuid.NewString()
							err = s.Store.Artifact(s.ctx, j.ArtifactID, "daily-bundle", map[string]any{"review": review, "mtfa": mtfa})
						}
					}
				}
			}
		}
	}
	j.State = "succeeded"
	if err != nil {
		j.State = "failed"
		j.Error = err.Error()
	}
	j.Finished = time.Now().UTC().Format(time.RFC3339)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := s.Store.SaveJob(ctx, j); e != nil {
		log.Printf("persist final job: %v", e)
	}
}

// StartScheduler catches up after restart. Holidays are not guessed: the review
// is dated using actual returned bars. At most three attempts per calendar day.
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
	if now.Format("15:04") < s.Config.Schedule {
		return
	}
	date := day(now)
	jobs, err := s.Store.Jobs(s.ctx)
	if err != nil {
		log.Printf("scheduler job lookup: %v", err)
		return
	}
	attempts := 0
	for _, j := range jobs {
		if j.ScheduledDate != date {
			continue
		}
		attempts++
		if j.State == "succeeded" || j.State == "running" {
			return
		}
		started, _ := time.Parse(time.RFC3339, j.Started)
		if now.Sub(started) < 30*time.Minute {
			return
		}
	}
	if attempts >= 3 {
		return
	}
	if _, err = s.Start("daily", nil, date); err != nil && !errors.Is(err, ErrBusy) {
		log.Printf("scheduler: %v", err)
	}
}
