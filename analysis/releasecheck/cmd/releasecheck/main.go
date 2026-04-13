// Command releasecheck is a standalone go-vet-compatible binary that enforces
// GopherDoc's "Transferable Ownership" memory contract: every engine.Result
// received from a pipeline channel must have Doc.Release() called before the
// Result goes out of scope.
//
// Usage:
//
//	releasecheck ./...
//	go vet -vettool=$(which releasecheck) ./...
//
// Install:
//
//	go install github.com/vesperarch/gopherdoc/analysis/releasecheck/cmd/releasecheck@latest
package main

import (
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/vesperarch/gopherdoc/analysis/releasecheck"
)

func main() {
	multichecker.Main(releasecheck.Analyzer)
}
