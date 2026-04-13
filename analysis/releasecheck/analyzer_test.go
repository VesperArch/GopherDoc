package releasecheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/vesperarch/gopherdoc/analysis/releasecheck"
)

// TestAnalyzer verifies the analyzer against bad/ (expected diagnostics) and
// good/ (zero diagnostics).
//
// analysistest uses GOPATH-mode loading (GO111MODULE=off, dir as GOPATH root),
// so EnginePkgPath is redirected to the flat stub package "engine" in
// testdata/src/ instead of the full module path.
func TestAnalyzer(t *testing.T) {
	old := releasecheck.EnginePkgPath
	releasecheck.EnginePkgPath = "engine"
	t.Cleanup(func() { releasecheck.EnginePkgPath = old })

	analysistest.Run(t, analysistest.TestData(), releasecheck.Analyzer, "bad", "good")
}
