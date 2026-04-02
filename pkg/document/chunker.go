package document

import (
	"bytes"
	"iter"
	"strconv"
	"unicode/utf8"
)

// paragraphSep is the double-newline paragraph boundary.
// Defined at package level to avoid allocating the slice on every iteration.
var paragraphSep = []byte("\n\n")

// WithParagraphs yields one Document per paragraph (split on "\n\n").
// Each chunk is a zero-copy sub-slice of doc.Content and shares the source
// document's Metadata by reference — callers must not mutate chunk.Metadata
// without copying it first.
func WithParagraphs(doc *Document) iter.Seq[*Document] {
	return func(yield func(*Document) bool) {
		content := doc.Content
		if len(content) == 0 {
			return
		}
		prefix := doc.ID + "#p"
		idx := 0
		pos := 0

		for {
			next := bytes.Index(content[pos:], paragraphSep)
			var raw []byte
			if next == -1 {
				raw = content[pos:]
			} else {
				raw = content[pos : pos+next]
			}

			text := bytes.TrimSpace(raw)
			if len(text) > 0 {
				chunk := &Document{
					ID:       buildChunkID(prefix, idx),
					Content:  text,
					Metadata: doc.Metadata,
				}
				idx++
				if !yield(chunk) {
					return
				}
			}

			if next == -1 {
				return
			}
			pos += next + len(paragraphSep)
		}
	}
}

// WithSlidingWindow yields fixed-size chunks with configurable overlap.
// Each chunk is a zero-copy sub-slice of doc.Content and shares the source
// document's Metadata by reference — callers must not mutate chunk.Metadata
// without copying it first.
//
// Chunk boundaries never split a UTF-8 rune or a word. When a boundary falls
// inside a word the algorithm retreats to the nearest whitespace; if none
// exists it falls back to the nearest rune boundary.
func WithSlidingWindow(doc *Document, chunkSize, overlapSize int) iter.Seq[*Document] {
	return func(yield func(*Document) bool) {
		content := doc.Content
		n := len(content)
		if n == 0 || chunkSize <= 0 {
			return
		}
		if overlapSize < 0 {
			overlapSize = 0
		}
		if overlapSize >= chunkSize {
			overlapSize = chunkSize - 1
		}

		prefix := doc.ID + "#w"
		idx := 0
		pos := 0

		for pos < n {
			end := pos + chunkSize
			if end >= n {
				end = n
			} else {
				end = retreatChunkEnd(content, pos, end)
			}

			chunk := &Document{
				ID:       buildChunkID(prefix, idx),
				Content:  content[pos:end],
				Metadata: doc.Metadata,
			}
			idx++
			if !yield(chunk) {
				return
			}

			if end >= n {
				break
			}

			next := end - overlapSize
			if next <= pos {
				pos = end
				continue
			}
			pos = retreatOverlapStart(content, pos+1, next, end)
		}
	}
}

// buildChunkID returns "{prefix}{n}" with a single heap allocation.
// The intermediate buffer is stack-allocated for IDs up to 256 bytes.
func buildChunkID(prefix string, n int) string {
	var arr [256]byte
	b := append(arr[:0], prefix...)
	b = strconv.AppendInt(b, int64(n), 10)
	return string(b)
}

// retreatChunkEnd moves targetEnd back to the nearest word boundary.
// Falls back to the nearest UTF-8 rune boundary if no whitespace is found.
func retreatChunkEnd(content []byte, minPos, targetEnd int) int {
	if isWhitespace(content[targetEnd]) || isWhitespace(content[targetEnd-1]) {
		return targetEnd
	}

	for i := targetEnd - 1; i > minPos; i-- {
		if isWhitespace(content[i]) {
			return i + 1
		}
	}

	// Force cut: no whitespace in range, align to UTF-8 rune boundary.
	for targetEnd > minPos && !utf8.RuneStart(content[targetEnd]) {
		targetEnd--
	}
	return targetEnd
}

// retreatOverlapStart moves target back to the nearest word start.
// Returns fallback when no boundary is found above minPos, ensuring progress.
func retreatOverlapStart(content []byte, minPos, target, fallback int) int {
	if target > 0 && isWhitespace(content[target-1]) {
		return target
	}

	for i := target - 1; i >= minPos; i-- {
		if isWhitespace(content[i]) {
			return i + 1
		}
	}

	return fallback
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\n'
}
