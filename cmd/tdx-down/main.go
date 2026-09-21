package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/injoyai/tdx/extend"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configured := strings.TrimSpace(os.Getenv("TDX_VIPDOC_DIR"))
	vipdoc := flag.String("vipdoc-dir", configured, "absolute destination vipdoc directory; defaults to TDX_VIPDOC_DIR")
	download := flag.String("download-dir", "output/hsjday", "zip and metadata cache directory")
	flag.Parse()
	if *vipdoc == "" || !filepath.IsAbs(*vipdoc) {
		return fmt.Errorf("vipdoc directory must be an absolute path via TDX_VIPDOC_DIR or -vipdoc-dir")
	}
	clean := filepath.Clean(*vipdoc)
	if !strings.EqualFold(filepath.Base(clean), "vipdoc") {
		return fmt.Errorf("vipdoc directory must end with %q because the official archive contains a vipdoc root: %s", "vipdoc", clean)
	}
	zipPath, err := extend.DownloadAndUnzipHsjDay(*download, filepath.Dir(clean))
	if err != nil {
		return err
	}
	fmt.Printf("downloaded=%s vipdoc=%s\n", zipPath, clean)
	return nil
}
