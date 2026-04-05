# GopherDoc

**[965 MB/s on a commodity i3. No external dependencies. Here's how.](ARCHITECTURE.md)**

**High-throughput streaming ingestion and chunking engine for RAG pipelines. Zero dependencies. Pure Go.**

[![Go 1.23+](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-zero-brightgreen)](go.mod)
[![High Performance](https://img.shields.io/badge/throughput-965%20MB%2Fs-orange)](pkg/engine/stress_test.go)

---

## Why GopherDoc?

Most ingestion libraries serialize all document bytes into memory before chunking. GopherDoc does not. Every allocation is accounted for, pooled, and released on a deterministic path.

| Metric | Value |
|---|---|
| **Sustained throughput** | **965 MB/s** |
| **Input processed** | **2.1 GB mixed corpus** |
| **Wall time** | **2.28 s** |
| **Post-GC residual heap** | **2.4 MB** |
| **Allocations (text/md)** | **1 per chunk** |
| **Alloc ratio (worst-case JSON)** | **≤ 3.1× input size** |

Hardware: Intel Core i3-10100F @ 3.60GHz. Corpus: Markdown, PlainText, CSV, JSON, PDF — 1,000 files, 1 KB–5 MB each, 50 concurrent workers. Throughput is the median of 3 runs; reproduce with:

```bash
go test -bench=BenchmarkPipeline_Stress -benchmem -benchtime=5s -count=3 ./pkg/engine/
```

---

## Key Features

**Zero-Copy Chunking.** `WithSlidingWindow` and `WithParagraphs` yield `[]byte` sub-slices of the original parse buffer. No data is copied at the chunking layer.

**`sync.Pool` Buffer Lifecycle.** Parse buffers (64 KB initial capacity) and `bufio.Reader` instances (64 KB) are pooled across goroutines. The owning buffer is transferred to the final chunk and returned to the pool by the caller after consumption.

**Word and UTF-8 Boundary Safety.** The sliding window retreats from raw byte positions to the nearest whitespace boundary, then falls back to the nearest UTF-8 rune start. No chunk ever begins or ends inside a multi-byte rune.

**Concurrent Pipeline.** A fixed goroutine pool consumes tasks from a buffered channel. Parser resolution, file I/O, and chunking are all coordinated through `context.Context` — cancellation propagates cleanly without goroutine leaks.

**PDF Extraction with OCR Fallback.** `PDFParser` shells out to `pdftotext` for text-layer PDFs and optionally falls back to `pdftoppm` + `tesseract` for scanned documents. The parser degrades gracefully when tools are absent — remaining formats continue unaffected.

**Format-Agnostic Registry.** `parser.Registry` decouples the pipeline from specific formats. Register any `document.Parser` implementation by extension at startup; the pipeline resolves it per-file at runtime.

**Structured Error Propagation.** Parse failures, I/O errors, and unsupported formats all surface as typed `engine.Result.Err` values — the result channel never closes early and never drops results.

---

## Installation

```bash
go get github.com/vesperarch/gopherdoc
```

Requires Go 1.23 or later. No transitive dependencies.

---

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "io"
    "log"
    "os"

    "github.com/vesperarch/gopherdoc/pkg/engine"
    "github.com/vesperarch/gopherdoc/pkg/parser"
)

func main() {
    const (
        chunkSize   = 4 << 10 // 4 KB
        overlapSize = 512
        numWorkers  = 8
    )

    reg := parser.NewRegistry()
    _ = reg.Register("md", &parser.MarkdownParser{MaxBytes: 10 << 20})
    _ = reg.Register("txt", &parser.PlainTextParser{MaxBytes: 10 << 20})
    _ = reg.Register("csv", &parser.CSVParser{MaxBytes: 10 << 20})
    _ = reg.Register("json", &parser.JSONParser{MaxBytes: 10 << 20})

    // PDF requires pdftotext in PATH. Available() returns a descriptive error
    // if the dependency is missing — remaining parsers are unaffected.
    pdfP := &parser.PDFParser{MaxBytes: 10 << 20, WithOCR: false}
    if err := pdfP.Available(); err == nil {
        _ = reg.Register("pdf", pdfP)
    }

    p := engine.NewPipeline(reg, chunkSize, overlapSize)
    ctx := context.Background()

    tasks := make(chan engine.Task, numWorkers*2)
    results := p.Run(ctx, numWorkers, tasks)

    // Submitter: close tasks when done so the pipeline drains cleanly.
    go func() {
        defer close(tasks)
        tasks <- engine.Task{
            ID:   "doc-001",
            Name: "report.pdf",
            Open: func() (io.ReadCloser, error) {
                return os.Open("report.pdf")
            },
        }
    }()

    for r := range results {
        if r.Err != nil {
            log.Printf("error: %v", r.Err)
            continue
        }

        fmt.Printf("chunk %s (%d bytes)\n", r.Doc.ID, len(r.Doc.Content))
        r.Doc.Release()
    }
}
```

**Paragraph mode** (split on `\n\n`, no fixed-size window): pass `chunkSize = 0` to `NewPipeline`.

---

## CLI

```bash
go run ./cmd/gopherdoc \
  -dir ./corpus \
  -workers 16 \
  -chunk-size 4096 \
  -overlap 512 \
  -limit 10485760 \
  -ocr
```

Walks `-dir` recursively, ingests all registered formats, and streams JSON-encoded chunks to stdout. Errors go to stderr. Exit code 1 if any file fails.

| Flag | Default | Description |
|---|---|---|
| `-dir` | `.` | Root directory to walk |
| `-workers` | `runtime.NumCPU()` | Concurrent worker goroutines |
| `-limit` | `10 MB` | Max bytes read per file |
| `-chunk-size` | `0` | Sliding window size in bytes (`0` = paragraph mode) |
| `-overlap` | `0` | Overlap between consecutive chunks in bytes |
| `-ocr` | `false` | Enable OCR fallback for scanned PDFs (requires `tesseract` and `pdftoppm`) |

---

## Architecture

```
Task (ID, Name, Open)
        │
        ▼
  engine.Pipeline.Run()              ← bounded goroutine pool
        │
        ├─ parser.Registry.Get(ext)
        │
        ├─ Parser.Parse(ctx, r)
        │   ├─ text/md/csv/json  → pool.GetReader / pool.GetBuffer (in-process)
        │   └─ pdf               → os.CreateTemp → pdftotext → pool.GetBuffer
        │                            └─ WithOCR: pdftoppm → tesseract (optional)
        │         └─ Document{Content []byte, PoolBuf *bytes.Buffer}
        │
        ├─ document.WithSlidingWindow()   ← zero-copy sub-slices
        │   or document.WithParagraphs()
        │
        └─ chan Result{Doc, Err}
                   │
                   ▼
            consumer range loop
                   └─ Doc.Release()  ← on last chunk
```

**Buffer ownership rule:** `Document.PoolBuf` is non-nil only on the **last chunk** emitted from a document. Call `Doc.Release()` after consuming that chunk's content to return the buffer to the internal pool.

---

## Operational Constraints

### Sliding Window — CJK and Multi-Byte Content

The retreat algorithm scans backward from the target boundary to find whitespace. In content composed entirely of multi-byte runes with no ASCII whitespace (e.g., dense Japanese or Chinese text), the algorithm falls back to UTF-8 rune alignment.

**Recommendation:** set `chunkSize ≥ 16` when processing CJK or emoji-dense content. Values below 4 on pure multi-byte content with no whitespace are not supported and produce undefined behaviour.

Native per-rune atomic boundary validation is scheduled for v2.0.

### PDF — System Dependencies

`PDFParser` requires `pdftotext` (poppler-utils) in `PATH`. Call `Available()` before registering to verify the dependency at startup. If absent, the parser is not registered and PDF files surface as `engine.Result.Err` with a clear message — no panic, no silent drop.

OCR fallback (`WithOCR: true`) additionally requires `pdftoppm` and `tesseract`. OCR is **off by default** — tesseract is typically 100–1000× slower than pdftotext. Enable only for corpora known to contain scanned documents.

PDF throughput is I/O-bound: each file requires a tempfile write before subprocess invocation. Expect ~600–800 MB/s on PDF-heavy workloads vs ~1,200 MB/s for text-only.

### Memory Limits

Each parser respects a `MaxBytes` cap via `io.LimitReader`. Files larger than `MaxBytes` are truncated, not rejected — bytes beyond the limit are silently discarded. Set an appropriate limit when ingesting untrusted input.

### Pool Buffer Cap

Content buffers larger than 1 MB after parsing are **not** returned to the pool (`pool.PutBuffer` discards them). This prevents large-file processing from permanently inflating per-goroutine pool memory.

---

## Roadmap

| Version | Feature |
|---|---|
| **v1.1** | ~~PDF extraction via `pdftotext`; OCR fallback via `tesseract`~~ — **shipped** |
| **v1.x** | Parser coverage: HTML, EPUB, DOCX via stdlib `encoding/xml` |
| **v2.0** | Atomic rune validation at chunk boundaries (no retreat loop for CJK) |
| **v2.x** | Semantic chunking: sentence-boundary detection using finite automata |

---

## License

MIT — see [LICENSE](LICENSE).
