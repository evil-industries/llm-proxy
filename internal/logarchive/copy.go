package logarchive

import (
	"context"
	"io"
)

// copyWithContext checks cancellation between bounded reads, including for local
// files. Wrapping the reader hides WriterTo optimizations that bypass Read.
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, contextReader{ctx: ctx, reader: src})
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if errContext := r.ctx.Err(); errContext != nil {
		return 0, errContext
	}
	return r.reader.Read(buffer)
}
