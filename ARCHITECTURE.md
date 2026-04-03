# GopherDoc — Technical Design Document

**Audience:** Senior Software Engineers, Systems Architects, Computer Scientists
**Version:** v1.0.1 (`ea8f05c`)
**Status:** Current


## Table of Contents

1. [The Fundamental Problem](#1-the-fundamental-problem)
2. [Memory Management and Static Allocation](#2-memory-management-and-static-allocation)
3. [Zero-Copy Architecture](#3-zero-copy-architecture)
4. [Data Invariants and the Retreat Algorithm](#4-data-invariants-and-the-retreat-algorithm)
5. [Stability Guarantees via Property-Based Testing](#5-stability-guarantees-via-property-based-testing)
6. [Accepted Trade-offs](#6-accepted-trade-offs)


## 1. The Fundamental Problem

### 1.1 Heap Thrashing in Traditional RAG Pipelines

Ingestion tools built on managed runtimes (Python/LangChain, Java/LlamaIndex JVM) share a pathological allocation pattern: each document transits through multiple intermediate heap representations before producing a usable chunk.

A typical Python pipeline:

```
bytes (disk) → str (UTF-8 decode) → List[str] (split) → List[str] (chunk) → List[str] (overlap)
```

Each arrow represents a new, independent heap allocation. For a 10 MB document, the pipeline may hold 3–5× the input size simultaneously as Python objects — all subject to reference counting and the GIL. CPython's GC was designed for interactive latency, not batch throughput.

In Go, the problem persists differently. Libraries using `strings.Split`, `regexp.FindAllString`, or `json.Unmarshal` → `json.MarshalIndent` materialize full intermediate representations. A naive Go pipeline for JSON still produces:

```
bytes (io.Reader) → interface{} (Unmarshal tree) → string (MarshalIndent) → []byte (chunk)
```

The `interface{}` tree from `json.Unmarshal` is the core offender: each node is a separate heap allocation, generating GC pressure proportional to node count — not byte count.

### 1.2 Why Build from Scratch

The zero external dependencies constraint is not ideological — it is architectural. Any external dependency introduces a foreign allocation model that cannot be controlled. The objective was a verifiable guarantee: **constant post-GC residual heap independent of input volume**.

This is only achievable when you own the lifecycle of every allocation on the critical path.


## 2. Memory Management and Static Allocation

### 2.1 Explicit Buffer Ownership

`sync.Pool` is commonly used superficially: get a buffer, use it, return it. The problem with this naive pattern is that the return point rarely coincides with the end of the data's useful lifetime.

In GopherDoc, the buffer allocated by the parser cannot be returned immediately after parsing because it is the backing memory for the emitted chunks. Chunks are sub-slices of the original buffer — returning it to the pool while chunks are still in-flight is a classic race condition with silent data corruption consequences.

The solution is a transferable ownership model: the buffer travels with the data to its last consumer.

```go
// Document carries the backing buffer as an explicit field.
// PoolBuf is nil on all chunks except the last.
type Document struct {
    ID       string
    Content  []byte         // sub-slice of PoolBuf
    Metadata map[string]any
    PoolBuf  *bytes.Buffer  // nil except on the last chunk
}

func (d *Document) Release() {
    if d.PoolBuf != nil {
        pool.PutBuffer(d.PoolBuf)
        d.PoolBuf = nil
    }
}
```

### 2.2 The Last-Chunk Transfer Pattern

The pipeline emits N chunks per document. Only the last chunk needs to carry the responsibility of returning the buffer. The implementation uses a `prev` accumulator to detect the last element of an iterator without lookahead:

```go
var prev *document.Document
for chunk := range chunks {       // iter.Seq — pull without buffering
    if prev != nil {
        emit(ctx, out, Result{Doc: prev})  // prev is not last: PoolBuf = nil
    }
    prev = chunk
}
if prev != nil {
    prev.PoolBuf = doc.PoolBuf    // ownership transfer
    emit(ctx, out, Result{Doc: prev})
}
```

This pattern is required because `iter.Seq` (Go 1.23+) does not expose "last element" semantics — the only way to know an element is last is to have seen the next one.

### 2.3 Why the Residual Heap Is Constant

The 2.0 MB post-GC residual heap is a direct consequence of this model: content buffers never accumulate on the heap. Each buffer has exactly one live owner at a time, and that owner returns the buffer to the pool before going out of scope. The pool maintains a bounded number of reusable buffers (64 KB initial capacity, discarded if > 1 MB after growth), ensuring the pool's working set converges to a fixed value regardless of volume processed.

| Model | Steady-state heap for 2.1 GB input |
|---|---|
| Naive (new buffer per document) | Proportional to parallelism × average document size |
| Pool without explicit ownership | Race conditions → corruption or leaks |
| **GopherDoc (transferable ownership)** | **Constant: ~2.0 MB** |


## 3. Zero-Copy Architecture

### 3.1 Sub-slicing as an Alternative to memcpy

In Go, a slice is a three-field struct: `{ptr *T, len int, cap int}`. A sub-slice `content[a:b]` creates a new three-field struct pointing to the same underlying memory — O(1) cost, zero bytes copied.

```
content:          [  h  e  l  l  o     w  o  r  l  d  ]
                   0  1  2  3  4  5  6  7  8  9  10 11

chunk[0].Content: content[0:5]  → ptr=&content[0], len=5
chunk[1].Content: content[3:11] → ptr=&content[3], len=8  (2-byte overlap)
```

No content bytes are copied between parse and final consumer. The only allocation per chunk is the `Document` struct itself — hence the guarantee of **1 allocation per chunk** for text and Markdown formats.

### 3.2 Why JSON Breaks the 1-Allocation Guarantee

JSON is the only format where zero-copy does not apply at the parse layer. `json.Unmarshal` requires materializing the value tree to validate structure before any output. The adopted strategy minimizes the damage: use `json.NewEncoder` writing directly into a pooled buffer, avoiding the intermediate string allocation of `json.MarshalIndent`:

```go
buf := pool.GetBuffer()
enc := json.NewEncoder(buf)
enc.SetIndent("", "  ")
enc.Encode(v)  // writes directly into the pooled buffer
```

The allocation ratio for JSON converges to ~3.1× input (1× for the Unmarshal tree + ~2× for encoder output). Going below this baseline without reimplementing the JSON parser is not possible — an explicitly accepted trade-off.


## 4. Data Invariants and the Retreat Algorithm

### 4.1 The Problem with Naive Byte-Offset Cutting

A naive fixed-offset chunker fails in two scenarios:

**1. Cutting inside a multi-byte rune.** UTF-8 encodes codepoints U+0080 and above in 2–4 bytes. A cut at an arbitrary position may separate continuation bytes (`10xxxxxx`) from the start byte (`11xxxxxx`), producing invalid sequences that silently corrupt downstream embeddings.

**2. Cutting inside a word.** LLM tokenizers use BPE (Byte Pair Encoding) — tokens are derived from common substrings in running text. Cutting `"retriev|al"` produces two degraded tokens instead of one semantically rich token, measurably reducing embedding quality.

### 4.2 The Retreat Algorithm

The algorithm operates in two passes over the buffer, both O(k) where k ≤ chunkSize:

**Pass 1 — Retreat chunk end:**

```
Given: content[pos:end] as chunk candidate

IF content[end] or content[end-1] is whitespace:
    return end                        // clean boundary, no retreat needed

ELSE:
    scan content[end-1 .. pos] for whitespace
    IF found at i: return i+1

    // Fallback: no whitespace in range — align to UTF-8 rune boundary
    WHILE end > pos AND NOT utf8.RuneStart(content[end]):
        end--
    return end
```

`utf8.RuneStart(b)` returns `true` if and only if `b & 0xC0 != 0x80` — the byte is not a UTF-8 continuation byte. This guarantees every cut lands on a valid rune start.

**Pass 2 — Retreat overlap start:**

The overlap rewinds `overlapSize` bytes from the end of the previous chunk. This point may land inside a word, producing a chunk with a semantically degraded start:

```
Given: target = end - overlapSize

IF content[target-1] is whitespace: return target  // clean start

ELSE:
    scan content[target-1 .. minPos] for whitespace
    IF found at i: return i+1

    // Fallback: no boundary in range — return end (progress guaranteed)
    return fallback
```

The fallback to `end` (not `target`) is critical: it guarantees monotonic progress of `pos` even in dense content without ASCII whitespace (e.g., CJK text without separators), preventing infinite loops.

### 4.3 Complexity and Invariants

| Property | Guarantee |
|---|---|
| Complexity per chunk | O(chunkSize) — retreat bounded to the window |
| Total complexity | O(n) — each byte visited at most 2× (chunk + overlap) |
| UTF-8 validity | Guaranteed by `utf8.RuneStart` in the fallback path |
| Monotonicity of `pos` | `next > pos` always, by construction of the fallback |
| No bytes dropped | Verified by property-based testing (see §5) |

**Known limitation:** `chunkSize < 4` on 100% multi-byte content without whitespace (e.g., pure Kanji) can approximate loop behavior — retreat does not converge if `end - pos < 4` and no `RuneStart` is found in range. Documented; recommended mitigation: `chunkSize ≥ 16` for CJK corpora.


## 5. Stability Guarantees via Property-Based Testing

### 5.1 The Insufficiency of Example-Based Tests

Unit tests verify specific known cases. For a chunker, this is insufficient: the relevant input space includes all combinations of `(content, chunkSize, overlapSize)` — a combinatorial space that manual tests cannot cover.

The core property to verify is not "the output for this specific input is correct" but rather **"for any valid input, structural invariants hold"**.

### 5.2 The Verified Properties

**Property 1 — No bytes dropped:**

For any position-encoded content (`content[i] = '!' + (i % 94)`, spaces at `i%7==6`), the union of all chunks must contain each input position exactly once (excluding overlap bytes, which appear in two consecutive chunks).

Verification uses pointer arithmetic to determine the exact offset of each sub-slice within the parent buffer — eliminating the ambiguity of `bytes.Equal` on content with repeated bytes:

```go
func chunkOffset(parent, sub []byte) int {
    if len(sub) == 0 { return 0 }
    return int(reflect.ValueOf(sub).Pointer() - reflect.ValueOf(parent).Pointer())
}
```

**Property 2 — chunkSize respected:**

`∀ chunk: len(chunk.Content) ≤ chunkSize`

Retreat only reduces chunk size — never increases it. Verified across 200 runs with `chunkSize ∈ [16, 512]`.

**Property 3 — UTF-8 integrity:**

`∀ chunk: utf8.Valid(chunk.Content) == true`

Verified with content containing multi-byte runes (U+00E9, U+4E2D, U+1F600) at all offset combinations.

**Property 4 — Exact overlap:**

The last `overlapSize` bytes of chunk N are identical to the start of chunk N+1. Verified as sub-slice identity (same pointer, not `bytes.Equal`).

**Property 5 — Zero-copy confirmed:**

`chunk.Content` is a sub-slice of the original buffer — `reflect.ValueOf(chunk.Content).Pointer()` must fall within `[base, base+len(original))`. Any violation indicates an unintended copy.

### 5.3 Why 200 Runs Are Sufficient

200 runs do not provide exhaustive coverage — they provide high-probability detection of systematic failures. If a property fails on a fraction `p` of the input space, the probability that 200 consecutive runs do not detect the failure is `(1-p)^200`. For `p = 0.01` (1% of inputs are buggy), this probability is `0.134%` — acceptable for non-safety-critical infrastructure.

Position-encoded content was chosen specifically to eliminate false negatives from coincidence: with unique bytes per position, any reordering, drop, or duplication is detectable with certainty.


## 6. Accepted Trade-offs

### 6.1 Ownership Complexity in Exchange for Throughput

The transferable ownership model (`PoolBuf` on the last chunk, `Release()` mandatory for the consumer) imposes a non-trivial contract on the public API. A consumer that forgets to call `Release()` does not cause a crash — it causes a gradual pool leak (buffers do not return to the pool; new allocations compensate). This is detectable via heap profiling but not by the compiler.

**Alternative considered:** automatic return via `runtime.SetFinalizer`. Discarded because finalizers in Go are non-deterministic with respect to timing — they would introduce unpredictable GC latency on exactly the path being made predictable.

**Decision:** accept ownership complexity in exchange for determinism.

### 6.2 JSON: 3.1× Allocation in Exchange for Correctness

Normalizing JSON requires structural validation before any output — it is not possible to produce well-formed JSON in streaming without materializing the value tree. The 3.1× ratio is the theoretical minimum for any correct implementation in Go using `encoding/json`.

**Alternative considered:** a streaming JSON formatter that normalizes indentation without Unmarshal. Discarded due to implementation complexity and correctness risk on edge cases (arbitrary-precision numbers, Unicode escapes, nested structures).

**Decision:** accept 3.1× for JSON; document as the worst-case upper bound.

### 6.3 CJK Without Whitespace: Undefined Behavior for chunkSize < 4

The retreat algorithm assumes ASCII whitespace exists at sufficient frequency to find a boundary within the retreat window. In pure CJK corpora, this assumption fails.

**Alternative considered:** full Unicode word boundary detection (Unicode Standard Annex #29). Discarded for v1.x due to implementation cost and the zero external dependencies constraint — a complete UAX#29 implementation requires non-trivial lookup tables.

**Decision:** document the limitation, recommend `chunkSize ≥ 16`, schedule for v2.0 via deterministic finite automaton over per-rune Unicode properties.

### 6.4 Pooled bufio.Reader: No Seek in Exchange for Throughput

Parsers use a pooled `bufio.Reader` (64 KB) over `io.LimitReader`. This eliminates any possibility of seek or re-read on the stream. If a parser requires lookahead beyond the `bufio.Reader` internal buffer, the abstraction breaks.

For the supported formats (Markdown line-by-line, CSV line-by-line, JSON via `json.Decoder`, PlainText line-by-line), 64 KB of lookahead is more than sufficient. For future formats requiring seek (e.g., binary formats with an index at end-of-file), pooling `bufio.Reader` will be insufficient and will require a redesign of the parser contract.

**Decision:** accept the limitation for v1.x. The `io.Reader` contract on the `Parser` interface is intentional — it is not `io.ReadSeeker`.


*Implementation reference: [`github.com/VesperArch/GopherDoc`](https://github.com/VesperArch/GopherDoc) @ `v1.0.1`*
