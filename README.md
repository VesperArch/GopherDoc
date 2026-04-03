# GopherDoc

**High-throughput streaming ingestion and chunking engine for RAG pipelines. Zero dependencies. Pure Go.**

[![Go 1.23+](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-zero-brightgreen)](go.mod)
[![High Performance](https://img.shields.io/badge/throughput-1%2C210%20MB%2Fs-orange)](pkg/engine/stress_test.go)

## Why GopherDoc?

Most ingestion libraries serialize all document bytes into memory before chunking. GopherDoc does not. Every allocation is accounted for, pooled, and released on a deterministic path.

| Metric | Value |
|---|---|
| **Sustained throughput** | **1,210 MB/s** |
| **Input processed** | **2.1 GB mixed corpus** |
| **Wall time** | **3.49 s** |
| **Post-GC residual heap** | **2.0 MB** |
| **Allocations (text/md)** | **1 per chunk** |
| **Alloc ratio (worst-case JSON)** | **≤ 3.1× input size** |

The benchmark corpus covers Markdown, PlainText, CSV, JSON, and intentionally unsupported formats to exercise the full error path.

## Key Features

**Zero-Copy Chunking.** `WithSlidingWindow` and `WithParagraphs` both yield `[]byte` sub-slices of the original parse buffer. No data is copied at the chunking layer.

**`sync.Pool` Buffer Lifecycle.** Parse buffers (64 KB initial capacity) and `bufio.Reader` instances (64 KB) are pooled across goroutines. The owning buffer is transferred to the final chunk and returned to the pool by the caller after consumption.

**Word and UTF-8 Boundary Safety.** The sliding window retreats from raw byte positions to the nearest whitespace boundary, then falls back to the nearest UTF-8 rune start. No chunk ever begins or ends inside a multi-byte rune.

**Concurrent Pipeline.** A fixed goroutine pool consumes tasks from a buffered channel. Parser resolution, file I/O, and chunking are all coordinated through `context.Context` — cancellation propagates cleanly without goroutine leaks.

**Format-Agnostic Registry.** `parser.Registry` decouples the pipeline from specific formats. Register any `document.Parser` implementation by extension at startup; the pipeline resolves it per-file at runtime.

**Structured Error Propagation.** Parse failures, I/O errors, and unsupported formats all surface as typed `engine.Result.Err` values — the result channel never closes early and never drops results.


## Installation

```bash
go get github.com/vesperarch/gopherdoc
```

Requires Go 1.23 or later. No transitive dependencies.


## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "io"
    "log"
    "os"

    "github.com/vesperarch/gopherdoc/internal/pool"
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

    p := engine.NewPipeline(reg, chunkSize, overlapSize)
    ctx := context.Background()

    tasks := make(chan engine.Task, numWorkers*2)
    results := p.Run(ctx, numWorkers, tasks)

    // Submitter: close tasks when done so the pipeline drains cleanly.
    go func() {
        defer close(tasks)
        tasks <- engine.Task{
            ID:   "doc-001",
            Name: "article.md",
            Open: func() (io.ReadCloser, error) {
                return os.Open("article.md")
            },
        }
    }()

    for r := range results {
        if r.Err != nil {
            log.Printf("error: %v", r.Err)
            continue
        }

        fmt.Printf("chunk %s (%d bytes)\n", r.Doc.ID, len(r.Doc.Content))

        // Return the backing buffer to the pool after consuming the last chunk.
        if r.Doc.PoolBuf != nil {
            pool.PutBuffer(r.Doc.PoolBuf)
            r.Doc.PoolBuf = nil
        }
    }
}
```

**Paragraph mode** (no chunking, split on `\n\n`): pass `chunkSize = 0` to `NewPipeline`.

## CLI

```bash
go run ./cmd/gopherdoc \
  -dir ./corpus \
  -workers 16 \
  -chunk-size 4096 \
  -overlap 512 \
  -limit 10485760
```

Walks `-dir` recursively, ingests all registered formats, and streams JSON-encoded chunks to stdout. Errors go to stderr. Exit code 1 if any file fails.

| Flag | Default | Description |
|---|---|---|
| `-dir` | `.` | Root directory to walk |
| `-workers` | `runtime.NumCPU()` | Concurrent worker goroutines |
| `-limit` | `10 MB` | Max bytes read per file |
| `-chunk-size` | `0` | Sliding window size in bytes (`0` = paragraph mode) |
| `-overlap` | `0` | Overlap between consecutive chunks in bytes |


## Architecture

```
Task (ID, Name, Open)
        │
        ▼
  engine.Pipeline.Run()         ← bounded goroutine pool
        │
        ├─ parser.Registry.Get(ext)
        ├─ Parser.Parse(ctx, r) ← pool.GetReader / pool.GetBuffer
        │         └─ Document{Content []byte, PoolBuf *bytes.Buffer}
        │
        ├─ document.WithSlidingWindow()  ← zero-copy sub-slices
        │   or document.WithParagraphs()
        │
        └─ chan Result{Doc, Err}
                   │
                   ▼
            consumer range loop
                   └─ pool.PutBuffer(Doc.PoolBuf)  ← on last chunk
```

**Buffer ownership rule:** `Document.PoolBuf` is non-nil only on the **last chunk** emitted from a document. The consumer is responsible for calling `pool.PutBuffer` after reading that chunk's content.


## Operational Constraints

### Sliding Window — CJK and Multi-Byte Content

The retreat algorithm scans backward from the target boundary to find whitespace. In content composed entirely of multi-byte runes with no ASCII whitespace (e.g., dense Japanese or Chinese text), the algorithm falls back to UTF-8 rune alignment.

**Recommendation:** set `chunkSize ≥ 16` when processing CJK or emoji-dense content. Values below 4 on pure multi-byte content with no whitespace are not supported and will produce undefined behaviour.

Native per-rune atomic boundary validation is scheduled for v2.0.

### Memory Limits

Each parser respects a `MaxBytes` cap via `io.LimitReader`. Files larger than `MaxBytes` are truncated, not rejected — trailing bytes that cross the limit are silently discarded. Set an appropriate limit when ingesting untrusted input.

### Pool Buffer Cap

Content buffers larger than 1 MB after parsing are **not** returned to the pool (`pool.PutBuffer` discards them). This prevents large-file processing from permanently inflating per-goroutine pool memory.


## Roadmap

| Version | Feature |
|---|---|
| **v1.x** | Parser coverage: HTML, EPUB, DOCX via stdlib `encoding/xml` |
| **v2.0** | PDF extraction via `os/exec` (pdftotext); OCR fallback (tesseract) |
| **v2.0** | Atomic rune validation at chunk boundaries (no retreat loop) |
| **v2.x** | Semantic chunking: sentence-boundary detection using finite automata |


## License

MIT — see [LICENSE](LICENSE).
