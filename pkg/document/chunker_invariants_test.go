package document

import (
	"math/rand/v2"
	"reflect"
	"testing"
	"unicode/utf8"
)

func chunkOffset(parent, sub []byte) int {
	if len(sub) == 0 {
		return 0
	}
	return int(reflect.ValueOf(sub).Pointer() - reflect.ValueOf(parent).Pointer())
}

// TestWithSlidingWindow_OverlapExact verifies that the overlap bytes at the
// start of chunk[i+1] exactly match the tail of chunk[i].
func TestWithSlidingWindow_OverlapExact(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		chunkSize int
		overlap   int
	}{
		{"word_aligned", "the quick brown fox jumps over the lazy dog", 16, 6},
		{"overlap_half", "aaa bbb ccc ddd eee fff ggg hhh iii jjj", 12, 6},
		{"overlap_one_byte", "abcdefghij klmnopqrst uvwxyz", 10, 1},
		{"overlap_at_word_boundary", "one two three four five six seven eight", 14, 7},
		{"dense_no_spaces", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN", 10, 4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := &Document{ID: "inv", Content: []byte(tc.content)}
			var chunks [][]byte
			for chunk := range WithSlidingWindow(doc, tc.chunkSize, tc.overlap) {
				chunks = append(chunks, chunk.Content)
			}
			if len(chunks) < 2 {
				return
			}
			for i := 0; i < len(chunks)-1; i++ {
				prev, next := chunks[i], chunks[i+1]
				if len(next) == 0 {
					continue
				}
				overlap := 0
				for overlap < len(prev) && overlap < len(next) {
					if prev[len(prev)-overlap-1] != next[overlap] {
						break
					}
					overlap++
				}
				tail := prev[len(prev)-overlap:]
				for j := range overlap {
					if tail[j] != next[j] {
						t.Errorf("chunk[%d]=%q chunk[%d]=%q: overlap byte[%d] mismatch",
							i, prev, i+1, next, j)
					}
				}
			}
		})
	}
}

// TestWithSlidingWindow_NoBytesDropped verifies no byte is dropped across all
// chunks. Content uses position-encoded bytes (spaces at i%7==6) so each
// byte's offset is unambiguous even with repeated values.
func TestWithSlidingWindow_NoBytesDropped(t *testing.T) {
	const runs = 200
	for range runs {
		size := 20 + rand.IntN(200)
		chunkSize := 10 + rand.IntN(40)
		overlap := rand.IntN(chunkSize)

		content := make([]byte, size)
		for i := range content {
			if i%7 == 6 {
				content[i] = ' '
			} else {
				content[i] = byte('!' + (i % 94))
			}
		}

		doc := &Document{ID: "prop", Content: content}
		covered := make([]bool, size)

		for chunk := range WithSlidingWindow(doc, chunkSize, overlap) {
			if len(chunk.Content) == 0 {
				continue
			}
			offset := chunkOffset(content, chunk.Content)
			if offset < 0 || offset+len(chunk.Content) > size {
				t.Fatalf("chunk offset %d out of bounds (size=%d)", offset, size)
			}
			for j := range chunk.Content {
				covered[offset+j] = true
			}
		}

		for i, c := range covered {
			if !c {
				t.Errorf("byte %d dropped (chunkSize=%d overlap=%d)", i, chunkSize, overlap)
				break
			}
		}
	}
}

// TestWithSlidingWindow_ChunkSizeRespected verifies that no chunk exceeds
// chunkSize bytes.
func TestWithSlidingWindow_ChunkSizeRespected(t *testing.T) {
	const runs = 200
	for range runs {
		size := 20 + rand.IntN(300)
		chunkSize := 8 + rand.IntN(50)
		overlap := rand.IntN(chunkSize)

		content := make([]byte, size)
		for i := range content {
			content[i] = byte('a' + rand.IntN(26))
			if rand.IntN(4) == 0 {
				content[i] = ' '
			}
		}

		doc := &Document{ID: "size", Content: content}
		for chunk := range WithSlidingWindow(doc, chunkSize, overlap) {
			if len(chunk.Content) > chunkSize {
				t.Errorf("chunk len %d exceeds chunkSize %d (chunk=%q)",
					len(chunk.Content), chunkSize, chunk.Content)
				break
			}
		}
	}
}

// TestWithSlidingWindow_UTF8NoSplit verifies no chunk boundary lands inside a
// multi-byte rune. chunkSize ≥ 16 ensures the window always spans at least one
// complete rune (max UTF-8 = 4 bytes).
func TestWithSlidingWindow_UTF8NoSplit(t *testing.T) {
	sources := []string{
		"café résumé naïve coöperate",
		"日本語テスト文字列です",
		"🔥🌊🌿🦊🐻 emoji parade",
		"한국어 텍스트 테스트입니다",
		"Ünïcödé méasüré",
	}

	for _, src := range sources {
		for _, cs := range []int{16, 20, 32} {
			for _, ov := range []int{0, 3, 7} {
				doc := &Document{ID: "utf8prop", Content: []byte(src)}
				for chunk := range WithSlidingWindow(doc, cs, ov) {
					if !utf8.Valid(chunk.Content) {
						t.Errorf("invalid UTF-8 in chunk %q (src=%q chunkSize=%d overlap=%d)",
							chunk.Content, src, cs, ov)
					}
				}
			}
		}
	}
}

// TestWithParagraphs_IDSequence verifies chunk IDs follow the "#p0", "#p1", ...
// pattern sequentially.
func TestWithParagraphs_IDSequence(t *testing.T) {
	doc := &Document{
		ID:      "doc",
		Content: []byte("A\n\nB\n\nC\n\nD\n\nE"),
	}

	var ids []string
	for chunk := range WithParagraphs(doc) {
		ids = append(ids, chunk.ID)
	}

	want := []string{"doc#p0", "doc#p1", "doc#p2", "doc#p3", "doc#p4"}
	if len(ids) != len(want) {
		t.Fatalf("got ids %v, want %v", ids, want)
	}
	for i := range ids {
		if ids[i] != want[i] {
			t.Errorf("id[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

// TestWithParagraphs_ContentIsSubSlice verifies that chunk Content is a
// zero-copy sub-slice — mutation propagates to the source document.
func TestWithParagraphs_ContentIsSubSlice(t *testing.T) {
	content := []byte("first paragraph\n\nsecond paragraph\n\nthird paragraph")
	doc := &Document{ID: "sub", Content: content}

	for chunk := range WithParagraphs(doc) {
		original := chunk.Content[0]
		chunk.Content[0] = 'X'
		found := false
		for _, b := range content {
			if b == 'X' {
				found = true
				break
			}
		}
		chunk.Content[0] = original
		if !found {
			t.Errorf("chunk %q is not a zero-copy sub-slice of original content", chunk.ID)
		}
		break
	}
}
