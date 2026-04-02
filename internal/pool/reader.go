package pool

import (
	"bufio"
	"io"
	"sync"
)

const readerBufSize = 64 << 10 // 64 KB

var readerPool = sync.Pool{
	New: func() any {
		return bufio.NewReaderSize(nil, readerBufSize)
	},
}

// GetReader returns a pooled bufio.Reader reset to read from r.
func GetReader(r io.Reader) *bufio.Reader {
	br := readerPool.Get().(*bufio.Reader)
	br.Reset(r)
	return br
}

// PutReader releases the reference to the underlying reader and returns br to the pool.
func PutReader(br *bufio.Reader) {
	br.Reset(nil)
	readerPool.Put(br)
}
