//go:build cgo

// tdx-research embeds the local research workflow and existing live HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend/httpserver"
	"github.com/injoyai/tdx/extend/research"
	"github.com/injoyai/tdx/lib/xorms"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func defaultImportDir() string {
	return strings.TrimSpace(os.Getenv("TDX_VIPDOC_DIR"))
}
func run() error {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "output/research/market.duckdb", "local DuckDB file")
	importDir := flag.String("import-dir", defaultImportDir(), "server-side vipdoc directory")
	scale := flag.Int("price-scale", 0, "offline price divisor: 0 by instrument, or explicit 100/1000")
	schedule := flag.String("schedule", "19:00", "daily Shanghai HH:MM; empty disables scheduling")
	symbolList := flag.String("symbols", "", "comma-separated update universe; empty discovers current A shares")
	offline := flag.Bool("offline", false, "disable live APIs and online jobs")
	flag.Parse()
	if *importDir != "" && !filepath.IsAbs(*importDir) {
		return fmt.Errorf("import directory must be an absolute path")
	}
	token := os.Getenv("TDX_API_TOKEN")
	if len(token) < 16 {
		return fmt.Errorf("set TDX_API_TOKEN to at least 16 characters")
	}
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0755); err != nil {
		return err
	}
	// Existing metadata caches also live under the output tree.
	cacheDir := filepath.Join(filepath.Dir(*dbPath), "tdx-cache")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := research.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	strategies, err := store.Strategies(ctx)
	if err != nil {
		return err
	}
	if len(strategies) == 0 {
		if err = store.SaveStrategy(ctx, research.Strategy{ID: "ma20", Name: "MA5/20 trend", Kind: "ma_trend", Fast: 5, Slow: 20, Lookback: 20}); err != nil {
			return err
		}
	}
	foundCup := false
	for _, strategy := range strategies {
		if strategy.ID == "weekly_cup_handle" {
			foundCup = true
			break
		}
	}
	if !foundCup {
		if err = store.SaveStrategy(ctx, research.Strategy{ID: "weekly_cup_handle", Name: "周线勺柄·双周缩量·反包", Kind: "weekly_cup_handle", Fast: 5, Slow: 20, Lookback: 20, MinAmount: 50000000}); err != nil {
			return err
		}
	}
	foundDoubleBottom := false
	for _, strategy := range strategies {
		if strategy.ID == "weekly_double_bottom" {
			foundDoubleBottom = true
			break
		}
	}
	if !foundDoubleBottom {
		if err = store.SaveStrategy(ctx, research.Strategy{ID: "weekly_double_bottom", Name: "周线W双底·放量破颈线", Kind: "weekly_double_bottom", Fast: 5, Slow: 20, Lookback: 20, MinAmount: 50000000}); err != nil {
			return err
		}
	}
	foundPlatform := false
	for _, strategy := range strategies {
		if strategy.ID == "weekly_platform_hold" {
			foundPlatform = true
			break
		}
	}
	if !foundPlatform {
		if err = store.SaveStrategy(ctx, research.Strategy{ID: "weekly_platform_hold", Name: "周线平台底·放量突破·缩量不破", Kind: "weekly_platform_hold", Fast: 5, Slow: 20, Lookback: 20, MinAmount: 50000000}); err != nil {
			return err
		}
	}
	var factory research.SourceFactory
	if !*offline {
		factory = func(ctx context.Context) (research.Source, func(), error) {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			return research.DialSource(ctx)
		}
	}
	symbols := []string{}
	if strings.TrimSpace(*symbolList) != "" {
		for _, v := range strings.Split(*symbolList, ",") {
			symbols = append(symbols, strings.TrimSpace(v))
		}
	}
	service, err := research.NewService(ctx, store, research.Config{ImportDir: *importDir, PriceScale: *scale, Schedule: *schedule, Symbols: symbols}, factory)
	if err != nil {
		return err
	}
	defer service.Close()
	mux := http.NewServeMux()
	mux.Handle("/v1/", service.Handler())
	var liveMu sync.Mutex
	var live *httpserver.Server
	// Lazy initialization leaves history and research available during outages.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if *offline {
			http.Error(w, "live source disabled", 503)
			return
		}
		liveMu.Lock()
		if live == nil {
			var liveErr error
			live, liveErr = httpserver.Default(httpserver.WithPoolSize(2), httpserver.WithCodesOptions(
				tdx.WithCodesDialDB(func() (*xorms.Engine, error) { return xorms.NewSqlite(filepath.Join(cacheDir, "codes.db")) }),
			))
			if liveErr != nil {
				liveMu.Unlock()
				http.Error(w, "live source unavailable", 503)
				return
			}
		}
		handler := live.Handler()
		liveMu.Unlock()
		handler.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: *addr, Handler: research.Authenticate(token, mux), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	service.StartScheduler()
	fail := make(chan error, 1)
	go func() { fail <- server.Serve(listener) }()
	log.Printf("tdx research listening on %s; database=%s", *addr, *dbPath)
	select {
	case <-ctx.Done():
	case err = <-fail:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = server.Shutdown(shutdown)
	if err != nil {
		_ = server.Close()
	}
	return err
}
