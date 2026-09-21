//go:build cgo

package main

import "testing"

func TestDefaultImportDirOnlyUsesEnvironment(t *testing.T) {
	t.Setenv("TDX_VIPDOC_DIR", " /srv/tdx/vipdoc ")
	if got := defaultImportDir(); got != "/srv/tdx/vipdoc" {
		t.Fatalf("unexpected import directory %q", got)
	}
	t.Setenv("TDX_VIPDOC_DIR", "")
	if got := defaultImportDir(); got != "" {
		t.Fatalf("machine-specific fallback leaked into configuration: %q", got)
	}
}
