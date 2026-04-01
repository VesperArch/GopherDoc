package document

import (
	"bytes"
	"fmt"
	"iter"
	"unicode/utf8"

	metacopy "github.com/vesperarch/gopherdoc/internal/copy"
)

// WithParagraphs returns an iterator that yields one Document per
// paragraph (split on "\n\n"). Each yielded Document carries a deep
// copy of the source metadata and a chunk-specific ID. Empty
// paragraphs after trimming are skipped. Iteration stops early if the
// caller breaks out of the range loop.
func WithParagraphs(doc *Document) iter.Seq[*Document] {
	return func(yield func(*Document) bool) {
		parts := bytes.Split(doc.Content, []byte("\n\n"))
		idx := 0
		for _, raw := range parts {
			text := bytes.TrimSpace(raw)
			if len(text) == 0 {
				continue
			}
			chunk := &Document{
				ID:       fmt.Sprintf("%s#p%d", doc.ID, idx),
				Content:  text,
				Metadata: metacopy.Metadata(doc.Metadata),
			}
			idx++
			if !yield(chunk) {
				return
			}
		}
	}
}

// WithSlidingWindow returns an iterator that yields fixed-size chunks
// with a configurable overlap between consecutive windows. Each chunk
// is a zero-copy sub-slice of doc.Content (shares the backing array),
// guaranteeing O(1) memory per emitted Document.
//
// Boundary adjustment: neither the chunk end nor the overlap start will
// split a UTF-8 rune or break a word. When a boundary falls inside a
// word, the algorithm retreats to the nearest whitespace (space or
// newline). If no whitespace exists (a single token longer than
// chunkSize), it falls back to the nearest valid rune boundary.
//
// Each yielded Document carries a sequential ID ("{docID}#w{index}")
// and a deep copy of the source metadata.
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
				ID:       fmt.Sprintf("%s#w%d", doc.ID, idx),
				Content:  content[pos:end],
				Metadata: metacopy.Metadata(doc.Metadata),
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

// retreatChunkEnd moves targetEnd backwards to the nearest position
// that does not split a word. A valid end is one where content[end-1]
// or content[end] is whitespace, meaning the slice content[pos:end]
// ends at a natural word boundary.
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

// retreatOverlapStart adjusts the overlap origin backwards to the
// nearest word start (a position preceded by whitespace). If no
// boundary is found above minPos, returns fallback so the caller
// advances without overlap rather than looping forever.
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
