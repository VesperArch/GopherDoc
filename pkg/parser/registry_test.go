package parser

import (
	"sync"
	"testing"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p := &PlainTextParser{}

	if err := r.Register(".txt", p); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, err := r.Get("TXT")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != p {
		t.Fatal("returned parser does not match registered one")
	}
}

func TestRegistry_CaseInsensitive(t *testing.T) {
	r := NewRegistry()
	p := &MarkdownParser{}
	_ = r.Register(".MD", p)

	for _, ext := range []string{"md", ".md", "MD", ".Md"} {
		if _, err := r.Get(ext); err != nil {
			t.Errorf("Get(%q) failed: %v", ext, err)
		}
	}
}

func TestRegistry_NotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("unknown")
	if err == nil {
		t.Fatal("expected error for unregistered extension")
	}
}

func TestRegistry_NilParser(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("txt", nil); err == nil {
		t.Fatal("expected error for nil parser")
	}
}

func TestRegistry_EmptyExtension(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("", &PlainTextParser{}); err == nil {
		t.Fatal("expected error for empty extension")
	}
}

func TestRegistry_Overwrite(t *testing.T) {
	r := NewRegistry()
	p1 := &PlainTextParser{MaxBytes: 1}
	p2 := &PlainTextParser{MaxBytes: 2}

	_ = r.Register("txt", p1)
	_ = r.Register("txt", p2)

	got, _ := r.Get("txt")
	if got != p2 {
		t.Fatal("overwrite did not replace previous parser")
	}
}

func TestRegistry_ConcurrentReadHeavy(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("md", &MarkdownParser{})
	_ = r.Register("txt", &PlainTextParser{})

	const readers = 100
	var wg sync.WaitGroup
	wg.Add(readers)

	for range readers {
		go func() {
			defer wg.Done()
			for range 500 {
				if _, err := r.Get("md"); err != nil {
					t.Errorf("concurrent get: %v", err)
				}
				if _, err := r.Get("txt"); err != nil {
					t.Errorf("concurrent get: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestRegistry_ConcurrentReadWrite(t *testing.T) {
	r := NewRegistry()
	var parsers [10]document.Parser
	for i := range parsers {
		parsers[i] = &PlainTextParser{MaxBytes: int64(i + 1)}
	}

	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 100 {
				_ = r.Register("txt", parsers[(i+j)%len(parsers)])
			}
		}()
	}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				r.Get("txt")
			}
		}()
	}
	wg.Wait()
}
