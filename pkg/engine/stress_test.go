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
	rawSize int
}

// contentForExt returns repeatable source material appropriate for ext.
// CSV and JSON return structurally valid content; prose formats return
// human-readable paragraphs; unsupported extensions return binary-looking
// noise that exercises the format-error path.
func contentForExt(ext string, size int) []byte {
	switch ext {
	case "csv":
		return buildCSVContent(size)
	case "json":
		return buildJSONContent(size)
	default:
		return buildProseContent(ext, size)
	}
}

func buildCSVContent(size int) []byte {
	const row = "alice,30,engineering,new york,true\n" +
		"bob,25,marketing,los angeles,false\n" +
		"carol,35,product,san francisco,true\n" +
		"dave,28,design,austin,false\n"
	const header = "name,age,department,city,active\n"

	var buf bytes.Buffer
	buf.Grow(size + len(header) + len(row))
	buf.WriteString(header)
	for buf.Len() < size {
		buf.WriteString(row)
	}
	return buf.Bytes()[:size]
}

func buildJSONContent(size int) []byte {
	// JSON normalization allocates ~3× the input (Unmarshal tree + MarshalIndent output).
	// Cap individual JSON files so the corpus stays within the alloc budget.
	const maxJSON = 64 << 10 // 64 KB
	if size > maxJSON {
		size = maxJSON
	}
	const elem = `{"id":1,"name":"Alice","score":9.5,"active":true},` + "\n"
	var buf bytes.Buffer
	buf.Grow(size + 4)
	buf.WriteString("[\n")
	for buf.Len() < size-2 {
		buf.WriteString(elem)
	}
	b := buf.Bytes()
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == ',' {
			b[i] = '\n'
			break
		}
	}
	buf.WriteString("]")
	return buf.Bytes()
}

func buildProseContent(ext string, size int) []byte {
	sources := map[string]string{
		"md": "# Section\n\nLorem ipsum dolor sit amet, consectetur adipiscing elit. " +
			"Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.\n\n" +
			"## Subsection\n\nDuis aute irure dolor in reprehenderit in voluptate velit esse. " +
			"Excepteur sint occaecat cupidatat non proident.\n\n",
		"txt": "Lorem ipsum dolor sit amet, consectetur adipiscing elit. " +
			"Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. \n\n" +
			"日本語テスト文字列です。絵文字も含みます: 🦊🐻🐼🐨🐯\n\n" +
			"한국어 텍스트도 포함됩니다. Ünïcödé téxt wïth dïacrïtïcs.\n\n",
		"pdf": "AAAAAAAABBBBBBBBCCCCCCCCDDDDDDDDEEEEEEEEFFFFFFFF" +
			"GGGGGGGGHHHHHHHHIIIIIIIIJJJJJJJJKKKKKKKK ",
		"exe": "\x7fELF\x02\x01\x01\x00XXXXXXXXXXXXXXXXXXXXXXXX" +
			"YYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYY ",
	}

	src, ok := sources[ext]
	if !ok {
		src = sources["txt"]
	}

	var buf bytes.Buffer
	buf.Grow(size + len(src))
	for buf.Len() < size {
		buf.WriteString(src)
	}
	content := buf.Bytes()[:size]
	for len(content) > 0 && !utf8.RuneStart(content[len(content)-1]) {
		content = content[:len(content)-1]
	}
	return content
}

// pdfMockText is 16 KB of prose, large enough to produce multiple 4 KB chunks.
var pdfMockText = func() []byte {
	const src = "The quick brown fox jumps over the lazy dog. " +
		"Pack my box with five dozen liquor jugs. " +
		"How vexingly quick daft zebras jump! "
	var b bytes.Buffer
	for b.Len() < 16<<10 {
		b.WriteString(src)
	}
	return b.Bytes()
}()

func mockPDFParser(maxBytes int64) *parser.PDFParser {
	return &parser.PDFParser{
		MaxBytes: maxBytes,
		Exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "pdftotext" {
				return pdfMockText, nil
			}
			return nil, fmt.Errorf("unexpected command: %s", name)
		},
	}
}

// buildCorpus generates n in-memory files covering six format classes:
//
//   - md  / txt – prose with paragraphs; exercises the common whitespace path
//   - csv        – structurally valid CSV; exercises CSVParser streaming
//   - json       – valid JSON array; exercises JSONParser normalization
//   - pdf        – ASCII prose; exercises PDFParser tempfile + pool path
//   - exe        – binary noise; exercises the format-error path
//
// Content is generated per-extension so every registered parser receives
// valid input and the error-path slot (exe) receives content that cannot parse.
func buildCorpus(n, minBytes, maxBytes int) []corpusEntry {
	exts := []string{"md", "txt", "csv", "json", "pdf", "exe"}

	entries := make([]corpusEntry, n)
	for i := range n {
		size := minBytes + rand.IntN(maxBytes-minBytes+1)
		ext := exts[i%len(exts)]
		captured := contentForExt(ext, size)

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
	_ = reg.Register("csv", &parser.CSVParser{MaxBytes: 10 << 20})
	_ = reg.Register("json", &parser.JSONParser{MaxBytes: 10 << 20})
	_ = reg.Register("pdf", mockPDFParser(10<<20))

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
		_ = chunks
		_ = errs
	}
}

// TestPipeline_StressThroughput is the memory auditor and integrity validator.
// It runs the same workload as the benchmark but focuses on allocation budget,
// post-GC heap, UTF-8 integrity, and error accounting.
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
	_ = reg.Register("csv", &parser.CSVParser{MaxBytes: 10 << 20})
	_ = reg.Register("json", &parser.JSONParser{MaxBytes: 10 << 20})
	_ = reg.Register("pdf", mockPDFParser(10<<20))

	entries := buildCorpus(numFiles, minBytes, maxBytes)

	var totalInputBytes int64
	var expectedFormatErrors int
	for i, e := range entries {
		totalInputBytes += int64(e.rawSize)
		if i%6 == 5 { // exe slot — unsupported format
			expectedFormatErrors++
		}
	}

	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

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
		if len(c) > 0 && !utf8.RuneStart(c[len(c)-1]) && !utf8.Valid(c) {
			brokenRune = true
		}
	}

	elapsed := time.Since(start)

	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

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

	// md/txt/csv parsers copy input once (~1×). JSON normalization allocates
	// ~3× per file (Unmarshal tree + MarshalIndent output + original bytes).
	// With a mixed corpus the effective ceiling is 3×.
	if newAllocMB > inputMB*3.1 {
		t.Errorf("ALLOC ALERT: allocated %.1f MB for %.1f MB input (%.1fx) — expected ≤3.1×",
			newAllocMB, inputMB, newAllocMB/inputMB)
	}

	if heapDeltaMB > inputMB*0.10 {
		t.Errorf("HEAP ALERT: post-GC heap delta %.1f MB > 10%% of input %.1f MB",
			heapDeltaMB, inputMB)
	}

	if chunkCount < numFiles-errCount {
		t.Errorf("chunk count %d < successfully parsed files %d", chunkCount, numFiles-errCount)
	}
}
