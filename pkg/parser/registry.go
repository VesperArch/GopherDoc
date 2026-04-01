package parser

import (
	"fmt"
	"strings"
	"sync"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

// Registry is a thread-safe store of Parser implementations keyed by
// file extension. It is optimized for concurrent read-heavy workloads
// through sync.RWMutex, allowing many goroutines to resolve parsers
// simultaneously while writes (registrations) remain exclusive.
type Registry struct {
	mu      sync.RWMutex
	parsers map[string]document.Parser
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{parsers: make(map[string]document.Parser)}
}

// Register associates a Parser with a file extension. The extension is
// stored in lower-case so that lookups are always case-insensitive.
// A nil parser or empty extension causes an error. Re-registering an
// existing extension silently overwrites the previous parser.
func (r *Registry) Register(ext string, p document.Parser) error {
	ext = normalizeExt(ext)
	if ext == "" {
		return fmt.Errorf("registry: extension must not be empty")
	}
	if p == nil {
		return fmt.Errorf("registry: parser must not be nil")
	}

	r.mu.Lock()
	r.parsers[ext] = p
	r.mu.Unlock()
	return nil
}

// Get returns the Parser registered for ext. The lookup is
// case-insensitive. If no parser is found, a descriptive error is
// returned so the caller can surface the unsupported format.
func (r *Registry) Get(ext string) (document.Parser, error) {
	ext = normalizeExt(ext)

	r.mu.RLock()
	p, ok := r.parsers[ext]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("registry: no parser registered for %q", ext)
	}
	return p, nil
}

// normalizeExt lower-cases the extension and ensures it has no leading dot.
func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	ext = strings.TrimPrefix(ext, ".")
	return ext
}
