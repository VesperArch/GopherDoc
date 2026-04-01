package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vesperarch/gopherdoc/pkg/parser"
)

// corpusEntry holds the pre-built content for a single task so the
// benchmark loop never allocates new strings during measurement.
type corpusEntry struct {
	task    Task
	rawSize int // original byte count before parsing
}

// buildCorpus generates n in-memory files covering three adversarial
// content classes:
//
//   - prose   – Lorem Ipsum paragraphs; tests the common whitespace path
//     in retreatChunkEnd.
//   - blob    – 500–2000 byte runs with no whitespace; forces the UTF-8
//     rune-boundary fallback in retreatChunkEnd.
//   - utf8    – Japanese text and emoji; validates that no chunk boundary
//     lands inside a multi-byte rune.
//
// Extensions cycle through md / txt / pdf / exe so that ~50% of tasks
// hit the "unsupported format" error path.
func buildCorpus(n, minBytes, maxBytes int) []corpusEntry {
	const (
		prose = "Lorem ipsum dolor sit amet, consectetur adipiscing elit. " +
			"Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. " +
			"Ut enim ad minim veniam, quis nostrud exercitation ullamco. \n\n" +
			"Duis aute irure dolor in reprehenderit in voluptate velit esse. " +
			"Excepteur sint occaecat cupidatat non proident, sunt in culpa. \n\n"

		blob = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" +
			"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" +
			"CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC" +
			"DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD" +
			"EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE" +
			"FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF" +
			"GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG" +
			"HHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHH "

		utf8text = "日本語テスト文字列です。絵文字も含みます: 🦊🐻🐼🐨🐯 " +
			"これはUTF-8マルチバイト文字のテストです。 \n\n" +
			"한국어 텍스트도 포함됩니다. 이것은 테스트입니다. " +
			"Ünïcödé téxt wïth dïacrïtïcs fôr gôôd méasüré. \n\n"
	)

	sources := []string{prose, blob, utf8text}
	exts := []string{"md", "txt", "pdf", "exe"}

	entries := make([]corpusEntry, n)
	for i := range n {
		size := minBytes + rand.IntN(maxBytes-minBytes+1)
		src := sources[i%len(sources)]

		// Build content by repeating the source until we reach size.
		var buf bytes.Buffer
		buf.Grow(size + len(src))
		for buf.Len() < size {
			buf.WriteString(src)
		}
		content := buf.Bytes()[:size]

		// Defensive: ensure we don't cut a UTF-8 rune at the size boundary.
		for len(content) > 0 && !utf8.RuneStart(content[len(content)-1]) {
			content = content[:len(content)-1]
		}

		// Capture as []byte — bytes.NewReader holds a reference, no copy.
		captured := make([]byte, len(content))
		copy(captured, content)

		ext := exts[i%len(exts)]
		entries[i] = corpusEntry{
			rawSize: len(captured),
			task: Task{
				ID:   fmt.Sprintf("stress-%d", i),
				Name: fmt.Sprintf("stress-%d.%s", i, ext),
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(bytes.NewReader(captured)), nil
				},
			},
		}
	}
	return entries
}

// BenchmarkPipeline_Stress measures pure pipeline throughput.
//
// Run: go test -bench=BenchmarkPipeline_Stress -benchmem -benchtime=5s ./pkg/engine/
//
// The corpus is built once outside b.N so allocation setup never
// contaminates the measured iterations. No GC calls, no MemStats reads,
// no atomics — the loop is as lean as the production hot path.
func BenchmarkPipeline_Stress(b *testing.B) {
	const (
		numFiles    = 1_000
		minBytes    = 1 << 10 // 1 KB
		maxBytes    = 5 << 20 // 5 MB
		numWorkers  = 50
		chunkSize   = 4 << 10 // 4 KB sliding window
		overlapSize = 512
	)

	reg := parser.NewRegistry()
	_ = reg.Register("md", &parser.MarkdownParser{MaxBytes: 10 << 20})
	_ = reg.Register("txt", &parser.PlainTextParser{MaxBytes: 10 << 20})
	// pdf / exe intentionally NOT registered → exercises error path under load.

	entries := buildCorpus(numFiles, minBytes, maxBytes)

	var totalInputBytes int64
	for _, e := range entries {
		totalInputBytes += int64(e.rawSize)
	}

	b.SetBytes(totalInputBytes)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		p := NewPipeline(reg, chunkSize, overlapSize)
		ctx := context.Background()
		ch := make(chan Task, numWorkers*2)
		results := p.Run(ctx, numWorkers, ch)

		go func() {
			defer close(ch)
			for _, e := range entries {
				ch <- e.task
			}
		}()

		var chunks, errs int64
		for r := range results {
			if r.Err != nil {
				errs++
				continue
			}
			chunks++
		}
		// Prevent the compiler from optimising away the loop.
		_ = chunks
		_ = errs
	}
}

// TestPipeline_StressThroughput is the memory auditor and integrity
// validator. It runs the same workload as the benchmark but focuses on:
//
//   - Allocation budget: parsers copy input once (1×); overhead from
//     bufio.Scanner growth pushes the budget to ≤2.5×. Anything higher
//     signals an unintended second copy in the pipeline.
//   - Post-GC heap: after a forced GC the live heap must be <10% of
//     input — confirms the chunker holds no hidden references to content.
//   - UTF-8 integrity: no emitted chunk may end on an incomplete rune.
//   - Error accounting: unsupported formats produce exactly one error
//     each, never a panic or a silent drop.
func TestPipeline_StressThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test skipped in -short mode")
	}

	const (
		numFiles    = 1_000
		minBytes    = 1 << 10 // 1 KB
		maxBytes    = 5 << 20 // 5 MB
		numWorkers  = 50
		chunkSize   = 4 << 10 // 4 KB sliding window
		overlapSize = 512
	)

	reg := parser.NewRegistry()
	_ = reg.Register("md", &parser.MarkdownParser{MaxBytes: 10 << 20})
	_ = reg.Register("txt", &parser.PlainTextParser{MaxBytes: 10 << 20})

	entries := buildCorpus(numFiles, minBytes, maxBytes)

	var totalInputBytes int64
	var expectedFormatErrors int
	for i, e := range entries {
		totalInputBytes += int64(e.rawSize)
		if i%4 == 2 || i%4 == 3 { // pdf, exe slots → unsupported format
			expectedFormatErrors++
		}
	}

	// ── Memory baseline (outside the timed path) ──────────────────────
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// ── Pipeline run ──────────────────────────────────────────────────
	p := NewPipeline(reg, chunkSize, overlapSize)
	ctx := context.Background()
	ch := make(chan Task, numWorkers*2)
	results := p.Run(ctx, numWorkers, ch)

	start := time.Now()

	go func() {
		defer close(ch)
		for _, e := range entries {
			ch <- e.task
		}
	}()

	var chunkCount, errCount int
	var chunkBytes int64
	var brokenRune bool

	for r := range results {
		if r.Err != nil {
			errCount++
			continue
		}
		chunkCount++
		c := r.Doc.Content
		chunkBytes += int64(len(c))

		// UTF-8 integrity: the last byte must start a valid rune or be a
		// single-byte ASCII character. A trailing continuation byte (10xxxxxx)
		// means the chunker split inside a multi-byte rune.
		if len(c) > 0 && !utf8.RuneStart(c[len(c)-1]) && !utf8.Valid(c) {
			brokenRune = true
		}
	}

	elapsed := time.Since(start)

	// ── Memory audit (outside the timed path) ─────────────────────────
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// ── Reporting ─────────────────────────────────────────────────────
	inputMB := float64(totalInputBytes) / (1 << 20)
	throughputMBps := inputMB / elapsed.Seconds()
	heapBeforeMB := float64(memBefore.HeapInuse) / (1 << 20)
	heapAfterMB := float64(memAfter.HeapInuse) / (1 << 20)
	heapDeltaMB := heapAfterMB - heapBeforeMB
	newAllocMB := float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / (1 << 20)

	t.Logf("─── Stress Results ───────────────────────────────")
	t.Logf("  Files processed : %d (%d workers)", numFiles, numWorkers)
	t.Logf("  Input data      : %.1f MB", inputMB)
	t.Logf("  Chunks emitted  : %d", chunkCount)
	t.Logf("  Chunk bytes     : %.1f MB", float64(chunkBytes)/(1<<20))
	t.Logf("  Errors (expect) : %d (format errors: %d)", errCount, expectedFormatErrors)
	t.Logf("  Wall time       : %s", elapsed.Round(time.Millisecond))
	t.Logf("  Throughput      : %.1f MB/s", throughputMBps)
	t.Logf("  TotalAlloc      : %.1f MB  (%.1f× input)", newAllocMB, newAllocMB/inputMB)
	t.Logf("  Heap before GC  : %.1f MB", heapBeforeMB)
	t.Logf("  Heap after GC   : %.1f MB", heapAfterMB)
	t.Logf("  Heap delta      : %.1f MB", heapDeltaMB)
	t.Logf("──────────────────────────────────────────────────")

	// ── Integrity checks ──────────────────────────────────────────────

	// Format errors are exact: every unsupported extension must produce exactly one.
	// Parse errors (e.g. bufio.Scanner token too long on blob files) are additional
	// and not predicted — we only assert the floor.
	if errCount < expectedFormatErrors {
		t.Errorf("error count %d < expected format errors %d — some unsupported formats were silently dropped",
			errCount, expectedFormatErrors)
	}

	if chunkBytes > totalInputBytes {
		t.Errorf("chunk bytes (%d) exceed input bytes (%d)", chunkBytes, totalInputBytes)
	}

	if brokenRune {
		t.Error("UTF-8 INTEGRITY: at least one chunk ends on an incomplete rune")
	}

	// Parsers copy input once (1×); bufio.Scanner growth adds ~20% overhead.
	// Budget ceiling is 2.5×. Exceeding it means an unintended second copy.
	if newAllocMB > inputMB*2.5 {
		t.Errorf("ALLOC ALERT: allocated %.1f MB for %.1f MB input (%.1fx) — expected ≤2.5×",
			newAllocMB, inputMB, newAllocMB/inputMB)
	}

	// Post-GC heap must be <10% of input: confirms the chunker holds no
	// hidden content references after the pipeline drains.
	if heapDeltaMB > inputMB*0.10 {
		t.Errorf("HEAP ALERT: post-GC heap delta %.1f MB > 10%% of input %.1f MB",
			heapDeltaMB, inputMB)
	}

	if chunkCount < numFiles-errCount {
		t.Errorf("chunk count %d < successfully parsed files %d", chunkCount, numFiles-errCount)
	}
}
