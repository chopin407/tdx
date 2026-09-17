//go:build !cgo

package main

import "log"

func main() {
	log.Fatal("tdx-research requires CGO_ENABLED=1 and a C/C++ compiler for DuckDB; see docs/research.md")
}
