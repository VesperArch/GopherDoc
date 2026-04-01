package document

import (
	"bytes"
	"fmt"
	"iter"
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
				Metadata: copyMeta(doc.Metadata),
			}
			idx++
			if !yield(chunk) {
				return
			}
		}
	}
}

func copyMeta(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
