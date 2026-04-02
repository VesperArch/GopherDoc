// Package pool provides reusable buffers and readers for hot paths.
package pool

import (
	"bytes"
	"sync"
)

const (
	bufferInitCap    = 64 << 10 // 64 KB
	maxPooledBufferCap = 1 << 20 // 1 MB
)

var bufPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, bufferInitCap))
	},
}

// GetBuffer returns a reset buffer from the pool.
func GetBuffer() *bytes.Buffer {
	return bufPool.Get().(*bytes.Buffer)
}

// PutBuffer returns b to the pool. Buffers larger than maxPooledBufferCap
// are discarded to prevent large-file processing from inflating pool memory.
func PutBuffer(b *bytes.Buffer) {
	if b.Cap() > maxPooledBufferCap {
		return
	}
	b.Reset()
	bufPool.Put(b)
}
